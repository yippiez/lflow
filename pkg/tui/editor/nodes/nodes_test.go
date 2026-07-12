package nodes

import (
	"context"
	"strings"
	"testing"

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
		"func (m *Model) refreshRows(it *item)":  "refreshRows",
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
