package editor

import (
	"testing"

	tuictx "github.com/lflow/lflow/pkg/tui/context"
	"github.com/lflow/lflow/pkg/tui/database"
)

// TestRunOutPersistsAcrossReload pins the fix: a bash node's run band is written
// to node_output and rehydrated on the next render, so output survives a quit.
func TestRunOutPersistsAcrossReload(t *testing.T) {
	db := database.InitTestMemoryDB(t)
	m := &Model{ctx: tuictx.DnoteCtx{DB: db}}

	m.runOut = map[string][]outLine{"b1": {
		{text: "hello"},
		{text: "boom", err: true},
	}}
	m.persistRunOut("b1")

	// simulate a restart: fresh maps, nothing in memory, same DB
	reopened := &Model{ctx: tuictx.DnoteCtx{DB: db}}
	reopened.ensureRunOutLoaded("b1")

	got := reopened.runOut["b1"]
	if len(got) != 2 {
		t.Fatalf("want 2 reloaded lines, got %d: %+v", len(got), got)
	}
	if got[0].text != "hello" || got[0].err {
		t.Errorf("line 0 wrong: %+v", got[0])
	}
	if got[1].text != "boom" || !got[1].err {
		t.Errorf("line 1 (stderr) wrong: %+v", got[1])
	}
}

// TestRunOutEmptyClearsCache: a re-run that produced nothing removes stale output.
func TestRunOutEmptyClearsCache(t *testing.T) {
	db := database.InitTestMemoryDB(t)
	m := &Model{ctx: tuictx.DnoteCtx{DB: db}}

	m.runOut = map[string][]outLine{"b1": {{text: "old"}}}
	m.persistRunOut("b1")

	// re-run yields no output
	m.runOut["b1"] = nil
	m.persistRunOut("b1")

	reopened := &Model{ctx: tuictx.DnoteCtx{DB: db}}
	reopened.ensureRunOutLoaded("b1")
	if len(reopened.runOut["b1"]) != 0 {
		t.Errorf("stale output should be cleared, got %+v", reopened.runOut["b1"])
	}
}

// TestDeleteRunOutRemovesCache: deleting the node drops its persisted band.
func TestDeleteRunOutRemovesCache(t *testing.T) {
	db := database.InitTestMemoryDB(t)
	m := &Model{ctx: tuictx.DnoteCtx{DB: db}}
	m.runOut = map[string][]outLine{"b1": {{text: "x"}}}
	m.persistRunOut("b1")

	m.deleteRunOut("b1")

	reopened := &Model{ctx: tuictx.DnoteCtx{DB: db}}
	reopened.ensureRunOutLoaded("b1")
	if len(reopened.runOut["b1"]) != 0 {
		t.Errorf("cache should be gone after delete, got %+v", reopened.runOut["b1"])
	}
}
