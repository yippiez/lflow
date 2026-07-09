package editor

import (
	"encoding/json"
	"unicode/utf8"
)

// Guards so a runaway command (a huge rg, a catted binary) cannot balloon
// memory, the DB, or the render loop: a band keeps only the newest maxRunLines
// lines — the head is dropped and counted (m.runDropped) — every captured line
// is clipped to maxRunLineLen bytes, and the persisted row is byte-budgeted
// from the tail.
const (
	maxRunLines        = 5000
	maxRunLineLen      = 4096
	maxRunPersistBytes = 512 << 10
)

// appendRunOut adds one captured line to a node's run band, enforcing the line
// and band caps above. Every streamed line goes through here.
func (m *Model) appendRunOut(uuid string, l outLine) {
	if len(l.text) > maxRunLineLen {
		cut := maxRunLineLen
		for cut > 0 && !utf8.RuneStart(l.text[cut]) {
			cut--
		}
		l.text = l.text[:cut] + "…"
	}
	out := append(m.runOut[uuid], l)
	if over := len(out) - maxRunLines; over > 0 {
		out = out[over:]
		if m.runDropped == nil {
			m.runDropped = map[string]int{}
		}
		m.runDropped[uuid] += over
	}
	m.runOut[uuid] = out
}

// A runnable node's captured output (bash/query stdout/stderr) is ephemeral in
// memory, but it is also mirrored into the node_output DB table so it survives a
// restart — the outline reads the same way it looked when you last ran the
// command. The table is keyed by node uuid and decoupled from the node row, so
// output persists the instant a run finishes (even before the node is saved).
//
// WARNING (invariant): run output is local-only — node_output is never synced and
// never enters the synced node payload. It is not notebook content.

// outLineDisk is the serialisable form of outLine (its fields are unexported).
type outLineDisk struct {
	Text string `json:"t"`
	Err  bool   `json:"e,omitempty"`
}

// ensureRunOutLoaded lazily hydrates a node's run band from node_output the first
// time it is rendered, so persisted output shows after a restart. A node that is
// currently running already has its lines in memory and is left untouched.
func (m *Model) ensureRunOutLoaded(uuid string) {
	if m.runOutLoaded == nil {
		m.runOutLoaded = map[string]bool{}
	}
	if m.runOutLoaded[uuid] {
		return
	}
	m.runOutLoaded[uuid] = true // mark first: a missing/garbled row is not retried
	if m.ctx.DB == nil {
		return
	}

	var raw string
	if err := m.ctx.DB.QueryRow("SELECT output FROM node_output WHERE uuid = ?", uuid).Scan(&raw); err != nil {
		return // never run, or no persisted output
	}
	if raw == "" {
		return
	}
	var disk []outLineDisk
	if json.Unmarshal([]byte(raw), &disk) != nil {
		return
	}
	if m.runOut == nil {
		m.runOut = map[string][]outLine{}
	}
	out := make([]outLine, len(disk))
	for i, l := range disk {
		out[i] = outLine{text: l.Text, err: l.Err}
	}
	m.runOut[uuid] = out
}

// persistRunOut writes a node's accumulated run band to node_output (overwriting
// any previous run). An empty band deletes the row, so a re-run that produced
// nothing clears stale output. Best-effort: a write error never blocks the run.
func (m *Model) persistRunOut(uuid string) {
	if m.runOutLoaded == nil {
		m.runOutLoaded = map[string]bool{}
	}
	m.runOutLoaded[uuid] = true // memory is now the source of truth for this uuid
	if m.ctx.DB == nil {
		return
	}

	out := m.runOut[uuid]
	if len(out) == 0 {
		_, _ = m.ctx.DB.Exec("DELETE FROM node_output WHERE uuid = ?", uuid)
		return
	}
	// byte-budget the row from the tail — the newest lines are the ones worth
	// keeping, and one giant run must not bloat the DB
	start, budget := len(out), maxRunPersistBytes
	for start > 0 && budget >= len(out[start-1].text)+16 {
		budget -= len(out[start-1].text) + 16
		start--
	}
	out = out[start:]
	disk := make([]outLineDisk, len(out))
	for i, l := range out {
		disk[i] = outLineDisk{Text: l.text, Err: l.err}
	}
	data, err := json.Marshal(disk)
	if err != nil {
		return
	}
	_, _ = m.ctx.DB.Exec(
		"INSERT INTO node_output (uuid, output) VALUES (?, ?) ON CONFLICT(uuid) DO UPDATE SET output = excluded.output",
		uuid, string(data))
}

// deleteRunOut drops a node's persisted run band — called when the node itself
// is removed so the row does not outlive it.
func (m *Model) deleteRunOut(uuid string) {
	if m.ctx.DB == nil {
		return
	}
	_, _ = m.ctx.DB.Exec("DELETE FROM node_output WHERE uuid = ?", uuid)
}
