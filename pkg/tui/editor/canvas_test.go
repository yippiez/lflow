package editor

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lflow/lflow/pkg/tui/database"
)

// keyMsg builds a KeyMsg for the canvas view tests from a key name.
func keyMsg(name string) tea.KeyMsg {
	switch name {
	case "right":
		return tea.KeyMsg{Type: tea.KeyRight}
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(name)}
}

func newCanvasTestModel(t *testing.T) (*Model, *item) {
	t.Helper()
	m, _, n1 := newAgentTestModel(t)
	n1.typ = database.TypeCanvas
	n1.name = ""
	return m, n1
}

// TestCanvasDocRoundTrip: the document persists as a blob and loads back
// intact — cells, objects, spans.
func TestCanvasDocRoundTrip(t *testing.T) {
	m, it := newCanvasTestModel(t)
	doc := &canvasDoc{W: 40, H: 10,
		Cells: []canvasCell{{X: 3, Y: 2, Ch: "★", Fg: "yellow"}},
		Objs:  []canvasObj{{Name: "A", X: 1, Y: 1, W: 5, H: 3}},
		Spans: []canvasSpan{{From: canvasAnchor{Obj: "A", DX: 5, DY: 1, X: 6, Y: 2}, To: canvasAnchor{X: 12, Y: 2}, Ch: "─"}},
	}
	m.canvasSave(it, doc)

	got := m.canvasLoad(it)
	if got.W != 40 || got.H != 10 || len(got.Cells) != 1 || len(got.Objs) != 1 || len(got.Spans) != 1 {
		t.Fatalf("round trip lost data: %+v", got)
	}
	if got.Cells[0].Ch != "★" || got.Objs[0].Name != "A" {
		t.Fatalf("content mismatch: %+v", got)
	}
}

// TestCanvasSpanFollowsObject: the CAD constraint — a span anchored to an
// object re-derives when the object moves.
func TestCanvasSpanFollowsObject(t *testing.T) {
	doc := &canvasDoc{W: 40, H: 10,
		Objs:  []canvasObj{{Name: "A", X: 2, Y: 2, W: 4, H: 3}},
		Spans: []canvasSpan{{From: canvasAnchor{Obj: "A", DX: 4, DY: 1}, To: canvasAnchor{X: 15, Y: 3}, Ch: "═"}},
	}
	x, y := doc.resolve(doc.Spans[0].From)
	if x != 6 || y != 3 {
		t.Fatalf("anchor = (%d,%d), want (6,3)", x, y)
	}

	doc.Objs[0].X, doc.Objs[0].Y = 5, 4 // the constraint re-derives
	x, y = doc.resolve(doc.Spans[0].From)
	if x != 9 || y != 5 {
		t.Fatalf("anchor after move = (%d,%d), want (9,5)", x, y)
	}
	grid := doc.composite()
	if c, ok := grid[[2]int{12, 5}]; !ok || c.ch != "═" {
		t.Fatalf("span must repaint from the moved anchor, got %+v", c)
	}
}

// TestCanvasAnchorAt: points on or around an object anchor to it (relative to
// its origin); points in the open anchor absolutely.
func TestCanvasAnchorAt(t *testing.T) {
	doc := &canvasDoc{W: 40, H: 10, Objs: []canvasObj{{Name: "B", X: 10, Y: 2, W: 6, H: 4}}}
	a := doc.anchorAt(16, 4) // just right of the box
	if a.Obj != "B" || a.DX != 6 || a.DY != 2 {
		t.Fatalf("edge anchor = %+v, want B+(6,2)", a)
	}
	a = doc.anchorAt(30, 8)
	if a.Obj != "" || a.X != 30 || a.Y != 8 {
		t.Fatalf("open anchor = %+v, want absolute (30,8)", a)
	}
}

// TestCanvasEraseAt: a painted cell goes first; with none, the span whose
// path covers the point goes.
func TestCanvasEraseAt(t *testing.T) {
	doc := &canvasDoc{W: 20, H: 5,
		Cells: []canvasCell{{X: 4, Y: 1, Ch: "●"}},
		Spans: []canvasSpan{{From: canvasAnchor{X: 2, Y: 3}, To: canvasAnchor{X: 8, Y: 3}, Ch: "─"}},
	}
	if !doc.eraseAt(4, 1) || len(doc.Cells) != 0 {
		t.Fatal("cell erase failed")
	}
	if !doc.eraseAt(5, 3) || len(doc.Spans) != 0 {
		t.Fatal("span erase failed")
	}
	if doc.eraseAt(0, 0) {
		t.Fatal("empty point must erase nothing")
	}
}

// TestCanvasText: objects render as boxes with their name on the border —
// the same text an agent receives via <canvas>.
func TestCanvasText(t *testing.T) {
	doc := &canvasDoc{W: 12, H: 5, Objs: []canvasObj{{Name: "A", X: 0, Y: 0, W: 4, H: 3}}}
	text := doc.canvasText()
	lines := strings.Split(text, "\n")
	if len(lines) != 3 {
		t.Fatalf("want 3 lines, got %d: %q", len(lines), text)
	}
	if lines[0] != "┌A─┐" || lines[1] != "│  │" || lines[2] != "└──┘" {
		t.Fatalf("box render mismatch:\n%s", text)
	}
}

// TestCanvasPaletteSearch: the user's own examples must hit.
func TestCanvasPaletteSearch(t *testing.T) {
	cases := map[string]string{ // query → a glyph that must be in the results
		"branch":          "├",
		"curved":          "╭",
		"geometry square": "□",
		"half block":      "▌",
		"shade":           "░",
		"arrow right":     "→",
	}
	for q, want := range cases {
		hits := canvasPaletteSearch(q)
		found := false
		for _, h := range hits {
			if h.ch == want {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("query %q must surface %q; got %d hits", q, want, len(hits))
		}
	}
	// background blue is a color entry
	hits := canvasPaletteSearch("background blue")
	if len(hits) == 0 || hits[0].cat != "background" || hits[0].color != "blue" {
		t.Fatalf("background blue mismatch: %+v", hits)
	}
}

// TestCanvasViewPaintAndSave: alt+e paints through the view and esc persists.
func TestCanvasViewPaintAndSave(t *testing.T) {
	m, it := newCanvasTestModel(t)
	v := canvasView{}
	if !v.Enter(m, it) {
		t.Fatal("Enter must focus")
	}
	st := canvasStateOf(m, it)
	st.brush = canvasBrush{Ch: "◆", Fg: "cyan"}
	if _, ok := v.Key(m, it, keyMsg("right")); !ok {
		t.Fatal("movement must be handled")
	}
	if _, ok := v.Key(m, it, keyMsg("enter")); !ok {
		t.Fatal("stamp must be handled")
	}
	v.Leave(m, it)

	got := m.canvasLoad(it)
	if len(got.Cells) != 1 || got.Cells[0].X != 1 || got.Cells[0].Ch != "◆" {
		t.Fatalf("stamp did not persist: %+v", got.Cells)
	}
}
