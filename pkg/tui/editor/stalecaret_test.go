package editor

import (
	tea "github.com/charmbracelet/bubbletea"
	"testing"
)

// TestStaleCaretDoesNotPanic pins the crash fix: a caret left past the current
// node's length (e.g. after a worker run reseeded the compose) must be clamped,
// not panic slicing runes[:m.caret].
func TestStaleCaretDoesNotPanic(t *testing.T) {
	m := newTestModel(80, "short")
	m.cursor = 0
	m.caret = 109 // far past len("short") == 5

	// typing a rune used to panic: runes[:109] with capacity 5
	if _, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")}); m.cursorItem() == nil {
		t.Fatal("cursor lost")
	}
	if got := m.cursorItem().name; got != "shortx" {
		t.Fatalf("name = %q, want shortx (caret clamped to end)", got)
	}

	// backspace and ctrl+backspace with a stale caret must also be safe
	m.caret = 99
	m.handleKey(tea.KeyMsg{Type: tea.KeyBackspace})
	m.caret = 99
	m.handleKey(tea.KeyMsg{Type: tea.KeyCtrlH})
}
