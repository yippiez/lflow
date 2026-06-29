package editor

import (
	"testing"

	"github.com/lflow/lflow/pkg/tui/database"
)

func cmdChipOf(m *Model) (database.Chip, bool) {
	for _, c := range m.chips {
		if c.Kind == chipKindCmd {
			return c, true
		}
	}
	return database.Chip{}, false
}

// TestCmdChipCreateViaDoubleSpace: typing "$ls -la" then a DOUBLE space commits an
// inline cmd chip whose value is the command (single spaces stay in the command).
func TestCmdChipCreateViaDoubleSpace(t *testing.T) {
	m, _ := dbModel(t, database.Node{UUID: "edit", Name: ""})
	cursorOn(m, "edit")
	m.caret = 0

	m.press("$ls -la")
	m.press(" ") // first space: stays part of the command
	if _, ok := cmdChipOf(m); ok {
		t.Fatal("a single space must not commit the cmd chip")
	}
	m.press(" ") // second space: commits the chip

	c, ok := cmdChipOf(m)
	if !ok {
		t.Fatal("double space did not create a cmd chip")
	}
	if c.Value != "ls -la" {
		t.Errorf("cmd value = %q, want %q", c.Value, "ls -la")
	}
	edit := m.tree.byUUID["edit"]
	if !hasAnchor(edit.name) {
		t.Fatalf("node has no chip anchor: %q", edit.name)
	}
	if got := displayAnchors(edit.name, m.chips); got != "$ls -la " {
		t.Errorf("rendered = %q, want %q", got, "$ls -la ")
	}
}

// TestCmdChipMidSentence: a "$cmd" token dropped mid-text (preceded by a space)
// converts, leaving the surrounding prose intact.
func TestCmdChipMidSentence(t *testing.T) {
	m, _ := dbModel(t, database.Node{UUID: "edit", Name: "run "})
	cursorOn(m, "edit")
	m.caret = len([]rune("run "))

	m.press("$echo hi")
	m.press(" ")
	m.press(" ")

	c, ok := cmdChipOf(m)
	if !ok {
		t.Fatal("no cmd chip created")
	}
	if c.Value != "echo hi" {
		t.Errorf("cmd value = %q, want %q", c.Value, "echo hi")
	}
	if got := displayAnchors(m.tree.byUUID["edit"].name, m.chips); got != "run $echo hi " {
		t.Errorf("rendered = %q, want %q", got, "run $echo hi ")
	}
}

// TestCmdChipPreviewIsEphemeral: a run preview lands in the in-memory chip label
// (shown as "$cmd → preview") but is never written to the chips table — the
// command persists, the output does not.
func TestCmdChipPreviewIsEphemeral(t *testing.T) {
	m, db := dbModel(t, database.Node{UUID: "edit", Name: ""})
	cursorOn(m, "edit")
	m.caret = 0
	m.press("$ls")
	m.press(" ")
	m.press(" ")

	c, ok := cmdChipOf(m)
	if !ok {
		t.Fatal("no cmd chip created")
	}
	m.runOut = map[string][]outLine{c.ID: {{text: "file-a"}, {text: "file-b"}}}
	m.setCmdPreview(c.ID)

	if got := chipDisplay(m.chips[c.ID]); got != "$ls → file-a" {
		t.Errorf("chip display = %q, want %q", got, "$ls → file-a")
	}
	// the persisted chip row must keep an empty label (output never persisted)
	stored, err := database.GetChip(db, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Label != "" {
		t.Errorf("persisted label = %q, want empty (preview must stay ephemeral)", stored.Label)
	}
}

// TestCmdChipNotInBashNode: inside a bash node the whole node is the command, so
// "$" stays literal and no inline cmd chip forms.
func TestCmdChipNotInBashNode(t *testing.T) {
	m, _ := dbModel(t, database.Node{UUID: "edit", Name: "", Type: database.TypeBash})
	cursorOn(m, "edit")
	m.caret = 0
	m.press("$ls -la")
	m.press(" ")
	m.press(" ")
	if _, ok := cmdChipOf(m); ok {
		t.Fatal("a bash node must not form an inline cmd chip")
	}
}
