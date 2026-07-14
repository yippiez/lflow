package nodes

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/lflow/lflow/pkg/tui/database"
	"github.com/lflow/lflow/pkg/tui/tag"
)

func TestSplitFenceInfo(t *testing.T) {
	lang, path := splitFenceInfo("mcfunction data/ex/function/greet.mcfunction")
	if lang != "mcfunction" || path != "data/ex/function/greet.mcfunction" {
		t.Fatalf("info parse: %q %q", lang, path)
	}
	lang, path = splitFenceInfo("json")
	if lang != "json" || path != "" {
		t.Fatalf("lang only: %q %q", lang, path)
	}
	if l, p := splitFenceInfo(""); l != "" || p != "" {
		t.Fatalf("empty: %q %q", l, p)
	}
}

// TestDatapackFlow: run → the fake turn's reply lands as the file's code, the
// path/lang parse from the fence info, the cwd pins, the state parks idle.
func TestDatapackFlow(t *testing.T) {
	h := newFakeHost(t)
	h.compute = func() <-chan tag.Event {
		ch := make(chan tag.Event, 4)
		ch <- tag.Event{Op: "message", Text: "```mcfunction data/ex/function/heal.mcfunction\neffect give @a regeneration 5 1\n```"}
		ch <- tag.Event{Op: "done"}
		close(ch)
		return ch
	}
	n := &fakeNode{uuid: "dp1", typ: database.TypeDatapack, text: "heal everyone for 5 seconds"}

	cmd := runDatapack(h, n)
	if cmd == nil {
		t.Fatalf("run must start: %s", h.flash)
	}
	msg := cmd()
	for i := 0; i < 20; i++ {
		ev, ok := msg.(dpEvMsg)
		if !ok {
			t.Fatalf("unexpected msg %T", msg)
		}
		next := ev.HandleNodePlugin(h)
		if next == nil {
			break
		}
		msg = next()
	}

	d := dpLoad(h, "dp1")
	if d.Code != "effect give @a regeneration 5 1" {
		t.Fatalf("code = %q", d.Code)
	}
	if d.Lang != "mcfunction" || d.Path != "data/ex/function/heal.mcfunction" {
		t.Fatalf("meta = %+v", d)
	}
	if d.Cwd == "" {
		t.Fatal("cwd must pin on first run")
	}
	if dpStateOf(h, "dp1").busy {
		t.Fatal("cell must park idle")
	}
	if !(dpView{}).Enter(h, n) {
		t.Fatal("code face must open")
	}
}

// TestDatapackRefusesEmpty: alt+r on a blank instruction nudges instead of firing.
func TestDatapackRefusesEmpty(t *testing.T) {
	h := newFakeHost(t)
	n := &fakeNode{uuid: "dp2", typ: database.TypeDatapack}
	if cmd := runDatapack(h, n); cmd != nil {
		t.Fatal("blank instruction must not fire")
	}
	if h.flash == "" {
		t.Fatal("blank instruction must flash a nudge")
	}
}

// TestDatapackCodeFaceEdits: the code face seeds from the generated file, takes
// edits, and flushes them back — the "in code it's editable" rule.
func TestDatapackCodeFaceEdits(t *testing.T) {
	h := newFakeHost(t)
	n := &fakeNode{uuid: "dp1", typ: database.TypeDatapack, text: "heal everyone"}
	dpSave(h, "dp1", dpData{Code: "effect give @a regeneration", Lang: "mcfunction"})

	v := dpView{}
	if !v.Enter(h, n) {
		t.Fatal("code face must open when a file exists")
	}
	for _, r := range " 5 1" {
		if r == ' ' {
			v.Key(h, n, tea.KeyMsg{Type: tea.KeySpace})
		} else {
			v.Key(h, n, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		}
	}
	v.Leave(h, n)
	if got := dpLoad(h, "dp1").Code; got != "effect give @a regeneration 5 1" {
		t.Fatalf("edited code = %q", got)
	}
}

// TestDatapackBlockCode: the node renders AS the code block once a file exists
// (and not while generating). No code / busy → the prose row; idle+code → the
// block with no caret; focused → the live edit buffer + caret.
func TestDatapackBlockCode(t *testing.T) {
	h := newFakeHost(t)
	n := &fakeNode{uuid: "dp1", typ: database.TypeDatapack, text: "heal everyone"}

	if _, _, ok := dpBlockCode(h, n, false); ok {
		t.Fatal("no file yet → prose row, not a block")
	}
	dpSave(h, "dp1", dpData{Code: "say hi", Lang: "mcfunction"})
	code, caret, ok := dpBlockCode(h, n, false)
	if !ok || code != "say hi" || caret != -1 {
		t.Fatalf("idle+code block = %q,%d,%v", code, caret, ok)
	}
	// busy yields to the shining prose even with a file present
	dpStateOf(h, "dp1").busy = true
	if _, _, ok := dpBlockCode(h, n, false); ok {
		t.Fatal("while generating → shining prose, not a block")
	}
	dpStateOf(h, "dp1").busy = false
	// prose face (alt+e flipped) yields to the prose row
	h.NodeStore("dp1")["blockFace"] = "nlp"
	if _, _, ok := dpBlockCode(h, n, false); ok {
		t.Fatal("prose face → prose row, not the block")
	}
	if (dpView{}).Enter(h, n) {
		t.Fatal("prose face → code editor must decline")
	}
}

// TestDatapackRenderShine: while generating the instruction shines (colored, no
// trace) and the animating flag is raised; idle with a file shows a {path} chip.
func TestDatapackRenderShine(t *testing.T) {
	h := newFakeHost(t)
	h.compute = func() <-chan tag.Event {
		ch := make(chan tag.Event, 4)
		ch <- tag.Event{Op: "message", Text: "```mcfunction data/ex/function/x.mcfunction\nsay hi\n```"}
		ch <- tag.Event{Op: "done"}
		close(ch)
		return ch
	}
	n := &fakeNode{uuid: "dp1", typ: database.TypeDatapack, text: "say hi to everyone"}

	cmd := runDatapack(h, n)
	if a, _ := h.NodeStore("dp1")["animating"].(bool); !a {
		t.Fatal("run must raise the animating flag")
	}
	if shown := dpRender(h, n); !strings.Contains(shown, "\x1b[38;2;") {
		t.Fatalf("busy render must shine: %q", shown)
	}

	msg := cmd()
	for i := 0; i < 20; i++ {
		ev, ok := msg.(dpEvMsg)
		if !ok {
			t.Fatalf("unexpected msg %T", msg)
		}
		next := ev.HandleNodePlugin(h)
		if next == nil {
			break
		}
		msg = next()
	}
	if a, _ := h.NodeStore("dp1")["animating"].(bool); a {
		t.Fatal("completion must drop the animating flag")
	}
	if shown := dpRender(h, n); !strings.Contains(shown, "{data/ex/function/x.mcfunction}") {
		t.Fatalf("idle render must show the path chip: %q", shown)
	}
}
