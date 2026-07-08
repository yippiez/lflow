package editor

import (
	"strings"
	"testing"

	"github.com/lflow/lflow/pkg/tui/database"
)

func paintModel(t *testing.T) (*Model, *item) {
	t.Helper()
	db := database.InitTestMemoryDB(t)
	root := &item{uuid: "root"}
	n := &item{uuid: "p1", name: "paint some words here", parent: root}
	root.children = []*item{n}
	tr := &tree{db: db, root: root, byUUID: map[string]*item{"root": root, "p1": n},
		externalNames: map[string]string{}, snapshots: map[string]snapshot{}}
	m := &Model{db: db, tree: tr, viewStack: []*item{root}, width: 100, height: 30}
	m.refreshRows()
	t.Cleanup(func() { nodeSpans = map[string][]database.NodeSpan{}; paintUUID = "" })
	return m, n
}

// TestPainterFlow drives p-from-/style → select a run → pick a color → the run
// persists, renders, and unpaints.
func TestPainterFlow(t *testing.T) {
	m, n := paintModel(t)

	// enter via the /style picker's p key
	m.mode = modeStyle
	m.caret = len("paint ") // anchor at the start of "some"
	if !(styleSource{}).onKey(m, &m.list, "p", nil) {
		t.Fatal("p inside /style must enter the painter")
	}
	if m.mode != modePaint {
		t.Fatalf("mode = %v, want modePaint", m.mode)
	}

	// grow the selection over "some" (4 runes)
	for i := 0; i < 4; i++ {
		m.handlePaintKey(tkey("shift+right"))
	}
	if lo, hi := paintBounds(); lo != 6 || hi != 10 {
		t.Fatalf("selection = [%d,%d), want [6,10)", lo, hi)
	}

	// enter opens the style list; picking red paints the run
	m.handlePaintKey(tkey("enter"))
	if m.mode != modePaintStyle {
		t.Fatalf("mode = %v, want modePaintStyle", m.mode)
	}
	paintStyleSource{}.onSelect(m, pickerItem{value: "red"})
	spans := nodeSpans["p1"]
	if len(spans) != 1 || spans[0].Start != 6 || spans[0].End != 10 || !strings.Contains(spans[0].Style, "color:red") {
		t.Fatalf("spans = %+v", spans)
	}
	// persisted
	saved, err := database.AllNodeSpans(m.db)
	if err != nil || len(saved["p1"]) != 1 {
		t.Fatalf("persisted spans = %v (%v)", saved, err)
	}
	// renders: the red SGR appears mid-body
	body := renderBody(n, n.name, -1, false, nil)
	if !strings.Contains(body, "\x1b[38;2;244;71;71m") {
		t.Fatalf("painted run must render red, got %q", body)
	}

	// unpaint: re-enter painter over the same run, press u
	m.caret = 6
	m.enterPaint()
	for i := 0; i < 4; i++ {
		m.handlePaintKey(tkey("shift+right"))
	}
	m.handlePaintKey(tkey("u"))
	if len(nodeSpans["p1"]) != 0 {
		t.Fatalf("unpaint left spans: %+v", nodeSpans["p1"])
	}
}

// TestPainterComposeAndSplit: a second style composes on the same run, and
// painting the middle of a run splits it.
func TestPainterComposeAndSplit(t *testing.T) {
	m, n := paintModel(t)

	// paint [0,10) yellow
	m.setSpanStyle(n, 0, 10, "color:yellow")
	// bold the same run: composes to bold+yellow
	m.caret = 0
	m.enterPaint()
	paintCaret = 10
	m.handlePaintKey(tkey("enter"))
	paintStyleSource{}.onSelect(m, pickerItem{value: "bold"})
	sp := nodeSpans["p1"]
	if len(sp) != 1 || !strings.Contains(sp[0].Style, "bold") || !strings.Contains(sp[0].Style, "color:yellow") {
		t.Fatalf("compose failed: %+v", sp)
	}

	// painting [3,6) red splits the run into three
	m.setSpanStyle(n, 3, 6, "color:red")
	sp = nodeSpans["p1"]
	if len(sp) != 3 || sp[0].End != 3 || sp[1].Style != "color:red" || sp[2].Start != 6 {
		t.Fatalf("split failed: %+v", sp)
	}
}

// TestSpansShiftWithEdits: typing before a run moves it; deleting through it
// shrinks it; the DB row follows.
func TestSpansShiftWithEdits(t *testing.T) {
	m, n := paintModel(t)
	m.setSpanStyle(n, 6, 10, "color:red") // "some"

	// insert 2 runes at the start → run slides right
	m.cursor = 0
	m.caret = 0
	m.press("X")
	m.press("Y")
	sp := nodeSpans["p1"]
	if len(sp) != 1 || sp[0].Start != 8 || sp[0].End != 12 {
		t.Fatalf("after insert: %+v", sp)
	}
	// backspace one of them → run slides back
	m.caret = 2
	m.press("backspace")
	sp = nodeSpans["p1"]
	if sp[0].Start != 7 || sp[0].End != 11 {
		t.Fatalf("after backspace: %+v", sp)
	}
	// deleting inside the run shrinks it
	m.caret = 9
	m.press("backspace")
	sp = nodeSpans["p1"]
	if sp[0].Start != 7 || sp[0].End != 10 {
		t.Fatalf("after inner delete: %+v", sp)
	}
	// the DB mirrors the shifted run
	saved, _ := database.AllNodeSpans(m.db)
	if len(saved["p1"]) != 1 || saved["p1"][0].Start != 7 || saved["p1"][0].End != 10 {
		t.Fatalf("persisted = %+v", saved["p1"])
	}
}
