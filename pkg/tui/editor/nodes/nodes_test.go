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

// TestNCCodeFaceEdits: the code face seeds from the generated snippet, takes
// edits, and flushes them back to the cell — the "in code it's editable" rule.
func TestNCCodeFaceEdits(t *testing.T) {
	h := newFakeHost(t)
	n := &fakeNode{uuid: "cell1", typ: database.TypeNLPCompute, text: "sum inputs"}
	ncSave(h, "cell1", ncData{Code: "b = 0"})

	v := ncView{}
	if !v.Enter(h, n) {
		t.Fatal("code face must open when code exists")
	}
	// append " + 1" at the end (Enter seeded the caret there)
	for _, r := range " + 1" {
		if r == ' ' {
			v.Key(h, n, tea.KeyMsg{Type: tea.KeySpace})
		} else {
			v.Key(h, n, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		}
	}
	v.Leave(h, n)
	if got := ncLoad(h, "cell1").Code; got != "b = 0 + 1" {
		t.Fatalf("edited code = %q, want %q", got, "b = 0 + 1")
	}
}

// TestNCCodeFaceRefusesEmpty: with no code yet the face declines, nudging alt+r.
func TestNCCodeFaceRefusesEmpty(t *testing.T) {
	h := newFakeHost(t)
	n := &fakeNode{uuid: "cell2", typ: database.TypeNLPCompute}
	if (ncView{}).Enter(h, n) {
		t.Fatal("code face must refuse when there is no code")
	}
}

// TestNCBlockCode: the node renders AS the code block once a snippet exists (and
// not while generating). No code / busy → the prose row (ok=false); idle+code →
// the block with no caret; focused → the live edit buffer + caret.
func TestNCBlockCode(t *testing.T) {
	h := newFakeHost(t)
	n := &fakeNode{uuid: "cell1", typ: database.TypeNLPCompute, text: "sum inputs"}

	if _, _, ok := ncBlockCode(h, n, false); ok {
		t.Fatal("no code yet → prose row, not a block")
	}
	ncSave(h, "cell1", ncData{Code: "b = 1", Lang: "python"})
	code, caret, ok := ncBlockCode(h, n, false)
	if !ok || code != "b = 1" || caret != -1 {
		t.Fatalf("idle+code block = %q,%d,%v", code, caret, ok)
	}
	// busy yields to the shining prose even with code present
	ncStateOf(h, "cell1").busy = true
	if _, _, ok := ncBlockCode(h, n, false); ok {
		t.Fatal("while generating → shining prose, not a block")
	}
	ncStateOf(h, "cell1").busy = false
	// focused → the live buffer drives the block
	st := ncStateOf(h, "cell1")
	st.buf, st.caret = "b = 2", 3
	if code, caret, _ := ncBlockCode(h, n, true); code != "b = 2" || caret != 3 {
		t.Fatalf("focused block = %q,%d", code, caret)
	}
}

// TestNCProseFace: the "nlp" face (alt+e flipped to prose) shows the prose row
// and declines the code editor even with code present, so the instruction stays
// inline-editable; the code face ("" / "code") shows and edits the block.
func TestNCProseFace(t *testing.T) {
	h := newFakeHost(t)
	n := &fakeNode{uuid: "cell1", typ: database.TypeNLPCompute, text: "sum inputs"}
	ncSave(h, "cell1", ncData{Code: "b = 1", Lang: "python"})

	// default (code) face: block shows and the editor opens
	if _, _, ok := ncBlockCode(h, n, false); !ok {
		t.Fatal("default face → code block shows")
	}
	if !(ncView{}).Enter(h, n) {
		t.Fatal("default face → code editor opens")
	}
	(ncView{}).Leave(h, n)

	// prose face: block yields, editor declines (instruction edited inline)
	h.NodeStore("cell1")["blockFace"] = "nlp"
	if _, _, ok := ncBlockCode(h, n, false); ok {
		t.Fatal("prose face → prose row, not the block")
	}
	if (ncView{}).Enter(h, n) {
		t.Fatal("prose face → code editor must decline")
	}
}

// TestNCRenderShineAndFlag: while generating the instruction shines (colored, no
// "computing…" trace) and the animating flag is raised so the shine tick lives;
// idle it is plain red; completion drops the flag.
func TestNCRenderShineAndFlag(t *testing.T) {
	h := newFakeHost(t)
	h.compute = func() <-chan tag.Event {
		ch := make(chan tag.Event, 4)
		ch <- tag.Event{Op: "message", Text: "```python\nb = 1\n```"}
		ch <- tag.Event{Op: "done"}
		close(ch)
		return ch
	}
	n := &fakeNode{uuid: "cell1", typ: database.TypeNLPCompute, text: "sum inputs"}

	cmd := runNLPCompute(h, n)
	if a, _ := h.NodeStore("cell1")["animating"].(bool); !a {
		t.Fatal("run must raise the animating flag")
	}
	shown := ncRender(h, n)
	if strings.Contains(shown, "computing") || strings.Contains(shown, "⋯") {
		t.Fatalf("busy render must not show an agent trace: %q", shown)
	}
	if !strings.Contains(shown, "\x1b[38;2;") { // an animated (shine) color is present
		t.Fatalf("busy render must be colored (shine): %q", shown)
	}

	// drain the turn to completion
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
	if a, _ := h.NodeStore("cell1")["animating"].(bool); a {
		t.Fatal("completion must drop the animating flag")
	}
}
