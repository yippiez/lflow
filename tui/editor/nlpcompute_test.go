package editor

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lflow/lflow/tui/database"
	"github.com/lflow/lflow/tui/nlp"
)

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

// ncTestModel builds a Model with a live DB and an injectable compute turn.
func ncTestModel(t *testing.T, compute func() (string, error)) *Model {
	t.Helper()
	prev := computeTurn
	computeTurn = func(_ context.Context, _ string, _ func(nlp.Event)) (string, error) {
		if compute == nil {
			return "", nil
		}
		return compute()
	}
	t.Cleanup(func() { computeTurn = prev })
	m := newTestModel(80)
	m.db = database.InitTestMemoryDB(t)
	return m
}

// TestNLPComputeFlow: run → the fake turn's reply lands as the cell's code,
// the cwd pins, the state parks idle.
func TestNLPComputeFlow(t *testing.T) {
	m := ncTestModel(t, func() (string, error) {
		return "```python\nb = sum(xs)\n```", nil
	})
	it := &item{uuid: "cell1", typ: database.TypeNLPCompute, name: "sum inputs, store as b"}

	cmd := runNLPCompute(m, it)
	if cmd == nil {
		t.Fatalf("run must start: %s", m.flash)
	}
	msg, ok := cmd().(ncDoneMsg)
	if !ok {
		t.Fatalf("unexpected msg %T", msg)
	}
	m.handleNCDone(msg)

	d := ncLoad(m, "cell1")
	if d.Code != "b = sum(xs)" || d.Lang != "python" {
		t.Fatalf("cell = %+v", d)
	}
	if d.Cwd == "" {
		t.Fatal("cwd must pin on first run")
	}
	if ncStateOf(m, "cell1").busy {
		t.Fatal("cell must park idle")
	}
	if !(ncView{}).enter(m, it) {
		t.Fatal("code version must open")
	}
}

// TestNCCodeFaceEdits: the code face seeds from the generated snippet, takes
// edits, and flushes them back to the cell — the "in code it's editable" rule.
func TestNCCodeFaceEdits(t *testing.T) {
	m := ncTestModel(t, nil)
	it := &item{uuid: "cell1", typ: database.TypeNLPCompute, name: "sum inputs"}
	ncSave(m, "cell1", ncData{Code: "b = 0"})

	v := ncView{}
	if !v.enter(m, it) {
		t.Fatal("code face must open when code exists")
	}
	// append " + 1" at the end (Enter seeded the caret there)
	for _, r := range " + 1" {
		if r == ' ' {
			v.key(m, it, tea.KeyMsg{Type: tea.KeySpace})
		} else {
			v.key(m, it, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		}
	}
	v.leave(m, it)
	if got := ncLoad(m, "cell1").Code; got != "b = 0 + 1" {
		t.Fatalf("edited code = %q, want %q", got, "b = 0 + 1")
	}
}

// TestNCCodeFaceRefusesEmpty: with no code yet the face declines, nudging alt+r.
func TestNCCodeFaceRefusesEmpty(t *testing.T) {
	m := ncTestModel(t, nil)
	it := &item{uuid: "cell2", typ: database.TypeNLPCompute}
	if (ncView{}).enter(m, it) {
		t.Fatal("code face must refuse when there is no code")
	}
}

// TestNCBlockCode: the node renders AS the code block once a snippet exists (and
// not while generating). No code / busy → the prose row (ok=false); idle+code →
// the block with no caret; focused → the live edit buffer + caret.
func TestNCBlockCode(t *testing.T) {
	m := ncTestModel(t, nil)
	it := &item{uuid: "cell1", typ: database.TypeNLPCompute, name: "sum inputs"}

	if _, _, ok := ncBlockCode(m, it, false); ok {
		t.Fatal("no code yet → prose row, not a block")
	}
	ncSave(m, "cell1", ncData{Code: "b = 1", Lang: "python"})
	code, caret, ok := ncBlockCode(m, it, false)
	if !ok || code != "b = 1" || caret != -1 {
		t.Fatalf("idle+code block = %q,%d,%v", code, caret, ok)
	}
	// busy yields to the shining prose even with code present
	ncStateOf(m, "cell1").busy = true
	if _, _, ok := ncBlockCode(m, it, false); ok {
		t.Fatal("while generating → shining prose, not a block")
	}
	ncStateOf(m, "cell1").busy = false
	// focused → the live buffer drives the block
	st := ncStateOf(m, "cell1")
	st.buf, st.caret = "b = 2", 3
	if code, caret, _ := ncBlockCode(m, it, true); code != "b = 2" || caret != 3 {
		t.Fatalf("focused block = %q,%d", code, caret)
	}
}

// TestNCProseFace: the "nlp" face (alt+e flipped to prose) shows the prose row
// and declines the code editor even with code present, so the instruction stays
// inline-editable; the code face ("" / "code") shows and edits the block.
func TestNCProseFace(t *testing.T) {
	m := ncTestModel(t, nil)
	it := &item{uuid: "cell1", typ: database.TypeNLPCompute, name: "sum inputs"}
	ncSave(m, "cell1", ncData{Code: "b = 1", Lang: "python"})

	// default (code) face: block shows and the editor opens
	if _, _, ok := ncBlockCode(m, it, false); !ok {
		t.Fatal("default face → code block shows")
	}
	if !(ncView{}).enter(m, it) {
		t.Fatal("default face → code editor opens")
	}
	(ncView{}).leave(m, it)

	// prose face: block yields, editor declines (instruction edited inline)
	m.nodeStore("cell1")["blockFace"] = "nlp"
	if _, _, ok := ncBlockCode(m, it, false); ok {
		t.Fatal("prose face → prose row, not the block")
	}
	if (ncView{}).enter(m, it) {
		t.Fatal("prose face → code editor must decline")
	}
}

// TestNCRenderCaret: the prose row carries the block cursor like any editable
// row — at caret within the instruction the cell inverts, past the end a trailing
// block cell marks the insertion point, and caret -1 (unselected, note/flash
// mode) draws none.
func TestNCRenderCaret(t *testing.T) {
	m := ncTestModel(t, nil)
	it := &item{uuid: "cell1", typ: database.TypeNLPCompute, name: "sum inputs"}
	ncSave(m, "cell1", ncData{Code: "b = 1", Lang: "python"})

	if got := ncRender(m, it, -1); strings.Contains(got, "\x1b[7m") {
		t.Fatalf("caret -1 must draw no cursor: %q", got)
	}
	if got := ncRender(m, it, 2); !strings.Contains(got, "su\x1b[7mm") {
		t.Fatalf("caret inside the instruction must invert that cell: %q", got)
	}
	if got := ncRender(m, it, len([]rune("sum inputs"))); !strings.Contains(got, "\x1b[7m ") {
		t.Fatalf("caret past the end must draw a trailing block cell: %q", got)
	}
	if !strings.Contains(ncRender(m, it, 3), cDim+"{python}") {
		t.Fatal("the {lang} chip must survive the caret")
	}
	st := ncStateOf(m, "cell1")
	st.busy = true
	if got := ncRender(m, it, 3); !strings.Contains(got, "\x1b[38;2;") {
		t.Fatalf("busy render must keep shining, not the caret: %q", got)
	}
	st.busy = false
}

// TestNCRenderShineAndFlag: while generating the instruction shines (colored, no
// "computing…" trace) and the animating flag is raised so the shine tick lives;
// idle it is plain red; completion drops the flag.
func TestNCRenderShineAndFlag(t *testing.T) {
	m := ncTestModel(t, func() (string, error) {
		return "```python\nb = 1\n```", nil
	})
	it := &item{uuid: "cell1", typ: database.TypeNLPCompute, name: "sum inputs"}

	cmd := runNLPCompute(m, it)
	if a, _ := m.nodeStore("cell1")["animating"].(bool); !a {
		t.Fatal("run must raise the animating flag")
	}
	shown := ncRender(m, it, -1)
	if strings.Contains(shown, "computing") || strings.Contains(shown, "⋯") {
		t.Fatalf("busy render must not show an agent trace: %q", shown)
	}
	if !strings.Contains(shown, "\x1b[38;2;") { // an animated (shine) color is present
		t.Fatalf("busy render must be colored (shine): %q", shown)
	}

	// drain the turn to completion
	msg, ok := cmd().(ncDoneMsg)
	if !ok {
		t.Fatalf("unexpected msg %T", msg)
	}
	m.handleNCDone(msg)
	if a, _ := m.nodeStore("cell1")["animating"].(bool); a {
		t.Fatal("completion must drop the animating flag")
	}
}
