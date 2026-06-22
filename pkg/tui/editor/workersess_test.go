package editor

import (
	"testing"
	"time"

	tuictx "github.com/lflow/lflow/pkg/tui/context"
)

// TestWorkerSessPersistsAcrossReload pins the resume feature's display half: a
// worker's run state (status, usage, tool-calls, deliverable) is snapshotted to a
// local file and rehydrated on reopen, so a reopened worker shows its prior result
// and stays harvestable instead of looking never-run.
func TestWorkerSessPersistsAcrossReload(t *testing.T) {
	dir := t.TempDir()
	m := &Model{ctx: tuictx.DnoteCtx{Paths: tuictx.Paths{Data: dir}}}
	m.ensureWorkerMaps()

	m.workerStatus["w1"] = "done"
	m.workerModel["w1"] = "pi/claude-sonnet"
	m.workerUsage["w1"] = workerUsage{model: "pi/claude-sonnet", in: 1200, out: 3400, cost: 0.04}
	m.workerActions["w1"] = []workerActivity{{tool: "read", text: "foo.go"}, {tool: "bash", text: "go test"}}
	m.workerDeliverable["w1"] = `[{"name":"the answer"}]`
	m.workerStart["w1"] = time.Unix(1000, 0)
	m.workerLastActive["w1"] = time.Unix(1042, 0)
	m.persistWorkerSess("w1")

	// simulate a restart: fresh model, nothing in memory
	re := &Model{ctx: tuictx.DnoteCtx{Paths: tuictx.Paths{Data: dir}}}
	re.ensureWorkerSessLoaded("w1")

	if re.workerStatus["w1"] != "done" {
		t.Errorf("status = %q, want done", re.workerStatus["w1"])
	}
	if re.workerModel["w1"] != "pi/claude-sonnet" {
		t.Errorf("model = %q", re.workerModel["w1"])
	}
	if u := re.workerUsage["w1"]; u.in != 1200 || u.out != 3400 || u.cost != 0.04 {
		t.Errorf("usage wrong: %+v", u)
	}
	if acts := re.workerActions["w1"]; len(acts) != 2 || acts[0].tool != "read" || acts[1].text != "go test" {
		t.Errorf("actions wrong: %+v", acts)
	}
	if re.workerDeliverable["w1"] != `[{"name":"the answer"}]` {
		t.Errorf("deliverable wrong: %q", re.workerDeliverable["w1"])
	}
	if !re.workerStart["w1"].Equal(time.Unix(1000, 0)) {
		t.Errorf("start wrong: %v", re.workerStart["w1"])
	}
	// a reopened worker with a snapshot is resumable
	if !re.workerHasSession("w1") {
		t.Errorf("worker with a snapshot should be resumable")
	}
}

// TestDeleteWorkerSessRemovesSnapshot: removing the node drops its snapshot, so a
// fresh worker reusing the uuid is not treated as resumable.
func TestDeleteWorkerSessRemovesSnapshot(t *testing.T) {
	dir := t.TempDir()
	m := &Model{ctx: tuictx.DnoteCtx{Paths: tuictx.Paths{Data: dir}}}
	m.ensureWorkerMaps()
	m.workerStatus["w1"] = "done"
	m.persistWorkerSess("w1")

	m.deleteWorkerSess("w1")

	re := &Model{ctx: tuictx.DnoteCtx{Paths: tuictx.Paths{Data: dir}}}
	if re.workerHasSession("w1") {
		t.Errorf("snapshot should be gone after delete")
	}
}

// TestWorkerNoSessionNotResumable: a never-run worker has no snapshot and is not
// resumable, so its first run sends full context (not a bare resume turn).
func TestWorkerNoSessionNotResumable(t *testing.T) {
	dir := t.TempDir()
	m := &Model{ctx: tuictx.DnoteCtx{Paths: tuictx.Paths{Data: dir}}}
	if m.workerHasSession("never") {
		t.Errorf("a never-run worker must not be resumable")
	}
}
