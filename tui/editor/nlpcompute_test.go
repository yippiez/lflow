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

// TestNCProseIsAnOrdinaryRichRow: the instruction renders through the same
// renderBody every row does — red by DEFAULT, a /color taking over when the node
// has one, the block cursor drawn where the caret is, and a chip in the text
// rendering as a chip instead of a raw anchor.
func TestNCProseIsAnOrdinaryRichRow(t *testing.T) {
	m := ncTestModel(t, nil)
	m.chips = map[string]database.Chip{}
	it := &item{uuid: "cell1", typ: database.TypeNLPCompute, name: "sum inputs"}

	body := renderBody(it, it.name, -1, false, m.chips)
	if !strings.Contains(body, cRed) {
		t.Fatalf("the default instruction is not red: %q", body)
	}
	if strings.Contains(body, cInvert) {
		t.Fatalf("caret -1 must draw no cursor: %q", body)
	}
	if got := renderBody(it, it.name, 2, true, m.chips); !strings.Contains(stripSGR(got), "sum inputs") ||
		!strings.Contains(got, cInvert) {
		t.Fatalf("the caret must invert its cell on the prose row: %q", got)
	}

	// a /color is the node's own word on the matter, red or not
	it.style = "color:blue"
	body = renderBody(it, it.name, -1, false, m.chips)
	if strings.Contains(body, cRed) {
		t.Fatalf("a recolored cell is still forced red: %q", body)
	}
	if _, color := typeOf(it.typ).glyph(it); color == cRed {
		t.Fatal("the → arrow ignored the node's color")
	}
	it.style = ""

	// …and a chip spliced into the instruction renders as the chip
	anchor := m.createLabeledChip(chipKindLink, "https://example.com", "the spec")
	it.name = "follow " + anchor
	body = renderBody(it, it.name, -1, false, m.chips)
	if !strings.Contains(stripSGR(body), "spec") || strings.Contains(body, anchor) {
		t.Fatalf("the chip did not render inside the instruction: %q", stripSGR(body))
	}
}

// TestNCLangMarkerInTheSuffix: a cell that has generated names its language in
// the shared row suffix — where every other type's marks live.
func TestNCLangMarkerInTheSuffix(t *testing.T) {
	m := ncTestModel(t, nil)
	it := &item{uuid: "cell1", typ: database.TypeNLPCompute, name: "sum inputs", parent: m.tree.root}
	m.tree.root.children = append(m.tree.root.children, it)
	m.tree.byUUID["cell1"] = it
	m.refreshRows()
	ncSave(m, "cell1", ncData{Code: "b = 1", Lang: "python"})

	r := m.rows[m.rowIndexOf(it)]
	if suffix := stripSGR(m.typeSuffix(r)); !strings.Contains(suffix, "python") {
		t.Fatalf("the cell does not say what it holds: %q", suffix)
	}
}

// TestNCShineWhileGenerating: while the turn is in flight the instruction SHINES
// (per-rune color, no agent trace) and the animating flag keeps the tick alive;
// completion drops both and the row goes back to its own color.
func TestNCShineWhileGenerating(t *testing.T) {
	m := ncTestModel(t, func() (string, error) {
		return "```python\nb = 1\n```", nil
	})
	m.chips = map[string]database.Chip{}
	it := &item{uuid: "cell1", typ: database.TypeNLPCompute, name: "sum inputs"}

	cmd := runNLPCompute(m, it)
	if a, _ := m.nodeStore("cell1")["animating"].(bool); !a {
		t.Fatal("run must raise the animating flag")
	}
	shown := renderBody(it, it.name, -1, false, m.chips)
	if strings.Contains(shown, "computing") || strings.Contains(shown, "⋯") {
		t.Fatalf("busy render must not show an agent trace: %q", shown)
	}
	// the shine paints per rune, so the row carries several colors where an
	// idle one carries a single flat foreground
	if strings.Count(shown, "\x1b[38;2;") < 2 || strings.Contains(shown, cRed) {
		t.Fatalf("busy render is not shining: %q", shown)
	}
	if !strings.Contains(stripSGR(shown), "sum inputs") {
		t.Fatalf("the shine ate the instruction: %q", stripSGR(shown))
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
	if ncGenerating["cell1"] {
		t.Fatal("completion must stop the shine")
	}
}
