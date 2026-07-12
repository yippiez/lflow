package nodes

import (
	"context"
	"math"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lflow/lflow/pkg/tui/database"
	"github.com/lflow/lflow/pkg/tui/editor"
	"github.com/lflow/lflow/pkg/tui/tag"
)

// fakeHost implements editor.NodeHost for plugin tests — the whole point of
// the plugin API: a node file tests against fakes, no editor Model needed.
type fakeHost struct {
	db      *database.DB
	stores  map[string]map[string]any
	flash   string
	deps    map[string]bool
	compute func() <-chan tag.Event
}

func newFakeHost(t *testing.T) *fakeHost {
	return &fakeHost{db: database.InitTestMemoryDB(t), stores: map[string]map[string]any{}}
}

func (f *fakeHost) NodeStore(uuid string) map[string]any {
	if f.stores[uuid] == nil {
		f.stores[uuid] = map[string]any{}
	}
	return f.stores[uuid]
}
func (f *fakeHost) NodeDB() *database.DB  { return f.db }
func (f *fakeHost) NodeFlash(msg string)  { f.flash = msg }
func (f *fakeHost) NodeDepOK(b string) bool {
	if f.deps == nil {
		return true
	}
	ok, probed := f.deps[b]
	return !probed || ok
}
func (f *fakeHost) NodeDefaultAgent() string { return "Pi" }
func (f *fakeHost) NodeAgentGate(string) (string, bool) {
	return "", false
}
func (f *fakeHost) NodeComputeTurn(context.Context, string, string, string, string) (<-chan tag.Event, error) {
	return f.compute(), nil
}

// fakeNode implements editor.NodeRef.
type fakeNode struct {
	uuid, typ, text, path string
	parent                *fakeNode
	kids                  []*fakeNode
}

func (n *fakeNode) UUID() string    { return n.uuid }
func (n *fakeNode) Type() string    { return n.typ }
func (n *fakeNode) Text() string    { return n.text }
func (n *fakeNode) SetText(s string) { n.text = s }
func (n *fakeNode) PathChip() string { return n.path }
func (n *fakeNode) Parent() (editor.NodeRef, bool) {
	if n.parent == nil {
		return nil, false
	}
	return n.parent, true
}
func (n *fakeNode) Siblings() []editor.NodeRef {
	if n.parent == nil {
		return nil
	}
	out := make([]editor.NodeRef, 0, len(n.parent.kids))
	for _, k := range n.parent.kids {
		out = append(out, k)
	}
	return out
}
func (n *fakeNode) Children() []editor.NodeRef {
	out := make([]editor.NodeRef, 0, len(n.kids))
	for _, k := range n.kids {
		out = append(out, k)
	}
	return out
}
func (n *fakeNode) Is(o editor.NodeRef) bool {
	fo, ok := o.(*fakeNode)
	return ok && fo == n
}

// ── codereview ──────────────────────────────────────────────────────────────

func TestCRRange(t *testing.T) {
	cases := map[string][]string{
		"":           nil,
		"a1b2..HEAD": {"a1b2", "HEAD"},
		"a1b2 c3d4":  {"a1b2", "c3d4"},
		"main":       {"main"},
	}
	for in, want := range cases {
		got := crRange(in)
		if len(got) != len(want) {
			t.Fatalf("crRange(%q) = %v, want %v", in, got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("crRange(%q) = %v, want %v", in, got, want)
			}
		}
	}
}

// ── codesig ─────────────────────────────────────────────────────────────────

func TestSigIdent(t *testing.T) {
	cases := map[string]string{
		"func EncodeValue(v any) (any, error)":   "EncodeValue",
		"func (m *Model) canvasRender(it *item)": "canvasRender",
		"type Req struct":                        "Req",
		"class Foo(Base):":                       "Foo",
		"def train(inputs, epochs=10):":          "train",
		"const OpHello = …":                      "OpHello",
	}
	for in, want := range cases {
		if got := sigIdent(in); got != want {
			t.Fatalf("sigIdent(%q) = %q, want %q", in, got, want)
		}
	}
}

// ── nlpcompute ──────────────────────────────────────────────────────────────

func TestPeelCodeFence(t *testing.T) {
	code, lang := peelCodeFence("```python\nb = sum(xs)\n```")
	if lang != "python" || code != "b = sum(xs)" {
		t.Fatalf("fence parse: %q %q", lang, code)
	}
	code, lang = peelCodeFence("plain = 1")
	if lang != "" || code != "plain = 1" {
		t.Fatalf("unfenced passthrough: %q %q", code, lang)
	}
}

// TestNLPComputeFlow: run → the fake turn's reply lands as the cell's code,
// the cwd pins, the state parks idle.
func TestNLPComputeFlow(t *testing.T) {
	h := newFakeHost(t)
	h.compute = func() <-chan tag.Event {
		ch := make(chan tag.Event, 4)
		ch <- tag.Event{Op: "message", Text: "```python\nb = sum(xs)\n```"}
		ch <- tag.Event{Op: "done"}
		close(ch)
		return ch
	}
	n := &fakeNode{uuid: "cell1", typ: database.TypeNLPCompute, text: "sum inputs, store as b"}

	cmd := runNLPCompute(h, n)
	if cmd == nil {
		t.Fatalf("run must start: %s", h.flash)
	}
	msg := cmd()
	for i := 0; i < 20; i++ {
		ev, ok := msg.(ncEvMsg)
		if !ok {
			t.Fatalf("unexpected msg %T", msg)
		}
		next := ev.HandleNodePlugin(h)
		if next == nil {
			break
		}
		msg = next()
	}

	d := ncLoad(h, "cell1")
	if d.Code != "b = sum(xs)" || d.Lang != "python" {
		t.Fatalf("cell = %+v", d)
	}
	if d.Cwd == "" {
		t.Fatal("cwd must pin on first run")
	}
	if ncStateOf(h, "cell1").busy {
		t.Fatal("cell must park idle")
	}
	if !(ncView{}).Enter(h, n) {
		t.Fatal("code version must open")
	}
}

// ── canvas ──────────────────────────────────────────────────────────────────

// TestCanvasObjectsAndDistances: colored backgrounds on the object plane are
// the objects; a distance constraint captures the current center distance as
// % of the canvas width, and the solver restores it after a disturbance.
func TestCanvasObjectsAndDistances(t *testing.T) {
	d := &canvasDoc{W: 100, H: 20}
	// a 2×2 red region and a 2×2 blue region 20 cells apart
	for _, x := range []int{2, 3} {
		for _, y := range []int{2, 3} {
			setCell(&d.Objects, canvasCell{X: x, Y: y, Ch: " ", Bg: "red"})
			setCell(&d.Objects, canvasCell{X: x + 20, Y: y, Ch: " ", Bg: "blue"})
		}
	}
	if got := len(d.objects()); got != 2 {
		t.Fatalf("distinct background colors must be distinct objects, got %d", got)
	}
	// an item stamped on red rides red
	setCell(&d.Objects, canvasCell{X: 2, Y: 2, Ch: "★", Fg: "yellow", Bg: d.objectColorAt(2, 2)})

	dist := canvasDist{A: "red", B: "blue"}
	cells, pct, ok := d.distance(dist)
	if !ok || cells != 20 || pct != 20 {
		t.Fatalf("distance = %v cells %v%% ok=%v, want 20 cells 20%%", cells, pct, ok)
	}
	dist.Pct = pct
	d.Dists = append(d.Dists, dist)

	// disturb blue and re-solve: the declared distance is restored
	d.translateObject("blue", 10, 0)
	d.solve()
	if got, _, _ := d.distance(dist); math.Abs(got-20) > 1 {
		t.Fatalf("solve must restore the declared distance, got %v", got)
	}
	// the star item traveled with red all along
	if d.objectColorAt(2, 2) != "red" {
		t.Fatal("items must ride their region")
	}
}

// TestCanvasDocRoundTrip: both planes and the constraints persist.
func TestCanvasDocRoundTrip(t *testing.T) {
	h := newFakeHost(t)
	doc := &canvasDoc{W: 30, H: 8,
		Cells:   []canvasCell{{X: 1, Y: 1, Ch: "★", Fg: "yellow"}},
		Objects: []canvasCell{{X: 5, Y: 5, Ch: " ", Bg: "green"}},
		Dists:   []canvasDist{{A: "green", B: "red", Pct: 10}},
	}
	canvasSave(h, "cv1", doc)
	got := canvasLoad(h, "cv1")
	if len(got.Cells) != 1 || len(got.Objects) != 1 || len(got.Dists) != 1 {
		t.Fatalf("round trip lost data: %+v", got)
	}
}

// TestCanvasPaletteSearch: the user's own examples must hit.
func TestCanvasPaletteSearch(t *testing.T) {
	cases := map[string]string{
		"branch":          "├",
		"curved":          "╭",
		"geometry square": "□",
		"half block":      "▌",
	}
	for q, want := range cases {
		found := false
		for _, hit := range canvasPaletteSearch(q) {
			if hit.ch == want {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("query %q must surface %q", q, want)
		}
	}
	hits := canvasPaletteSearch("background blue")
	if len(hits) == 0 || hits[0].cat != "background" || hits[0].color != "blue" {
		t.Fatalf("background blue mismatch: %+v", hits)
	}
}

// TestCanvasViewPlanes: tab flips planes, enter paints the right plane, a
// background brush defines objects, and t+t lands a distance constraint.
func TestCanvasViewPlanes(t *testing.T) {
	h := newFakeHost(t)
	n := &fakeNode{uuid: "cv2", typ: database.TypeCanvas}
	v := canvasView{}
	if !v.Enter(h, n) {
		t.Fatal("Enter must focus")
	}
	st := canvasStateOf(h, "cv2")

	key := func(s string) {
		var k tea.KeyMsg
		switch s {
		case "enter":
			k = tea.KeyMsg{Type: tea.KeyEnter}
		case "tab":
			k = tea.KeyMsg{Type: tea.KeyTab}
		case "right":
			k = tea.KeyMsg{Type: tea.KeyRight}
		default:
			k = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
		}
		v.Key(h, n, k)
	}

	key("enter") // painter stamp
	if len(st.doc.Cells) != 1 {
		t.Fatal("the painter plane must stamp a cell")
	}

	key("tab")
	if st.plane != "object" {
		t.Fatal("tab must switch planes")
	}
	// paint a red region at (1,0) and a blue region at (4,0)
	st.brush = canvasBrush{Ch: " ", Bg: "red"}
	key("right")
	key("enter")
	st.brush = canvasBrush{Ch: " ", Bg: "blue"}
	key("right")
	key("right")
	key("right")
	key("enter")
	if len(st.doc.objects()) != 2 {
		t.Fatalf("two background colors must be two objects: %+v", st.doc.Objects)
	}
	key("t") // from blue (cursor on it)
	if st.distFrom != "blue" {
		t.Fatalf("distance from = %q", st.distFrom)
	}
	st.cx = 1 // over red
	key("t")
	if len(st.doc.Dists) != 1 {
		t.Fatalf("distance must land: %+v", st.doc.Dists)
	}

	v.Leave(h, n)
	if got := canvasLoad(h, "cv2"); len(got.Dists) != 1 || len(got.objects()) != 2 {
		t.Fatalf("Leave must persist: %+v", got)
	}
}

// TestNCColorLine: the simple highlighter's three behaviors.
func TestNCColorLine(t *testing.T) {
	th := editor.NodeTheme()
	if got := ncColorLine("# a comment"); !strings.HasPrefix(got, th.Dim) {
		t.Fatalf("comment must dim: %q", got)
	}
	if got := ncColorLine("def train(x):"); !strings.Contains(got, th.Accent+"def"+th.Reset) {
		t.Fatalf("keyword must color: %q", got)
	}
}
