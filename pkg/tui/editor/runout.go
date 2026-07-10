package editor

import (
	"encoding/json"
	"unicode/utf8"

	"github.com/lflow/lflow/pkg/tui/database"
)

// Guards so a runaway command (a huge rg, a catted binary) cannot balloon
// memory, the DB, or the render loop: a band keeps only the newest maxRunLines
// lines — the head is dropped and counted (runState.dropped) — every captured line
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
	r := m.ensureRun(uuid)
	out := append(r.out, l)
	if over := len(out) - maxRunLines; over > 0 {
		out = out[over:]
		r.dropped += over
	}
	r.out = out
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

// runOutDisk is the persisted local-only run band. Older rows were a bare
// []outLineDisk; ensureRunOutLoaded keeps that shape readable.
type runOutDisk struct {
	PWD   string        `json:"pwd,omitempty"`
	Lines []outLineDisk `json:"lines"`
}

// ensureRunOutLoaded lazily hydrates a node's run band from node_output the first
// time it is rendered, so persisted output shows after a restart. A node that is
// currently running already has its lines in memory and is left untouched.
func (m *Model) ensureRunOutLoaded(uuid string) {
	r := m.ensureRun(uuid)
	if r.loaded {
		return
	}
	r.loaded = true // mark first: a missing/garbled row is not retried
	if m.ctx.DB == nil {
		return
	}

	raw, err := database.LoadNodeOutput(m.ctx.DB, uuid)
	if err != nil || raw == "" {
		return // never run, or no persisted output
	}
	var row runOutDisk
	if err := json.Unmarshal([]byte(raw), &row); err != nil {
		var legacy []outLineDisk
		if json.Unmarshal([]byte(raw), &legacy) != nil {
			return
		}
		row.Lines = legacy
	}
	if row.PWD != "" {
		r.pwd = row.PWD
	}
	out := make([]outLine, len(row.Lines))
	for i, l := range row.Lines {
		out[i] = outLine{text: l.Text, err: l.Err}
	}
	r.out = out
}

// persistRunOut writes a node's accumulated run band to node_output (overwriting
// any previous run). An empty band deletes the row, so a re-run that produced
// nothing clears stale output. Best-effort: a write error never blocks the run.
func (m *Model) persistRunOut(uuid string) {
	r := m.ensureRun(uuid)
	r.loaded = true // memory is now the source of truth for this uuid
	if m.ctx.DB == nil {
		return
	}

	out := r.out
	if len(out) == 0 {
		_ = database.DeleteNodeOutput(m.ctx.DB, uuid)
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
	data, err := json.Marshal(runOutDisk{PWD: r.pwd, Lines: disk})
	if err != nil {
		return
	}
	_ = database.SaveNodeOutput(m.ctx.DB, uuid, string(data))
}

// deleteRunOut drops a node's persisted run band — called when the node itself
// is removed so the row does not outlive it.
func (m *Model) deleteRunOut(uuid string) {
	// stop a still-running command before dropping its state — otherwise the
	// cancel func goes with the delete and the process is orphaned (quit can no
	// longer reap it, since it ranges m.runs).
	if r := m.run(uuid); r != nil && r.cancel != nil {
		r.cancel()
	}
	delete(m.runs, uuid) // one delete drops the band, drop count, pwd and loaded flag
	if m.ctx.DB == nil {
		return
	}
	_ = database.DeleteNodeOutput(m.ctx.DB, uuid)
}
