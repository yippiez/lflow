package editor

import (
	"strings"
	"testing"

	"github.com/lflow/lflow/tui/database"
)

// TestEmptyNodeRendersBlankRow: an empty node is a divider stripped of its
// rule — the row shows nothing but the connector rail, no glyph, no text, no
// rule; under the cursor a single red · marks the otherwise blank line.
func TestEmptyNodeRendersBlankRow(t *testing.T) {
	m := newTestModel(40, "above", "gap", "below")
	m.tree.root.children[1].typ = database.TypeEmpty
	m.refreshRows()

	lines := m.viewOutline(m.width - 1)
	if len(lines) < 3 {
		t.Fatalf("want at least 3 rows, got %d: %q", len(lines), lines)
	}
	row := lines[1]
	plain := strings.TrimSpace(stripSGR(row))
	if plain != "" && plain != "│" {
		t.Errorf("unhovered empty node should render a blank row, got %q", row)
	}
	if strings.Contains(row, "─") {
		t.Errorf("empty node must not render a divider rule: %q", row)
	}

	// move the cursor onto it: the only mark is a single red ·
	m.cursor = 1
	row = m.viewOutline(m.width - 1)[1]
	if !strings.Contains(row, cRed+"·") {
		t.Errorf("hovered empty node should show a red · cue: %q", row)
	}
	if got := strings.TrimSpace(strings.TrimSuffix(stripSGR(row), "·")); got != "" && got != "│" {
		t.Errorf("hovered empty node should show nothing but the · cue, got %q", row)
	}
}

// TestEmptyNodeInTypePicker: /type offers the Empty type.
func TestEmptyNodeInTypePicker(t *testing.T) {
	m := newTestModel(40, "x")
	found := false
	for _, k := range m.filteredTypes("empty") {
		if k == database.TypeEmpty {
			found = true
		}
	}
	if !found {
		t.Errorf("the /type picker should offer Empty, got %v", m.filteredTypes("empty"))
	}
}
