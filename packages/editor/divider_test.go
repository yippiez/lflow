package editor

import (
	"strings"
	"testing"

	"github.com/lflow/lflow/packages/database"
)

// TestDividerRendersFullWidthRule: a divider hides its glyph and renders a rule
// that stretches to the right edge — muted gray normally, red under the cursor.
func TestDividerRendersFullWidthRule(t *testing.T) {
	m := newTestModel(40, "above", "div", "below")
	m.tree.root.children[1].typ = database.TypeDivider
	m.refreshRows()

	out := strings.Join(m.viewOutline(m.width-1), "\n")
	lines := strings.Split(out, "\n")
	if len(lines) < 3 {
		t.Fatalf("want at least 3 rows, got %d: %q", len(lines), out)
	}
	div := lines[1]
	if strings.Contains(stripSGR(div), "○") {
		t.Errorf("divider must hide its circle glyph: %q", div)
	}
	if !strings.Contains(div, "─") {
		t.Errorf("divider should render a horizontal rule: %q", div)
	}
	// cursor starts on row 0, so the divider is unselected → muted gray, not red
	if strings.Contains(div, cRed) {
		t.Errorf("unhovered divider should be gray, not red: %q", div)
	}

	// move the cursor onto the divider: the rule turns red
	m.cursor = 1
	div = strings.Split(strings.Join(m.viewOutline(m.width-1), "\n"), "\n")[1]
	if !strings.Contains(div, cRed) {
		t.Errorf("hovered divider should be red: %q", div)
	}
}

// TestDividerLineCentered: the rule is ~80% of the available width and centered,
// with roughly equal gaps on the left and right so it sits in the middle.
func TestDividerLineCentered(t *testing.T) {
	m := newTestModel(60, "x")
	r := m.rows[0]
	maxLine := m.width - 1

	line := stripSGR(dividerLine(r, maxLine, "", false))
	dashes := strings.Count(line, "─")
	leading := len(line) - len(strings.TrimLeft(line, " "))

	const prefixW = 1 // " " before the (empty) depth-0 connector
	avail := maxLine - prefixW
	if want := avail * 24 / 25; dashes != want {
		t.Errorf("rule width = %d, want %d (~96%% of %d)", dashes, want, avail)
	}
	leftGap := leading - prefixW
	rightGap := maxLine - leading - dashes
	if d := leftGap - rightGap; d < -1 || d > 1 {
		t.Errorf("rule not centered: leftGap=%d rightGap=%d", leftGap, rightGap)
	}
	if leftGap < 1 {
		t.Errorf("rule should hang short on the left too, leftGap=%d", leftGap)
	}
}

// TestDividerTextAtMidpoint: a divider with a name shows that text centered on
// the rule, with equal rule runs on both sides.
func TestDividerTextAtMidpoint(t *testing.T) {
	m := newTestModel(60, "x")
	m.tree.root.children[0].name = "Part Two"
	r := m.rows[0]
	maxLine := m.width - 1

	line := stripSGR(dividerLine(r, maxLine, "Part Two", false))
	left := strings.Index(line, "Part Two")
	right := left + len("Part Two")
	leftRule := strings.Count(line[:left], "─")
	rightRule := strings.Count(line[right:], "─")
	if leftRule < 1 || rightRule < 1 {
		t.Fatalf("text should sit between rule runs, got: %q", line)
	}
	if leftRule != rightRule {
		t.Errorf("rule runs should be equal to center the text: left=%d right=%d", leftRule, rightRule)
	}
	if line[left-1] != ' ' || line[right] != ' ' {
		t.Errorf("text should have one space of breathing room each side: %q", line)
	}
}

// TestDividerEmptyNameStaysRule: deleting a divider's text falls back to the
// plain full-width rule.
func TestDividerEmptyNameStaysRule(t *testing.T) {
	m := newTestModel(40, "above", "div", "below")
	m.tree.root.children[1].typ = database.TypeDivider
	m.refreshRows()

	out := strings.Join(m.viewOutline(m.width-1), "\n")
	lines := strings.Split(out, "\n")
	div := lines[1]
	if strings.Contains(stripSGR(div), "○") {
		t.Errorf("divider must hide its circle glyph: %q", div)
	}
	if !strings.Contains(div, "─") {
		t.Errorf("empty divider should render a horizontal rule: %q", div)
	}
}

// TestDividerTextRendersInline: a named divider in the outline shows its text on
// the rule, and the cursor sits on the correct rune inside the text.
func TestDividerTextRendersInline(t *testing.T) {
	m := newTestModel(60, "above", "div", "below")
	m.tree.root.children[1].typ = database.TypeDivider
	m.tree.root.children[1].name = "Part Two"
	m.refreshRows()
	m.cursor = 1
	m.caret = 5

	out := strings.Join(m.viewOutline(m.width-1), "\n")
	div := strings.Split(out, "\n")[1]
	plain := stripSGR(div)
	if !strings.Contains(plain, "Part Two") {
		t.Fatalf("divider text should render on the rule: %q", div)
	}
	if !strings.Contains(div, cInvert+"T") {
		t.Errorf("caret at 5 should invert the 'T' of 'Two': %q", div)
	}
}
