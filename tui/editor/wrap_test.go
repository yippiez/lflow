package editor

import (
	"strings"
	"testing"
)

const longName = "a very long node that keeps going and going past the right edge of the terminal"

// TestNoWrapRowIsOneLine is the point of /wrap:toggle: a node longer than the
// row wraps onto continuation lines by default and occupies exactly one line
// once it carries the flag.
func TestNoWrapRowIsOneLine(t *testing.T) {
	m := newTestModel(40, longName)
	m.focused = true

	group, _ := m.renderRow(m.tree, m.rows[0], rowOpts{maxLine: 39})
	if len(group) < 2 {
		t.Fatalf("a wrapping row of %d runes rendered %d line(s), want several", len([]rune(longName)), len(group))
	}

	m.rows[0].it.style = "nowrap"
	group, _ = m.renderRow(m.tree, m.rows[0], rowOpts{maxLine: 39})
	if len(group) != 1 {
		t.Fatalf("a truncating row rendered %d lines, want 1:\n%s", len(group), strings.Join(group, "\n"))
	}
	if w := visibleWidth(group[0]); w > 39 {
		t.Fatalf("truncated line is %d columns wide, want <= 39: %q", w, stripSGR(group[0]))
	}
	if !strings.HasSuffix(stripSGR(group[0]), "…") {
		t.Fatalf("truncated line does not mark the cut: %q", stripSGR(group[0]))
	}
}

// TestNoWrapRowScrollsToCaret is the editing half: the window follows the caret
// past the right edge, and comes back to the left edge when the caret does.
func TestNoWrapRowScrollsToCaret(t *testing.T) {
	m := newTestModel(40, longName)
	m.focused = true
	m.rows[0].it.style = "nowrap"

	m.caret = len([]rune(longName))
	group, _ := m.renderRow(m.tree, m.rows[0], rowOpts{maxLine: 39, selected: true, interactive: true})
	line := stripSGR(group[0])
	if len(group) != 1 {
		t.Fatalf("scrolled row rendered %d lines, want 1", len(group))
	}
	if !strings.Contains(line, "terminal") {
		t.Fatalf("the window did not follow the caret to the end of the text: %q", line)
	}
	if !strings.HasPrefix(strings.TrimLeft(line, " "), "○ …") {
		t.Fatalf("the rail and glyph did not hold their columns: %q", line)
	}
	if m.hscrollCol == 0 {
		t.Fatal("caret past the right edge left the window at column 0")
	}

	m.caret = 0
	group, _ = m.renderRow(m.tree, m.rows[0], rowOpts{maxLine: 39, selected: true, interactive: true})
	if line = stripSGR(group[0]); !strings.Contains(line, "a very long node") {
		t.Fatalf("the window did not come back with the caret: %q", line)
	}
}

// TestNoWrapRowIsOneVisualLine keeps the caret helpers honest: a truncating row
// is ONE line to the keyboard, so ↑/↓ cross to the neighbouring node instead of
// walking wrap points that are no longer rendered.
func TestNoWrapRowIsOneVisualLine(t *testing.T) {
	m := newTestModel(40, longName, "second")
	if got := len(m.selectedVisualRows()); got < 2 {
		t.Fatalf("a wrapping row has %d visual lines, want several", got)
	}
	m.rows[0].it.style = "nowrap"
	if got := m.selectedVisualRows(); len(got) != 1 || got[0] != 0 {
		t.Fatalf("selectedVisualRows on a truncating row = %v, want [0]", got)
	}
}

// TestWrapToggleAppliesToSelection is the "select → line wrap" path: /wrap:toggle
// with a row selection live turns every selected node at once, like /style.
func TestWrapToggleAppliesToSelection(t *testing.T) {
	m := newTestModel(40, "one", "two", "three")
	m.selOn, m.selAnchor, m.cursor = true, 0, 2
	m.runSlash("/wrap:toggle")
	for i, it := range m.rows {
		if !styleNoWrap(it.it.style) {
			t.Fatalf("row %d (%q) did not take the flag: style = %q", i, it.it.name, it.it.style)
		}
	}
	m.runSlash("/wrap:toggle")
	for i, it := range m.rows {
		if styleNoWrap(it.it.style) {
			t.Fatalf("row %d (%q) kept the flag through the second toggle", i, it.it.name)
		}
	}
}

// TestWindowLineKeepsStyleOpen: a window that opens inside a colored run has to
// reopen it, or the visible half of the span renders unstyled.
func TestWindowLineKeepsStyleOpen(t *testing.T) {
	body := cRed + "red text that runs on for a while" + cReset
	got := windowLine(body, 10, 20)
	if !strings.Contains(got, cRed) {
		t.Fatalf("window dropped the open color: %q", got)
	}
	if plain := stripSGR(got); !strings.HasPrefix(plain, "…") {
		t.Fatalf("window did not mark the left cut: %q", plain)
	}
	if w := visibleWidth(got); w > 20 {
		t.Fatalf("window is %d columns wide, want <= 20", w)
	}
}
