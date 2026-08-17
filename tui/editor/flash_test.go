package editor

import (
	"strings"
	"testing"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
)

func feedFlash(m *Model, s string) {
	for _, r := range s {
		m.handleFlashKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
}

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

// alt+s enters flash and waits for a search character — no labels until you type.
func TestEnterFlashWaitsForQuery(t *testing.T) {
	m := newTestModel(80, "alpha", "beta", "gamma")
	m.cursor = 0
	m.enterFlash()
	if m.mode != modeFlash {
		t.Fatalf("mode = %d, want modeFlash", m.mode)
	}
	if len(m.flashTargets) != 0 {
		t.Fatalf("targets = %d, want 0 before typing", len(m.flashTargets))
	}
}

// Typing a query highlights every visible match and assigns a jump label.
func TestFlashQueryLabelsMatches(t *testing.T) {
	m := newTestModel(80, "alpha", "beta", "gamma")
	m.cursor = 0
	m.enterFlash()
	feedFlash(m, "mm")
	if m.flashQuery != "mm" {
		t.Fatalf("flashQuery = %q, want mm", m.flashQuery)
	}
	if len(m.flashTargets) != 1 || m.flashTargets[0].row != 2 {
		t.Fatalf("targets = %+v, want only gamma", m.flashTargets)
	}
	if m.flashTargets[0].label == "" {
		t.Fatal("match has no label")
	}
}

// A key that does not extend the search fires the matching label and jumps
// the caret onto the hit.
func TestFlashLabelJumpsToMatch(t *testing.T) {
	m := newTestModel(80, "hello", "world", "foo")
	m.cursor = 0
	m.enterFlash()
	feedFlash(m, "w")
	var label string
	for _, tg := range m.flashTargets {
		if tg.row == 1 {
			label = tg.label
		}
	}
	if label == "" {
		t.Fatal("no match for world")
	}
	feedFlash(m, label)
	if m.mode != modeOutline {
		t.Fatalf("mode = %d, want modeOutline after jumping", m.mode)
	}
	if m.cursor != 1 {
		t.Fatalf("cursor = %d, want 1", m.cursor)
	}
	if m.caret != 0 {
		t.Fatalf("caret = %d, want 0 (start of \"world\")", m.caret)
	}
}

// A character that still occurs after the query extends the search instead of
// jumping, even if it would otherwise be a label letter.
func TestFlashPrefersExtendingTheQuery(t *testing.T) {
	m := newTestModel(80, "alpha", "alpine")
	m.cursor = 0
	m.enterFlash()
	feedFlash(m, "al")
	if m.mode != modeFlash {
		t.Fatalf("mode = %d, want still modeFlash after \"al\"", m.mode)
	}
	if m.flashQuery != "al" {
		t.Fatalf("flashQuery = %q, want al", m.flashQuery)
	}
	if len(m.flashTargets) != 2 {
		t.Fatalf("targets = %d, want 2", len(m.flashTargets))
	}
}

// Enter jumps to the nearest remaining match.
func TestFlashEnterJumpsNearest(t *testing.T) {
	m := newTestModel(80, "cat", "dog", "catalog")
	m.cursor = 0
	m.enterFlash()
	feedFlash(m, "c")
	m.handleFlashKey(tea.KeyMsg{Type: tea.KeyEnter})
	if m.mode != modeOutline {
		t.Fatalf("mode = %d, want modeOutline", m.mode)
	}
	if m.cursor != 0 {
		t.Fatalf("cursor = %d, want 0 (nearest \"c\")", m.cursor)
	}
}

// Esc cancels flash mode without moving the cursor.
func TestFlashEscCancels(t *testing.T) {
	m := newTestModel(80, "alpha", "beta")
	m.cursor = 1
	m.enterFlash()
	feedFlash(m, "a")
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

// An active match paints the typed span black-on-white and overlays the
// remaining label black-on-red without inserting cells.
func TestFlashStyleVisibleOverlaysLabel(t *testing.T) {
	got := flashStyleVisible("hello", []flashTarget{{start: 1, end: 4, label: "a"}}, "")
	plain := stripSGR(got)
	if utf8.RuneCountInString(plain) != 5 {
		t.Fatalf("overlay changed width: %q", plain)
	}
	if !strings.Contains(got, cBlack) || !strings.Contains(got, bgFlashHit) {
		t.Error("typed match must be black on white")
	}
	if !strings.Contains(got, bgFlashLabel) || !strings.Contains(got, "a") {
		t.Error("label must overlay black on red")
	}
	if strings.Contains(got, cRed) && !strings.Contains(got, bgFlashLabel) {
		t.Error("label must not use red foreground")
	}
	faded := flashStyleVisible("hello", []flashTarget{{start: 1, end: 4, label: "s"}}, "a")
	if strings.Contains(faded, bgFlashLabel) {
		t.Error("a non-matching label must not stay painted")
	}
}

// Two-letter labels still narrow on the first letter instead of firing.
func TestFlashNarrowsOnPartialLabel(t *testing.T) {
	names := make([]string, 30)
	for i := range names {
		names[i] = "xx"
	}
	m := newTestModel(80, names...)
	m.cursor = 0
	m.enterFlash()
	feedFlash(m, "x")
	var two string
	for _, tg := range m.flashTargets {
		if len(tg.label) == 2 {
			two = tg.label
			break
		}
	}
	if two == "" {
		t.Fatal("expected at least one two-letter label among 30 matches")
	}
	// the first letter of a two-letter label must not also extend "x" — "xx"
	// already is the whole name, so the next char is a label.
	first := string(two[0])
	m.handleFlashKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(first)})
	if m.mode != modeFlash {
		t.Fatalf("mode = %d, want still modeFlash after one letter", m.mode)
	}
	if m.flashInput != first {
		t.Fatalf("flashInput = %q, want %q", m.flashInput, first)
	}
}

// The live view grays the outline and overlays the hit / label in place.
func TestFlashHighlightsMatchInView(t *testing.T) {
	m := newTestModel(80, "hello", "world")
	m.enterFlash()
	feedFlash(m, "ell")
	out := m.View()
	if !strings.Contains(out, bgFlashHit) || !strings.Contains(out, cBlack) {
		t.Fatal("flash match must paint the typed span black on white")
	}
	if !strings.Contains(out, bgFlashLabel) {
		t.Fatal("flash match must overlay the remaining label black on red")
	}
}
