package editor

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	tuictx "github.com/lflow/lflow/pkg/tui/context"
	"github.com/lflow/lflow/pkg/tui/database"
)

// TestRunOutPersistsAcrossReload pins the fix: a bash node's run band is written
// to node_output and rehydrated on the next render, so output survives a quit.
func TestRunOutPersistsAcrossReload(t *testing.T) {
	db := database.InitTestMemoryDB(t)
	m := &Model{ctx: tuictx.DnoteCtx{DB: db}}

	r := m.ensureRun("b1")
	r.out = []outLine{
		{text: "hello"},
		{text: "boom", err: true},
	}
	r.pwd = "/tmp/project"
	m.persistRunOut("b1")

	// simulate a restart: fresh maps, nothing in memory, same DB
	reopened := &Model{ctx: tuictx.DnoteCtx{DB: db}}
	reopened.ensureRunOutLoaded("b1")

	got := reopened.run("b1").out
	if len(got) != 2 {
		t.Fatalf("want 2 reloaded lines, got %d: %+v", len(got), got)
	}
	if got[0].text != "hello" || got[0].err {
		t.Errorf("line 0 wrong: %+v", got[0])
	}
	if got[1].text != "boom" || !got[1].err {
		t.Errorf("line 1 (stderr) wrong: %+v", got[1])
	}
	if pwd := reopened.run("b1").pwd; pwd != "/tmp/project" {
		t.Fatalf("pwd = %q, want /tmp/project", pwd)
	}
	bands := (runOutView{}).Bands(reopened, &item{uuid: "b1", typ: "bash"}, "", 80, 0, 10, true)
	if len(bands) < 2 || !strings.Contains(bands[1], "pwd: /tmp/project") {
		t.Fatalf("alt+e band should show pwd, got %q", bands)
	}
}

// TestRunOutEmptyClearsCache: a re-run that produced nothing removes stale output.
func TestRunOutEmptyClearsCache(t *testing.T) {
	db := database.InitTestMemoryDB(t)
	m := &Model{ctx: tuictx.DnoteCtx{DB: db}}

	m.ensureRun("b1").out = []outLine{{text: "old"}}
	m.persistRunOut("b1")

	// re-run yields no output
	m.ensureRun("b1").out = nil
	m.persistRunOut("b1")

	reopened := &Model{ctx: tuictx.DnoteCtx{DB: db}}
	reopened.ensureRunOutLoaded("b1")
	if r := reopened.run("b1"); r != nil && len(r.out) != 0 {
		t.Errorf("stale output should be cleared, got %+v", r.out)
	}
}

// TestAppendRunOutCapsBand: a band holds at most maxRunLines lines — the head
// is dropped and counted, the tail kept (the guard a huge rg needed).
func TestAppendRunOutCapsBand(t *testing.T) {
	m := &Model{}
	for i := 0; i < maxRunLines+100; i++ {
		m.appendRunOut("u1", outLine{text: "x"})
	}
	if got := len(m.run("u1").out); got != maxRunLines {
		t.Fatalf("band length = %d, want %d", got, maxRunLines)
	}
	if got := m.run("u1").dropped; got != 100 {
		t.Fatalf("dropped = %d, want 100", got)
	}
}

// TestAppendRunOutClipsLongLine: a single huge line is clipped to
// maxRunLineLen bytes at a rune boundary.
func TestAppendRunOutClipsLongLine(t *testing.T) {
	m := &Model{}
	m.appendRunOut("u1", outLine{text: strings.Repeat("é", maxRunLineLen)}) // 2 bytes/rune
	got := m.run("u1").out[0].text
	if len(got) > maxRunLineLen+len("…") {
		t.Fatalf("line kept %d bytes, want <= %d", len(got), maxRunLineLen+len("…"))
	}
	if !strings.HasSuffix(got, "…") || !strings.HasSuffix(strings.TrimSuffix(got, "…"), "é") {
		t.Fatalf("clip broke the rune boundary near %q", got[len(got)-8:])
	}
}

// TestPersistRunOutByteBudget: only the newest lines that fit the byte budget
// reach the DB — one giant run cannot bloat a node_output row.
func TestPersistRunOutByteBudget(t *testing.T) {
	db := database.InitTestMemoryDB(t)
	m := &Model{ctx: tuictx.DnoteCtx{DB: db}}
	line := strings.Repeat("x", maxRunLineLen)
	for i := 0; i < 1000; i++ { // ~4MB raw, budget is 512KB
		m.appendRunOut("b1", outLine{text: line})
	}
	m.persistRunOut("b1")

	reopened := &Model{ctx: tuictx.DnoteCtx{DB: db}}
	reopened.ensureRunOutLoaded("b1")
	got := len(reopened.run("b1").out)
	if got == 0 || got >= 1000 {
		t.Fatalf("persisted %d lines, want a budgeted tail (0 < n < 1000)", got)
	}
	if got > maxRunPersistBytes/maxRunLineLen+1 {
		t.Fatalf("persisted %d lines, over the %dB budget", got, maxRunPersistBytes)
	}
}

// TestAltXStopsThenClears: alt+x on a running node stops it (output kept);
// alt+x again clears the band.
func TestAltXStopsThenClears(t *testing.T) {
	m := newTestModel(80, "yes")
	it := m.tree.root.children[0]
	it.uuid = "u1"
	it.typ = "bash"
	m.tree.byUUID["u1"] = it

	stopped := false
	r := m.ensureRun("u1")
	r.out = []outLine{{text: "line"}}
	r.cancel = func() { stopped = true }
	r.ch = make(chan tea.Msg)
	r.dropped = 42
	r.pwd = "/tmp/project"

	altX := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x"), Alt: true}
	m.handleKey(altX)
	if !stopped {
		t.Fatal("alt+x did not cancel the running command")
	}
	if rs := m.run("u1"); rs != nil && rs.cancel != nil {
		t.Fatal("alt+x left the run marked running")
	}
	if len(m.run("u1").out) != 1 {
		t.Fatal("alt+x stop discarded the captured output")
	}

	m.handleKey(altX)
	if len(m.run("u1").out) != 0 {
		t.Fatal("second alt+x did not clear the band")
	}
	if m.run("u1").dropped != 0 {
		t.Fatal("second alt+x did not reset the dropped counter")
	}
	if m.run("u1").pwd != "" {
		t.Fatal("second alt+x did not clear the captured pwd")
	}
}

// TestDeleteRunOutRemovesCache: deleting the node drops its persisted band.
func TestDeleteRunOutRemovesCache(t *testing.T) {
	db := database.InitTestMemoryDB(t)
	m := &Model{ctx: tuictx.DnoteCtx{DB: db}}
	m.ensureRun("b1").out = []outLine{{text: "x"}}
	m.persistRunOut("b1")

	m.deleteRunOut("b1")

	reopened := &Model{ctx: tuictx.DnoteCtx{DB: db}}
	reopened.ensureRunOutLoaded("b1")
	if r := reopened.run("b1"); r != nil && len(r.out) != 0 {
		t.Errorf("cache should be gone after delete, got %+v", r.out)
	}
}
