package editor

import (
	"strings"
	"testing"

	"github.com/lflow/lflow/pkg/tui/database"
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

// TestDividerLineFillsWidth: the rule fills the line to the right edge.
func TestDividerLineFillsWidth(t *testing.T) {
	m := newTestModel(30, "x")
	r := m.rows[0]
	maxLine := m.width - 1
	got := visibleWidth(dividerLine(r, maxLine, false))
	if got != maxLine {
		t.Errorf("divider rule width = %d, want %d", got, maxLine)
	}
}
