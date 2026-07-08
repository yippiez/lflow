package editor

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lflow/lflow/pkg/tui/database"
)

// tkey extends editor_test.go's key helper with the extra keys these tests use.
func tkey(name string) tea.KeyMsg {
	switch name {
	case "ctrl+w":
		return tea.KeyMsg{Type: tea.KeyCtrlW}
	case "home":
		return tea.KeyMsg{Type: tea.KeyHome}
	case "end":
		return tea.KeyMsg{Type: tea.KeyEnd}
	}
	return key(name)
}

// TestNoteEditorWordMovement drives the /note field: ctrl+←/→ jump words and
// ctrl+w deletes the word left, the same vocabulary as the outline editor.
func TestNoteEditorWordMovement(t *testing.T) {
	root := &item{uuid: "root"}
	n := &item{uuid: "n1", name: "a node", note: "alpha beta gamma", parent: root}
	root.children = []*item{n}
	tr := &tree{root: root, byUUID: map[string]*item{"root": root, "n1": n},
		externalNames: map[string]string{}, snapshots: map[string]snapshot{}}
	m := &Model{tree: tr, viewStack: []*item{root}, width: 80, height: 24, mode: modeNote}
	m.refreshRows()
	m.caret = len([]rune(n.note)) // note editor opens at the end

	m.handleNoteKey(tkey("ctrl+left"))
	if m.caret != len("alpha beta ") {
		t.Fatalf("ctrl+left: caret = %d, want %d", m.caret, len("alpha beta "))
	}
	m.handleNoteKey(tkey("ctrl+left"))
	if m.caret != len("alpha ") {
		t.Fatalf("ctrl+left ×2: caret = %d, want %d", m.caret, len("alpha "))
	}
	m.handleNoteKey(tkey("ctrl+right"))
	if m.caret != len("alpha beta ") {
		t.Fatalf("ctrl+right: caret = %d, want %d", m.caret, len("alpha beta "))
	}
	m.handleNoteKey(tkey("ctrl+w"))
	if n.note != "alpha gamma" {
		t.Fatalf("ctrl+w: note = %q, want %q", n.note, "alpha gamma")
	}
	m.handleNoteKey(tkey("home"))
	if m.caret != 0 {
		t.Fatalf("home: caret = %d", m.caret)
	}
	m.handleNoteKey(tkey("end"))
	if m.caret != len([]rune(n.note)) {
		t.Fatalf("end: caret = %d", m.caret)
	}
	// mid-note insertion lands at the caret, not the end
	m.handleNoteKey(tkey("home"))
	m.handleNoteKey(tkey("X"))
	if n.note != "Xalpha gamma" {
		t.Fatalf("insert at caret: note = %q", n.note)
	}
}

// TestLinkEditorCaret drives the alt+e link editor: the caret moves, words
// jump, and edits land at the caret instead of only appending at the end.
func TestLinkEditorCaret(t *testing.T) {
	db := database.InitTestMemoryDB(t)
	root := &item{uuid: "root"}
	tr := &tree{db: db, root: root, byUUID: map[string]*item{"root": root},
		externalNames: map[string]string{}, snapshots: map[string]snapshot{}}
	m := &Model{db: db, tree: tr, viewStack: []*item{root}, width: 80, height: 24,
		chips: map[string]database.Chip{}}

	c := database.Chip{ID: "c1", Kind: chipKindLink, Value: "https://example.com", Label: "my link name"}
	m.chips[c.ID] = c
	m.openLinkEdit(c)

	if m.linkEditCaret != len("my link name") {
		t.Fatalf("open: caret at %d, want end", m.linkEditCaret)
	}
	// word-jump back twice, then insert mid-field
	m.handleLinkEditKey(tkey("ctrl+left"))
	m.handleLinkEditKey(tkey("ctrl+left"))
	if m.linkEditCaret != len("my ") {
		t.Fatalf("ctrl+left ×2: caret = %d, want %d", m.linkEditCaret, len("my "))
	}
	m.handleLinkEditKey(tkey("X"))
	if m.linkEditName != "my Xlink name" {
		t.Fatalf("insert at caret: name = %q", m.linkEditName)
	}
	// plain left then backspace deletes at the caret, not at the end
	m.handleLinkEditKey(tkey("left"))
	m.handleLinkEditKey(tkey("backspace"))
	if m.linkEditName != "myXlink name" {
		t.Fatalf("backspace at caret: name = %q", m.linkEditName)
	}
	// switching to the target field resets the caret to that field's end
	m.handleLinkEditKey(tkey("tab"))
	if m.linkEditField != 1 || m.linkEditCaret != len("https://example.com") {
		t.Fatalf("tab: field %d caret %d", m.linkEditField, m.linkEditCaret)
	}
	// ctrl+w in the target eats the last word
	m.handleLinkEditKey(tkey("ctrl+w"))
	if m.linkEditTarget != "" && m.linkEditTarget == "https://example.com" {
		t.Fatalf("ctrl+w did nothing: %q", m.linkEditTarget)
	}
	// enter persists through saveLinkEdit
	m.handleLinkEditKey(tkey("enter"))
	if got := m.chips["c1"].Label; got != "myXlink name" {
		t.Fatalf("saved label = %q", got)
	}
}
