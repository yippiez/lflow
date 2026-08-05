package editor

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/lflow/lflow/packages/database"
	tuictx "github.com/lflow/lflow/packages/tui"
)

// TestRunOutPersistsAcrossReload pins the fix: a bash node's run band is written
// to node_output and rehydrated on the next render, so output survives a quit.
func TestRunOutPersistsAcrossReload(t *testing.T) {
	db := database.InitTestMemoryDB(t)
	m := &Model{ctx: tuictx.Ctx{DB: db}}

	r := m.ensureRun("b1")
	r.out = []outLine{
		{text: "hello"},
		{text: "boom", err: true},
	}
	r.pwd = "/tmp/project"
	m.persistRunOut("b1")

	// simulate a restart: fresh maps, nothing in memory, same DB
	reopened := &Model{ctx: tuictx.Ctx{DB: db}}
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
	// the header carries where it ran, on the same line as the rest of the chrome
	bands := (runOutView{}).bands(reopened, &item{uuid: "b1", typ: "bash"}, "", 80, 0, 10, true)
	if len(bands) < 2 || !strings.Contains(bands[0], "/tmp/project") {
		t.Fatalf("alt+e header should show pwd, got %q", bands)
	}
}

// TestRunOutEmptyClearsCache: a re-run that produced nothing removes stale output.
func TestRunOutEmptyClearsCache(t *testing.T) {
	db := database.InitTestMemoryDB(t)
	m := &Model{ctx: tuictx.Ctx{DB: db}}

	m.ensureRun("b1").out = []outLine{{text: "old"}}
	m.persistRunOut("b1")

	// re-run yields no output
	m.ensureRun("b1").out = nil
	m.persistRunOut("b1")

	reopened := &Model{ctx: tuictx.Ctx{DB: db}}
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
	m := &Model{ctx: tuictx.Ctx{DB: db}}
	line := strings.Repeat("x", maxRunLineLen)
	for i := 0; i < 1000; i++ { // ~4MB raw, budget is 512KB
		m.appendRunOut("b1", outLine{text: line})
	}
	m.persistRunOut("b1")

	reopened := &Model{ctx: tuictx.Ctx{DB: db}}
	reopened.ensureRunOutLoaded("b1")
	got := len(reopened.run("b1").out)
	if got == 0 || got >= 1000 {
		t.Fatalf("persisted %d lines, want a budgeted tail (0 < n < 1000)", got)
	}
	if got > maxRunPersistBytes/maxRunLineLen+1 {
		t.Fatalf("persisted %d lines, over the %dB budget", got, maxRunPersistBytes)
	}
}

// TestAltKStopsThenClears: alt+k on a running node stops it (output kept);
// alt+k again clears the band. (alt+x is the cut key, so kill moved off it.)
func TestAltKStopsThenClears(t *testing.T) {
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

	altK := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("k"), Alt: true}
	m.handleKey(altK)
	if !stopped {
		t.Fatal("alt+k did not cancel the running command")
	}
	if rs := m.run("u1"); rs != nil && rs.cancel != nil {
		t.Fatal("alt+k left the run marked running")
	}
	if len(m.run("u1").out) != 1 {
		t.Fatal("alt+k stop discarded the captured output")
	}

	m.handleKey(altK)
	if len(m.run("u1").out) != 0 {
		t.Fatal("second alt+k did not clear the band")
	}
	if m.run("u1").dropped != 0 {
		t.Fatal("second alt+k did not reset the dropped counter")
	}
	if m.run("u1").pwd != "" {
		t.Fatal("second alt+k did not clear the captured pwd")
	}
}

// TestDeleteRunOutRemovesCache: deleting the node drops its persisted band.
func TestDeleteRunOutRemovesCache(t *testing.T) {
	db := database.InitTestMemoryDB(t)
	m := &Model{ctx: tuictx.Ctx{DB: db}}
	m.ensureRun("b1").out = []outLine{{text: "x"}}
	m.persistRunOut("b1")

	m.deleteRunOut("b1")

	reopened := &Model{ctx: tuictx.Ctx{DB: db}}
	reopened.ensureRunOutLoaded("b1")
	if r := reopened.run("b1"); r != nil && len(r.out) != 0 {
		t.Errorf("cache should be gone after delete, got %+v", r.out)
	}
}

// TestTypeChangeDropsTheRunBand is the stuck-output regression: run a bash node,
// backspace it back into an ordinary node, and the last command's output stayed
// hanging under a row that no longer runs anything.
func TestTypeChangeDropsTheRunBand(t *testing.T) {
	m, _ := dbModel(t, database.Node{UUID: "b", Name: "", Type: database.TypeBash})
	it := m.tree.byUUID["b"]
	m.cursor = m.rowIndexOf(it)

	r := m.ensureRun("b")
	r.out = []outLine{{text: "hello from the last run"}}
	m.persistRunOut("b")
	if len(m.ensureRun("b").lines()) == 0 {
		t.Fatal("the fixture has no output to lose")
	}

	// backspace on the empty bash row demotes it to a bullet
	m.press("backspace")
	if it.typ != database.TypeBullets {
		t.Fatalf("type = %q, want the demote to bullets", it.typ)
	}
	if got := m.ensureRun("b").lines(); len(got) != 0 {
		t.Errorf("the old output stayed under the node: %v", got)
	}
	if raw, err := database.LoadNodeOutput(m.db, "b"); err == nil && raw != "" {
		t.Errorf("the output row survived on disk: %q", raw)
	}
}

// TestTypePickerDropsTheRunBand: the same holds for /type, which is the other
// way a node stops being the thing that produced its band.
func TestTypePickerDropsTheRunBand(t *testing.T) {
	m, _ := dbModel(t, database.Node{UUID: "b", Name: "echo hi", Type: database.TypeBash})
	it := m.tree.byUUID["b"]
	m.cursor = m.rowIndexOf(it)
	m.ensureRun("b").out = []outLine{{text: "hi"}}
	m.persistRunOut("b")

	m.mode = modeType
	typeSource{}.onSelect(m, pickerItem{value: database.TypeQuote})
	if it.typ != database.TypeQuote {
		t.Fatalf("type = %q", it.typ)
	}
	if got := m.ensureRun("b").lines(); len(got) != 0 {
		t.Errorf("output survived a retype: %v", got)
	}
}

// TestRunPaneSizesToItsContent: ⌥e on a three-line result used to open a page of
// blank rows — the outline gone, nothing in its place. A finished run's pane is
// exactly its own size; a run still streaming keeps a floor so the outline below
// does not shuffle on every line that arrives.
func TestRunPaneSizesToItsContent(t *testing.T) {
	m, _ := dbModel(t, database.Node{UUID: "b", Name: "echo hi", Type: database.TypeBash})
	r := m.ensureRun("b")
	r.out = []outLine{{text: "hi"}}
	m.persistRunOut("b")

	// finished: one header + one line
	if got := runViewLines(m, "b"); got != 2 {
		t.Errorf("finished pane = %d lines, want 2", got)
	}
	// streaming: the floor holds even with the same one line
	r.cancel = func() {}
	if got := runViewLines(m, "b"); got != runPaneFloor {
		t.Errorf("streaming pane = %d lines, want the %d floor", got, runPaneFloor)
	}
	// and a long finished run is its own height, not the floor
	r.cancel = nil
	for i := 0; i < 40; i++ {
		r.out = append(r.out, outLine{text: "more"})
	}
	if got := runViewLines(m, "b"); got != 42 {
		t.Errorf("long pane = %d lines, want 42", got)
	}
}

// TestFocusedPaneNeverTallerThanItsContent: the central loop is what enforces it,
// so a short view leaves the rows below it on screen.
func TestFocusedPaneNeverTallerThanItsContent(t *testing.T) {
	m, _ := dbModel(t,
		database.Node{UUID: "a", Name: "above", Rank: 0},
		database.Node{UUID: "b", Name: "echo hi", Type: database.TypeBash, Rank: 1},
		database.Node{UUID: "c", Name: "below", Rank: 2},
	)
	m.height = 24
	r := m.ensureRun("b")
	r.out = []outLine{{text: "hi"}}
	m.persistRunOut("b")
	m.cursor = m.rowIndexOfUUID("b")
	m.focused = true

	view := stripSGR(m.View())
	if !strings.Contains(view, "below") {
		t.Errorf("the focused pane pushed the outline off screen:\n%s", view)
	}
	if n := strings.Count(view, "\n"); n > m.height {
		t.Errorf("frame is %d lines, taller than the %d-row window", n+1, m.height)
	}
}

// TestRunTailSurvivesAReopen: the output was always saved — the ROW was what
// forgot it, so a reopened bash node showed a bare command until ⌥e went and
// fetched the band. That reads exactly like the result was thrown away.
func TestRunTailSurvivesAReopen(t *testing.T) {
	m, db := dbModel(t, database.Node{UUID: "b", Name: "echo hi", Type: database.TypeBash})
	r := m.ensureRun("b")
	r.out = []outLine{{text: "hi"}}
	m.persistRunOut("b")

	// a fresh model over the same database, as a reopen gives you
	tr, err := loadTree(db, database.RootUUID)
	if err != nil {
		t.Fatal(err)
	}
	m2 := &Model{db: db, ctx: m.ctx, tree: tr, viewStack: []*item{tr.root},
		width: 80, height: 24, chips: map[string]database.Chip{}}
	m2.hydrateRunTails()
	m2.refreshRows()
	m2.syncRunTails()

	it := m2.tree.byUUID["b"]
	tail := stripSGR(bashBodyTail(it, m2.chips))
	if !strings.Contains(tail, "→ hi") {
		t.Errorf("the reopened row lost its result: %q", tail)
	}
}
