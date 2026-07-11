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

// TestPainterFlow drives p-from-/style (capturing the highlighted style) →
// place/resize the window → enter paints it → the run persists, renders, and
// unpaints.
func TestPainterFlow(t *testing.T) {
	m, n := paintModel(t)

	// enter via the /style picker's p key: the highlighted row rides along
	m.mode = modeStyle
	m.caret = len("paint ") // the window opens at the caret, on "some"
	items := (styleSource{}).items(m, "")
	for i, it := range items {
		if it.value == "red" {
			m.list.sel = i
		}
	}
	if !(styleSource{}).onKey(m, &m.list, "p", items) {
		t.Fatal("p inside /style must enter the painter")
	}
	if m.mode != modePaint {
		t.Fatalf("mode = %v, want modePaint", m.mode)
	}
	if paintValue != "red" {
		t.Fatalf("paintValue = %q, want the highlighted red row", paintValue)
	}
	if lo, hi := paintBounds(); lo != 6 || hi != 7 {
		t.Fatalf("window = [%d,%d), want the one-rune [6,7)", lo, hi)
	}

	// shift+right grows the window over "some" (4 runes)
	for i := 0; i < 3; i++ {
		m.handlePaintKey(tkey("shift+right"))
	}
	if lo, hi := paintBounds(); lo != 6 || hi != 10 {
		t.Fatalf("window = [%d,%d), want [6,10)", lo, hi)
	}
	// plain arrows slide the window without resizing; shift+left shrinks it
	m.handlePaintKey(tkey("right"))
	m.handlePaintKey(tkey("left"))
	if lo, hi := paintBounds(); lo != 6 || hi != 10 {
		t.Fatalf("window after slide = [%d,%d), want [6,10)", lo, hi)
	}
	m.handlePaintKey(tkey("shift+left"))
	m.handlePaintKey(tkey("shift+right"))
	if lo, hi := paintBounds(); lo != 6 || hi != 10 {
		t.Fatalf("window after resize = [%d,%d), want [6,10)", lo, hi)
	}

	// enter paints the window with the captured style — no second list
	m.handlePaintKey(tkey("enter"))
	if m.mode != modeOutline {
		t.Fatalf("mode = %v, want modeOutline after painting", m.mode)
	}
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
	body := renderBody(n, n.name, -1, false, nil, false)
	if !strings.Contains(body, "\x1b[38;2;244;71;71m") {
		t.Fatalf("painted run must render red, got %q", body)
	}

	// unpaint: re-enter the painter over the same run, press u
	m.caret = 6
	m.enterPaint("red")
	for i := 0; i < 3; i++ {
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
	m.enterPaint("bold")
	paintWidth = 10
	m.handlePaintKey(tkey("enter"))
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
