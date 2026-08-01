package editor

import (
	"strings"
	"testing"

	"github.com/lflow/lflow/packages/database"
)

func spanModel(t *testing.T) (*Model, *item) {
	t.Helper()
	db := database.InitTestMemoryDB(t)
	root := &item{uuid: "root"}
	n := &item{uuid: "p1", name: "paint some words here", parent: root}
	root.children = []*item{n}
	tr := &tree{db: db, root: root, byUUID: map[string]*item{"root": root, "p1": n},
		externalNames: map[string]string{}, snapshots: map[string]snapshot{}}
	m := &Model{db: db, tree: tr, viewStack: []*item{root}, width: 100, height: 30}
	m.refreshRows()
	t.Cleanup(func() { nodeSpans = map[string][]database.NodeSpan{}; textSelUUID = "" })
	return m, n
}

// styleSel drives the /style picker as a keypress would: pick the row carrying
// the given value and apply it.
func styleSel(t *testing.T, m *Model, value string) {
	t.Helper()
	m.mode = modeStyle
	for _, it := range (styleSource{}).items(m, "") {
		if it.value == value {
			(styleSource{}).onSelect(m, it)
			return
		}
	}
	t.Fatalf("no %q row in the /style picker", value)
}

// TestSelectionStylesRunOnly is the whole feature: select a word with shift+→,
// pick a style, and only that run is styled — the node's own style stays
// untouched. Re-picking the same color clears the run again.
func TestSelectionStylesRunOnly(t *testing.T) {
	m, n := spanModel(t)

	m.caret = len("paint ") // on "some"
	m.press("shift+right")
	if textSelLo != 6 || textSelHi != 10 {
		t.Fatalf("selection = [%d,%d), want [6,10) over \"some\"", textSelLo, textSelHi)
	}

	styleSel(t, m, "red")
	if m.mode != modeOutline {
		t.Fatalf("mode = %v, want modeOutline after styling", m.mode)
	}
	if m.textSelOn {
		t.Fatal("styling the run must release the selection")
	}
	if n.style != "" {
		t.Fatalf("node style = %q, want the line left alone", n.style)
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
		t.Fatalf("styled run must render red, got %q", body)
	}

	// re-picking red over the same run clears it — the unstyle
	m.caret = 6
	m.press("shift+right")
	styleSel(t, m, "red")
	if len(nodeSpans["p1"]) != 0 {
		t.Fatalf("re-picking the same color must clear the run: %+v", nodeSpans["p1"])
	}
}

// TestStyleWithoutSelectionStillStylesNode: with no horizontal selection the
// picker behaves exactly as before — the whole node takes the style.
func TestStyleWithoutSelectionStillStylesNode(t *testing.T) {
	m, n := spanModel(t)
	styleSel(t, m, "bold")
	if !strings.Contains(n.style, "bold") {
		t.Fatalf("node style = %q, want bold", n.style)
	}
	if len(nodeSpans["p1"]) != 0 {
		t.Fatalf("no selection must leave the text runs alone: %+v", nodeSpans["p1"])
	}
}

// TestSpanComposeAndSplit: a second style composes on the same run, and styling
// the middle of a run splits it.
func TestSpanComposeAndSplit(t *testing.T) {
	m, n := spanModel(t)

	// style [0,10) yellow, then bold the same run: composes to bold+yellow
	m.setSpanStyle(n, 0, 10, "color:yellow")
	m.applyStyleToSpan(n, 0, 10, "bold")
	sp := nodeSpans["p1"]
	if len(sp) != 1 || !strings.Contains(sp[0].Style, "bold") || !strings.Contains(sp[0].Style, "color:yellow") {
		t.Fatalf("compose failed: %+v", sp)
	}

	// styling [3,6) red splits the run into three
	m.setSpanStyle(n, 3, 6, "color:red")
	sp = nodeSpans["p1"]
	if len(sp) != 3 || sp[0].End != 3 || sp[1].Style != "color:red" || sp[2].Start != 6 {
		t.Fatalf("split failed: %+v", sp)
	}
}

// TestSpansShiftWithEdits: typing before a run moves it; deleting through it
// shrinks it; the DB row follows.
func TestSpansShiftWithEdits(t *testing.T) {
	m, n := spanModel(t)
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
