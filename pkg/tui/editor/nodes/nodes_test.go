package nodes

import (
	"context"
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

// TestCanvasObjectsAndTies: distinct colors are distinct objects; a tie holds
// the gap between edges and the solver translates the From object.
func TestCanvasObjectsAndTies(t *testing.T) {
	d := &canvasDoc{W: 40, H: 12}
	// a 2×2 red shape at (2,2) and a 1×2 blue shape at (10,3)
	for _, c := range []canvasCell{{X: 2, Y: 2, Fg: "red"}, {X: 3, Y: 2, Fg: "red"}, {X: 2, Y: 3, Fg: "red"}, {X: 3, Y: 3, Fg: "red"},
		{X: 10, Y: 3, Fg: "blue"}, {X: 10, Y: 4, Fg: "blue"}} {
		c.Ch = "█"
		setCell(&d.Objects, c)
	}
	objs := d.objects()
	if len(objs) != 2 {
		t.Fatalf("distinct colors must be distinct objects, got %d", len(objs))
	}

	// tie: blue.left sits 3 right of red.right
	d.Ties = append(d.Ties, canvasTie{
		From: canvasEnd{Obj: "blue", Edge: "left"},
		To:   canvasEnd{Obj: "red", Edge: "right"},
		Gap:  3,
	})
	d.solve()
	bl, _ := d.edgeCoord(canvasEnd{Obj: "blue", Edge: "left"})
	rr, _ := d.edgeCoord(canvasEnd{Obj: "red", Edge: "right"})
	if bl-rr != 3 {
		t.Fatalf("tie unsatisfied: blue.left=%d red.right=%d", bl, rr)
	}

	// widen the gap: blue translates further right
	d.Ties[0].Gap = 6
	d.solve()
	bl, _ = d.edgeCoord(canvasEnd{Obj: "blue", Edge: "left"})
	if bl-rr != 6 {
		t.Fatalf("gap change must re-solve: blue.left=%d", bl)
	}

	// border tie: red.left pinned 1 from the canvas left edge
	d.Ties = append(d.Ties, canvasTie{
		From: canvasEnd{Obj: "red", Edge: "left"},
		To:   canvasEnd{Obj: "border", Edge: "left"},
		Gap:  1,
	})
	d.solve()
	rl, _ := d.edgeCoord(canvasEnd{Obj: "red", Edge: "left"})
	if rl != 1 {
		t.Fatalf("border tie unsatisfied: red.left=%d", rl)
	}
}

// TestCanvasDocRoundTrip: both planes and the ties persist through the blob.
func TestCanvasDocRoundTrip(t *testing.T) {
	h := newFakeHost(t)
	doc := &canvasDoc{W: 30, H: 8,
		Cells:   []canvasCell{{X: 1, Y: 1, Ch: "★", Fg: "yellow"}},
		Objects: []canvasCell{{X: 5, Y: 5, Ch: "█", Fg: "green"}},
		Ties:    []canvasTie{{From: canvasEnd{Obj: "green", Edge: "top"}, To: canvasEnd{Obj: "border", Edge: "top"}, Gap: 5}},
	}
	canvasSave(h, "cv1", doc)
	got := canvasLoad(h, "cv1")
	if len(got.Cells) != 1 || len(got.Objects) != 1 || len(got.Ties) != 1 {
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

// TestCanvasViewPlanes: tab flips planes, enter paints the right plane, and
// t+t on two objects lands a tie.
func TestCanvasViewPlanes(t *testing.T) {
	h := newFakeHost(t)
	n := &fakeNode{uuid: "cv2", typ: database.TypeCanvas}
	v := canvasView{}
	if !v.Enter(h, n) {
		t.Fatal("Enter must focus")
	}
	st := canvasStateOf(h, "cv2")

	// draw plane stamp
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
	key("enter")
	if len(st.doc.Cells) != 1 {
		t.Fatal("draw plane must stamp a cell")
	}

	// object plane: paint red at (1,0), blue at (3,0), tie them
	key("tab")
	if st.plane != "object" {
		t.Fatal("tab must switch planes")
	}
	key("right")
	key("enter") // red at (1,0)
	st.objColor = "blue"
	key("right")
	key("right")
	key("enter") // blue at (3,0)
	if len(st.doc.objects()) != 2 {
		t.Fatalf("two colors must be two objects: %+v", st.doc.Objects)
	}
	key("t") // from blue (cursor on it)
	if st.tieFrom == nil || st.tieFrom.Obj != "blue" {
		t.Fatalf("tie from = %+v", st.tieFrom)
	}
	st.cx = 1 // over red
	key("t")
	if len(st.doc.Ties) != 1 {
		t.Fatalf("tie must land: %+v", st.doc.Ties)
	}

	v.Leave(h, n)
	if got := canvasLoad(h, "cv2"); len(got.Ties) != 1 || len(got.Objects) != 2 {
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
