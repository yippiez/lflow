package editor

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// A runnable node's captured output (bash stdout/stderr) is ephemeral in memory,
// but it is also mirrored to a local JSON file so it survives a restart — the
// outline reads the same way it looked when you last ran the command. Like the
// voice wav, this cache lives under the data dir; it is NEVER in the DB or sync
// (run output is not notebook content), so no migration and no churn.
//
// Query output is already persistent (it materializes as mirror child nodes) and
// a worker's deliverable is harvested into real nodes, so this covers the one
// output that was being dropped: a bash node's run band.

func (m *Model) runOutPath(uuid string) string {
	return filepath.Join(m.ctx.Paths.Data, "lflow", "runout", uuid+".json")
}

// outLineDisk is the serialisable form of outLine (its fields are unexported).
type outLineDisk struct {
	Text string `json:"t"`
	Err  bool   `json:"e,omitempty"`
}

// ensureRunOutLoaded lazily hydrates a node's run band from disk the first time
// it is rendered, so persisted output shows after a restart. A node that is
// currently running already has its lines in memory and is left untouched.
func (m *Model) ensureRunOutLoaded(uuid string) {
	if m.runOutLoaded == nil {
		m.runOutLoaded = map[string]bool{}
	}
	if m.runOutLoaded[uuid] {
		return
	}
	m.runOutLoaded[uuid] = true // mark first: a missing/garbled file is not retried

	data, err := os.ReadFile(m.runOutPath(uuid))
	if err != nil {
		return // never run, or no persisted output
	}
	var disk []outLineDisk
	if json.Unmarshal(data, &disk) != nil {
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

// persistRunOut writes a node's accumulated run band to disk (overwriting any
// previous run). An empty band removes the file, so a re-run that produced
// nothing clears stale output. Best-effort: a write error never blocks the run.
func (m *Model) persistRunOut(uuid string) {
	if m.runOutLoaded == nil {
		m.runOutLoaded = map[string]bool{}
	}
	m.runOutLoaded[uuid] = true // memory is now the source of truth for this uuid

	out := m.runOut[uuid]
	path := m.runOutPath(uuid)
	if len(out) == 0 {
		_ = os.Remove(path)
		return
	}
	disk := make([]outLineDisk, len(out))
	for i, l := range out {
		disk[i] = outLineDisk{Text: l.text, Err: l.err}
	}
	data, err := json.Marshal(disk)
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	_ = os.WriteFile(path, data, 0o644)
}

// deleteRunOut drops a node's persisted run band — called when the node itself
// is removed so the cache does not outlive it.
func (m *Model) deleteRunOut(uuid string) {
	_ = os.Remove(m.runOutPath(uuid))
}
