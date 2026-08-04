package editor

import (
	"github.com/lflow/lflow/pkg/tui/database"

	"strings"
	"testing"
)

// TestTextSelectionGrowsByWordAndRune: shift+←/→ nudges one rune and
// ctrl+shift+←/→ takes whole words, matching ordinary text editors.
func TestTextSelectionGrowsByWordAndRune(t *testing.T) {
	m := newTestModel(100, "paint some words here")
	t.Cleanup(func() { textSelUUID = "" })
	n := m.rows[0].it
	n.uuid = "p1"
	m.tree.byUUID["p1"] = n

	m.caret = len("paint ")
	m.press("shift+right") // "s"
	if _, lo, hi, ok := m.textSelection(); !ok || lo != 6 || hi != 7 {
		t.Fatalf("after shift+right: [%d,%d) ok=%v, want [6,7)", lo, hi, ok)
	}
	m.press("ctrl+shift+right") // finish "some"
	if _, lo, hi, _ := m.textSelection(); lo != 6 || hi != 10 {
		t.Fatalf("ctrl+shift+right: [%d,%d), want [6,10)", lo, hi)
	}
	m.press("ctrl+shift+right") // add " words"
	if _, lo, hi, _ := m.textSelection(); lo != 6 || hi != 16 {
		t.Fatalf("second ctrl+shift+right: [%d,%d), want [6,16)", lo, hi)
	}
	m.press("shift+left") // one rune back off the edge
	if _, lo, hi, _ := m.textSelection(); lo != 6 || hi != 15 {
		t.Fatalf("shift+left: [%d,%d), want [6,15)", lo, hi)
	}

	// the bar renders over the selected runes only
	m.caret = 6
	m.clearTextSel()
	m.press("shift+right")
	body := renderBody(n, n.name, -1, false, nil)
	before, after, found := strings.Cut(body, bgTextSel())
	if !found {
		t.Fatalf("selection must render a bar, got %q", body)
	}
	if strings.Contains(stripSGR(before), "s") || !strings.HasPrefix(stripSGR(after), "s") {
		t.Fatalf("the bar must start at %q, got %q", "s", body)
	}
}

// TestTextSelectionDropsOnMovementAndEsc: the selection is transient — a plain
// arrow or esc releases it, and it never survives onto another node.
func TestTextSelectionDropsOnMovementAndEsc(t *testing.T) {
	m := newTestModel(100, "alpha beta", "second node")
	t.Cleanup(func() { textSelUUID = "" })

	m.caret = 0
	m.press("shift+right")
	if !m.textSelOn {
		t.Fatal("shift+right must start a selection")
	}
	m.press("right")
	if m.textSelOn || textSelUUID != "" {
		t.Fatal("a plain arrow must drop the selection")
	}

	m.caret = 0
	m.press("shift+right")
	m.press("esc")
	if m.textSelOn {
		t.Fatal("esc must drop the selection")
	}

	// shift+down hands over to the row selection: never both at once
	m.caret = 0
	m.press("shift+right")
	m.press("shift+down")
	if m.textSelOn || !m.selOn {
		t.Fatalf("shift+down must swap to a row selection (text=%v rows=%v)", m.textSelOn, m.selOn)
	}
}

// TestTypingReplacesTheSelection: selecting a word and typing used to leave the
// word sitting there with a stray letter beside it. A selection is content —
// typing over it replaces it, backspace removes it whole.
func TestTypingReplacesTheSelection(t *testing.T) {
	m, _ := dbModel(t, database.Node{UUID: "n", Name: "delete this word now"})
	it := m.tree.byUUID["n"]
	m.cursor = m.rowIndexOf(it)

	// select "this " with shift+ctrl+right from the start of it
	m.caret = len("delete ")
	m.press("ctrl+shift+right")
	// a word jump takes the word, not the space after it
	if _, lo, hi, ok := m.textSelection(); !ok || lo != 7 || hi != 11 {
		t.Fatalf("selection = %d..%d ok=%v", lo, hi, ok)
	}
	m.press("X")

	if it.name != "delete X word now" {
		t.Errorf("name = %q, want the selection replaced by the typed letter", it.name)
	}
	if m.caret != 8 {
		t.Errorf("caret = %d, want it after the typed letter", m.caret)
	}
	if m.textSelOn {
		t.Error("the selection outlived the typing")
	}
}

// TestBackspaceDeletesTheSelection: the same run, removed rather than replaced.
func TestBackspaceDeletesTheSelection(t *testing.T) {
	m, _ := dbModel(t, database.Node{UUID: "n", Name: "delete this word now"})
	it := m.tree.byUUID["n"]
	m.cursor = m.rowIndexOf(it)
	m.caret = len("delete ")
	m.press("ctrl+shift+right")

	m.press("backspace")
	if it.name != "delete  word now" {
		t.Errorf("name = %q, want the whole selected word gone", it.name)
	}
	if m.caret != 7 {
		t.Errorf("caret = %d, want it where the run began", m.caret)
	}
}

// TestSelectionReplaceTakesItsChipsWithIt: an anchor inside the deleted run
// would otherwise survive as an orphan "@?" pointing at nothing.
func TestSelectionReplaceTakesItsChipsWithIt(t *testing.T) {
	m, _ := dbModel(t, database.Node{UUID: "n", Name: "keep "})
	it := m.tree.byUUID["n"]
	m.cursor = m.rowIndexOf(it)
	m.caret = len([]rune(it.name))
	anchor := m.createLabeledChip(chipKindTag, "qol", "")
	it.name += anchor
	id := anchorChipID(anchor)

	textSelUUID, textSelLo, textSelHi = it.uuid, len([]rune("keep ")), len([]rune(it.name))
	m.textSelOn = true
	m.press("z")

	if it.name != "keep z" {
		t.Errorf("name = %q", it.name)
	}
	if _, ok := m.chips[id]; ok {
		t.Error("the chip record outlived the anchor that was deleted")
	}
}

// TestNoteSelectionReplaces: the note surface behaves the same way.
func TestNoteSelectionReplaces(t *testing.T) {
	m, _ := dbModel(t, database.Node{UUID: "n", Name: "row", Note: "delete this word"})
	it := m.tree.byUUID["n"]
	m.cursor = m.rowIndexOf(it)
	m.mode = modeNote
	m.caret = len("delete ")
	m.press("ctrl+shift+right")
	if _, _, _, ok := m.textSelection(); !ok {
		t.Fatal("no note selection")
	}
	m.press("Q")
	if it.note != "delete Q word" {
		t.Errorf("note = %q, want the selection replaced", it.note)
	}
	if it.name != "row" {
		t.Errorf("the node's name was touched: %q", it.name)
	}
}
