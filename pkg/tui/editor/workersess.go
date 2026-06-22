package editor

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/lflow/lflow/pkg/agent"
)

// A worker's run state — status, model, usage, tool-call history, and the
// harvestable deliverable — is ephemeral in memory, but it is also mirrored to a
// local JSON snapshot so a reopened worker shows its prior result (and stays
// harvestable) instead of looking never-run. Like the bash run-output cache
// (runout.go) and the voice wav, this lives under the data dir and is NEVER in
// the DB or sync — run state is not notebook content.
//
// The live conversation itself is resumed separately, by the agent BACKEND's own
// on-disk session (pi --session-id), keyed by the same node uuid — so alt+r on a
// reopened worker continues the real conversation with full memory.

func (m *Model) workerSessPath(uuid string) string {
	return filepath.Join(m.ctx.Paths.Data, "lflow", "worker", uuid+".json")
}

// piSessionDir pins where pi stores resumable worker sessions, co-located with
// lflow's data so resume does not depend on the editor's working directory.
func (m *Model) piSessionDir() string {
	dir := filepath.Join(m.ctx.Paths.Data, "lflow", "pi-sessions")
	_ = os.MkdirAll(dir, 0o755)
	return dir
}

type workerActivityDisk struct {
	Tool string `json:"tool,omitempty"`
	Text string `json:"text,omitempty"`
}

// workerSessDisk is the serialisable snapshot of a worker's run state.
type workerSessDisk struct {
	Provider    string               `json:"provider"`
	Model       string               `json:"model"`
	SessionID   string               `json:"sessionId"` // backend session id (= uuid for pi)
	Status      string               `json:"status"`
	UsageModel  string               `json:"usageModel,omitempty"`
	In          int                  `json:"in,omitempty"`
	Out         int                  `json:"out,omitempty"`
	Cost        float64              `json:"cost,omitempty"`
	Estimated   bool                 `json:"estimated,omitempty"`
	Actions     []workerActivityDisk `json:"actions,omitempty"`
	Deliverable string               `json:"deliverable,omitempty"`
	StartUnix   int64                `json:"startUnix,omitempty"`
	ActiveUnix  int64                `json:"activeUnix,omitempty"`
}

// workerHasSession reports whether a worker has a saved (resumable) session,
// either live this session or persisted from a prior run. A true result means
// alt+r should RESUME the conversation rather than start it fresh.
func (m *Model) workerHasSession(uuid string) bool {
	m.ensureWorkerSessLoaded(uuid)
	if !m.workerStart[uuid].IsZero() {
		return true
	}
	_, err := os.Stat(m.workerSessPath(uuid))
	return err == nil
}

// ensureWorkerSessLoaded lazily hydrates a worker's run state from its snapshot
// the first time it is touched, so a reopened worker renders its prior result.
// A worker that has already run (or is running) this session owns its in-memory
// state and is left untouched.
func (m *Model) ensureWorkerSessLoaded(uuid string) {
	if m.workerSessLoaded == nil {
		m.workerSessLoaded = map[string]bool{}
	}
	if m.workerSessLoaded[uuid] {
		return
	}
	m.workerSessLoaded[uuid] = true // mark first: a missing/garbled file is not retried

	data, err := os.ReadFile(m.workerSessPath(uuid))
	if err != nil {
		return // never run, or no persisted state
	}
	var d workerSessDisk
	if json.Unmarshal(data, &d) != nil {
		return
	}

	m.ensureWorkerMaps()
	m.workerStatus[uuid] = d.Status
	m.workerModel[uuid] = d.Model
	m.workerDeliverable[uuid] = d.Deliverable
	m.workerUsage[uuid] = workerUsage{model: d.UsageModel, in: d.In, out: d.Out, cost: d.Cost, estimated: d.Estimated}
	if len(d.Actions) > 0 {
		acts := make([]workerActivity, len(d.Actions))
		for i, a := range d.Actions {
			acts[i] = workerActivity{tool: a.Tool, text: a.Text}
		}
		m.workerActions[uuid] = acts
	}
	if d.StartUnix > 0 {
		m.workerStart[uuid] = time.Unix(d.StartUnix, 0)
	}
	if d.ActiveUnix > 0 {
		m.workerLastActive[uuid] = time.Unix(d.ActiveUnix, 0)
	}
}

// persistWorkerSess writes a worker's current run state to its snapshot. Called
// as the state evolves (usage/activity/deliverable) and when the run ends, so a
// quit at any point leaves an accurate snapshot. Best-effort: a write error
// never blocks the run.
func (m *Model) persistWorkerSess(uuid string) {
	if m.workerSessLoaded == nil {
		m.workerSessLoaded = map[string]bool{}
	}
	m.workerSessLoaded[uuid] = true // memory is authoritative for this uuid now

	u := m.workerUsage[uuid]
	d := workerSessDisk{
		Provider:    string(agent.ParseModel(m.workerModel[uuid]).CLI),
		Model:       m.workerModel[uuid],
		SessionID:   uuid, // pi keys its session on the node uuid
		Status:      m.workerStatus[uuid],
		UsageModel:  u.model,
		In:          u.in,
		Out:         u.out,
		Cost:        u.cost,
		Estimated:   u.estimated,
		Deliverable: m.workerDeliverable[uuid],
	}
	for _, a := range m.workerActions[uuid] {
		d.Actions = append(d.Actions, workerActivityDisk{Tool: a.tool, Text: a.text})
	}
	if t := m.workerStart[uuid]; !t.IsZero() {
		d.StartUnix = t.Unix()
	}
	if t := m.workerLastActive[uuid]; !t.IsZero() {
		d.ActiveUnix = t.Unix()
	}

	data, err := json.Marshal(d)
	if err != nil {
		return
	}
	path := m.workerSessPath(uuid)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	_ = os.WriteFile(path, data, 0o644)
}

// deleteWorkerSess drops a worker's persisted snapshot — called when the node is
// removed so the cache does not outlive it. The backend's own session file is
// left to its retention; without our snapshot it is simply never resumed.
func (m *Model) deleteWorkerSess(uuid string) {
	_ = os.Remove(m.workerSessPath(uuid))
}

// ensureWorkerMaps initialises the per-worker state maps if absent.
func (m *Model) ensureWorkerMaps() {
	if m.workerStatus == nil {
		m.workerStatus = map[string]string{}
	}
	if m.workerModel == nil {
		m.workerModel = map[string]string{}
	}
	if m.workerUsage == nil {
		m.workerUsage = map[string]workerUsage{}
	}
	if m.workerActions == nil {
		m.workerActions = map[string][]workerActivity{}
	}
	if m.workerDeliverable == nil {
		m.workerDeliverable = map[string]string{}
	}
	if m.workerStart == nil {
		m.workerStart = map[string]time.Time{}
	}
	if m.workerLastActive == nil {
		m.workerLastActive = map[string]time.Time{}
	}
}
