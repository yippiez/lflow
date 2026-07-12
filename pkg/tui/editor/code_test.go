package editor

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/lflow/lflow/pkg/tui/database"
)

// focusCode puts the cursor on a code node and enters its editor (as alt+e /
// the /type auto-focus do), returning the node.
func focusCode(t *testing.T, m *Model, uuid string) *item {
	t.Helper()
	cursorOn(m, uuid)
	it := m.tree.byUUID[uuid]
	if !(codeView{}).Enter(m, it) {
		t.Fatal("code view refused focus")
	}
	m.focused = true
	return it
}

// typeInto feeds runes/space/enter to the model as the focused view sees them.
func typeInto(m *Model, s string) {
	for _, r := range s {
		switch r {
		case '\n':
			m.feed(tea.KeyMsg{Type: tea.KeyEnter})
		case ' ':
			m.feed(tea.KeyMsg{Type: tea.KeySpace})
		default:
			m.feed(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		}
	}
}

// TestCodeViewTypesMultiLine: the code editor keeps newlines and leading/nested
// space indentation as literal text — indentation is NOT read as the exit
// gesture, which is what broke naive multi-line code entry.
func TestCodeViewTypesMultiLine(t *testing.T) {
	m, _ := dbModel(t, database.Node{UUID: "c", Name: "", Type: database.TypeCode})
	focusCode(t, m, "c")

	typeInto(m, "def f(xs):\n")
	typeInto(m, "  total = 0\n")   // two-space indent, must stay literal
	typeInto(m, "    return total") // deeper indent, still literal

	if !m.focused {
		t.Fatal("indentation must not exit the editor")
	}
	codeView{}.Leave(m, m.tree.byUUID["c"])
	want := "def f(xs):\n  total = 0\n    return total"
	if got := m.tree.byUUID["c"].name; got != want {
		t.Fatalf("code =\n%q\nwant\n%q", got, want)
	}
}

// TestCodeViewDoubleSpaceExits: two spaces right after a content char at the end
// exit to a fresh sibling, trimming the pending pair — the "done editing" gesture.
func TestCodeViewDoubleSpaceExits(t *testing.T) {
	m, _ := dbModel(t, database.Node{UUID: "c", Name: "", Type: database.TypeCode})
	focusCode(t, m, "c")

	typeInto(m, "print(1)")
	before := len(m.tree.root.children)
	typeInto(m, "  ") // content + double space → exit

	if m.focused {
		t.Fatal("double space after content must leave the editor")
	}
	if got := m.tree.byUUID["c"].name; got != "print(1)" {
		t.Fatalf("the pending spaces must be trimmed, code = %q", got)
	}
	if len(m.tree.root.children) != before+1 {
		t.Fatalf("a fresh sibling must appear, children %d → %d", before, len(m.tree.root.children))
	}
	if cur := m.cursorItem(); cur == m.tree.byUUID["c"] || cur.name != "" {
		t.Fatalf("cursor should land on the new empty sibling, got %#v", cur)
	}
}

// TestCodeViewTabIndents: Tab inserts two spaces (never a real tab), so indent
// depth stays consistent with the space-based indentation the block shows.
func TestCodeViewTabIndents(t *testing.T) {
	m, _ := dbModel(t, database.Node{UUID: "c", Name: "", Type: database.TypeCode})
	focusCode(t, m, "c")
	m.feed(tea.KeyMsg{Type: tea.KeyTab})
	typeInto(m, "x")
	codeView{}.Leave(m, m.tree.byUUID["c"])
	if got := m.tree.byUUID["c"].name; got != "  x" {
		t.Fatalf("tab should indent two spaces, code = %q", got)
	}
}

// TestHLCodeLine: the shared highlighter dims comments and colors keywords.
func TestHLCodeLine(t *testing.T) {
	if got := HLCodeLine("# a comment"); !strings.HasPrefix(got, cDim) {
		t.Fatalf("comment must dim: %q", got)
	}
	if got := HLCodeLine("def train(x):"); !strings.Contains(got, cAccent+"def") {
		t.Fatalf("keyword must color: %q", got)
	}
}

// TestCodeInlineRow: a multi-line code node's one-line row is the dim line-count
// tag, and the always-on band renders the gray block beneath it.
func TestCodeInlineRow(t *testing.T) {
	m, _ := dbModel(t, database.Node{UUID: "c", Name: "a\nb\nc", Type: database.TypeCode})
	cursorOn(m, "c")
	row := renderBody(m.tree.byUUID["c"], "a\nb\nc", -1, false, nil, false)
	if got := stripSGR(row); got != "code · 3 lines" {
		t.Fatalf("code row = %q", got)
	}
	bands := m.codeBands(m.rows[m.cursor], false, 80)
	if len(bands) != 5 { // ┌ header, 3 lines, └ footer
		t.Fatalf("bands = %d, want 5", len(bands))
	}
	if !strings.Contains(bands[0], "code") {
		t.Fatalf("header band missing label: %q", bands[0])
	}
}
