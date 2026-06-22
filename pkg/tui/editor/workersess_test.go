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

// TestWorkerTranscriptPersistsAcrossReload: the full you↔agent conversation is
// snapshotted and restored on reopen, so the scrollback survives a quit — and the
// capture is CLI-agnostic (appendXcript is the same path for pi/opencode/grok).
func TestWorkerTranscriptPersistsAcrossReload(t *testing.T) {
	dir := t.TempDir()
	m := &Model{ctx: tuictx.DnoteCtx{Paths: tuictx.Paths{Data: dir}}}
	m.ensureWorkerMaps()

	m.appendXcript("w1", "you", "count the go files")
	m.appendXcript("w1", "agent", deliverableToText(`[{"text":"there are 12"}]`))
	m.appendXcript("w1", "you", "and the test files?")
	m.appendXcript("w1", "agent", deliverableToText(`[{"text":"there are 5"}]`))

	re := &Model{ctx: tuictx.DnoteCtx{Paths: tuictx.Paths{Data: dir}}}
	re.ensureWorkerSessLoaded("w1")

	got := re.workerTranscript["w1"]
	if len(got) != 4 {
		t.Fatalf("want 4 transcript lines, got %d: %+v", len(got), got)
	}
	if got[0].role != "you" || got[0].text != "count the go files" {
		t.Errorf("line 0 wrong: %+v", got[0])
	}
	if got[1].role != "agent" || got[1].text != "there are 12" {
		t.Errorf("line 1 (answer) wrong: %+v", got[1])
	}
	if got[3].role != "agent" || got[3].text != "there are 5" {
		t.Errorf("line 3 wrong: %+v", got[3])
	}
}

// TestDeliverableToTextFlattens: nested deliverable nodes flatten to indented text.
func TestDeliverableToTextFlattens(t *testing.T) {
	got := deliverableToText(`[{"text":"parent","children":[{"text":"child"}]}]`)
	want := "parent\n  child"
	if got != want {
		t.Errorf("deliverableToText = %q, want %q", got, want)
	}
	if deliverableToText("") != "" || deliverableToText("not json") != "" {
		t.Errorf("empty/garbage deliverable should flatten to empty")
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
