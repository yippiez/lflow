package editor

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lflow/lflow/pkg/tui/database"
)

// flashLabels must hand out prefix-free labels: no label may be the start of
// another, or typing a short one would be ambiguous with a longer one.
func TestFlashLabelsArePrefixFree(t *testing.T) {
	for _, n := range []int{1, 5, 26, 27, 50, 200} {
		labels := flashLabels(n)
		if len(labels) != n {
			t.Fatalf("n=%d: got %d labels, want %d", n, len(labels), n)
		}
		seen := map[string]bool{}
		for _, l := range labels {
			if seen[l] {
				t.Fatalf("n=%d: duplicate label %q", n, l)
			}
			seen[l] = true
		}
		for _, a := range labels {
			for _, b := range labels {
				if a != b && strings.HasPrefix(b, a) {
					t.Fatalf("n=%d: label %q is a prefix of %q", n, a, b)
				}
			}
		}
	}
}

// alt+s enters flash mode and labels a jump action on every row except the one
// the cursor is already on (where a jump would be a no-op).
func TestEnterFlashLabelsEveryOtherRow(t *testing.T) {
	m := newTestModel(80, "alpha", "beta", "gamma")
	m.cursor = 0
	m.enterFlash()
	if m.mode != modeFlash {
		t.Fatalf("mode = %d, want modeFlash", m.mode)
	}
	jumps := 0
	for _, tg := range m.flashTargets {
		if tg.kind == flashJump {
			jumps++
			if tg.row == m.cursor {
				t.Fatalf("jump labelled on the cursor row %d", tg.row)
			}
		}
	}
	if jumps != 2 {
		t.Fatalf("jump targets = %d, want 2", jumps)
	}
}

// Typing a complete label fires its action: a jump moves the cursor onto the row
// and leaves flash mode.
func TestFlashJumpFires(t *testing.T) {
	m := newTestModel(80, "alpha", "beta", "gamma")
	m.cursor = 0
	m.enterFlash()
	// find the label that jumps to row 2
	var label string
	for _, tg := range m.flashTargets {
		if tg.kind == flashJump && tg.row == 2 {
			label = tg.label
		}
	}
	if label == "" {
		t.Fatal("no jump target for row 2")
	}
	for _, r := range label {
		m.handleFlashKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	if m.mode != modeOutline {
		t.Fatalf("mode = %d, want modeOutline after firing", m.mode)
	}
	if m.cursor != 2 {
		t.Fatalf("cursor = %d, want 2", m.cursor)
	}
}

// A bash node offers run and expand actions on top of jump, straight from the
// registry — no per-type wiring in flash.
func TestFlashOffersBashActions(t *testing.T) {
	m := newTestModel(80, "note", "echo hi")
	m.tree.root.children[1].typ = database.TypeBash
	m.refreshRows()
	m.cursor = 0
	m.enterFlash()
	var run, expand bool
	for _, tg := range m.flashTargets {
		if tg.row != 1 {
			continue
		}
		switch tg.kind {
		case flashRun:
			run = true
		case flashExpand:
			expand = true
		}
	}
	if !run || !expand {
		t.Fatalf("bash actions: run=%v expand=%v, want both true", run, expand)
	}
}

// Esc cancels flash mode without moving the cursor.
func TestFlashEscCancels(t *testing.T) {
	m := newTestModel(80, "alpha", "beta")
	m.cursor = 1
	m.enterFlash()
	m.handleFlashKey(tea.KeyMsg{Type: tea.KeyEsc})
	if m.mode != modeOutline {
		t.Fatalf("mode = %d, want modeOutline", m.mode)
	}
	if m.cursor != 1 {
		t.Fatalf("cursor moved to %d, want 1", m.cursor)
	}
	if m.flashTargets != nil {
		t.Fatal("flashTargets not cleared")
	}
}

// Typing the first letter of a two-letter label narrows rather than fires:
// flashInput advances and the chip rendering grays the matched prefix.
func TestFlashNarrowsOnPartialLabel(t *testing.T) {
	// 30 rows forces some two-letter labels.
	names := make([]string, 30)
	for i := range names {
		names[i] = "row"
	}
	m := newTestModel(80, names...)
	m.cursor = 0
	m.enterFlash()
	var two string
	for _, tg := range m.flashTargets {
		if len(tg.label) == 2 {
			two = tg.label
			break
		}
	}
	if two == "" {
		t.Fatal("expected at least one two-letter label among 30 rows")
	}
	first := string(two[0])
	m.handleFlashKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(first)})
	if m.mode != modeFlash {
		t.Fatalf("mode = %d, want still modeFlash after one letter", m.mode)
	}
	if m.flashInput != first {
		t.Fatalf("flashInput = %q, want %q", m.flashInput, first)
	}
}
