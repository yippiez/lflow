package editor

import (
	"strings"
	"testing"

	"github.com/lflow/lflow/tui/database"
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

// TestDividerLineCentered: the rule is ~96% of the available width and hangs
// left of center — its left gap is half of the right's, because the reserved
// glyph slot already contributes a node gap on the left.
func TestDividerLineCentered(t *testing.T) {
	m := newTestModel(60, "x")
	r := m.rows[0]
	maxLine := m.width - 1

	line := stripSGR(dividerLine(r, maxLine, "", false))
	dashes := strings.Count(line, "─")
	leading := len(line) - len(strings.TrimLeft(line, " "))

	const prefixW = 3 // " " before the (empty) depth-0 connector + blank glyph slot + separator
	avail := maxLine - prefixW
	if want := avail * 24 / 25; dashes != want {
		t.Errorf("rule width = %d, want %d (~96%% of %d)", dashes, want, avail)
	}
	leftGap := leading - prefixW
	rightGap := maxLine - leading - dashes
	if want := (avail - dashes) / 4; leftGap != want {
		t.Errorf("left gap = %d, want %d (half the centering)", leftGap, want)
	}
	if rightGap != avail-dashes-leftGap {
		t.Errorf("right gap = %d, want %d (left's half added here)", rightGap, avail-dashes-leftGap)
	}
	if rightGap <= leftGap {
		t.Errorf("rule should hang left: rightGap=%d leftGap=%d", rightGap, leftGap)
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

// TestDividerEmptySelectedNoCaretSlot: an empty divider under the cursor is
// still just a clean rule — the caret must not draw a slot that splits the rule
// into two runs around empty space.
func TestDividerEmptySelectedNoCaretSlot(t *testing.T) {
	m := newTestModel(60, "above", "div", "below")
	m.tree.root.children[1].typ = database.TypeDivider
	m.tree.root.children[1].name = ""
	m.refreshRows()
	m.cursor = 1
	m.caret = 0

	out := strings.Join(m.viewOutline(m.width-1), "\n")
	div := strings.Split(out, "\n")[1]
	plain := stripSGR(div)
	if strings.Count(plain, "─") == 0 {
		t.Fatalf("empty divider should still render a rule: %q", plain)
	}
	// the rule must be one unbroken run: any gap in the dashes means the caret
	// slot wedged into the middle
	if s := strings.Index(plain, "─"); s >= 0 {
		if strings.Contains(plain[s:], " ") {
			t.Errorf("empty selected divider must be one unbroken rule, got: %q", plain)
		}
	}
}

// TestDividerTextKeepsCenteredFootprint: a named divider spans the same ~96%
// centered footprint as an empty one — the text never stretches the rule out to
// 100% of the available width.
func TestDividerTextKeepsCenteredFootprint(t *testing.T) {
	m := newTestModel(60, "x")
	m.tree.root.children[0].name = "Part Two"
	r := m.rows[0]
	maxLine := m.width - 1

	named := visibleWidth(stripSGR(dividerLine(r, maxLine, "Part Two", false)))
	empty := visibleWidth(stripSGR(dividerLine(r, maxLine, "", false)))
	if named >= maxLine {
		t.Errorf("named divider spans %d of %d columns — should hang short", named, maxLine)
	}
	// the text eats into the rule, but the total footprint stays ~equal: the
	// named divider is not wider than the empty one
	if named > empty+2 {
		t.Errorf("named divider footprint %d exceeds empty %d", named, empty)
	}
}
