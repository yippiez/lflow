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

// chooseChipMenu opens the @ menu, filters to one kind, and selects it — the
// shared front half of every @-driven chip insertion in tests.
func chooseChipMenu(m *Model, kind string) {
	m.press("@")
	for _, s := range chipSpecs {
		if s.kind == kind {
			m.compl.query = s.menu
			break
		}
	}
	m.applyChipMenu(m.cursorItem(), m.complItems())
}

// TestCmdChipCreateViaAtMenu: @cmd opens the command input; spaces stay in the
// command and Enter splices an inline cmd chip whose value is the command.
func TestCmdChipCreateViaAtMenu(t *testing.T) {
	m, _ := dbModel(t, database.Node{UUID: "edit", Name: ""})
	cursorOn(m, "edit")
	m.caret = 0

	chooseChipMenu(m, chipKindCmd)
	m.press("ls -la") // a space stays part of the command, completer stays open
	if _, ok := cmdChipOf(m); ok {
		t.Fatal("the chip must not commit before Enter")
	}
	m.press("enter")

	c, ok := cmdChipOf(m)
	if !ok {
		t.Fatal("@cmd + enter did not create a cmd chip")
	}
	if c.Value != "ls -la" {
		t.Errorf("cmd value = %q, want %q", c.Value, "ls -la")
	}
	edit := m.tree.byUUID["edit"]
	if !hasAnchor(edit.name) {
		t.Fatalf("node has no chip anchor: %q", edit.name)
	}
	if got := displayAnchors(edit.name, m.chips); got != "$ ls -la" {
		t.Errorf("rendered = %q, want %q", got, "$ ls -la")
	}
}

// TestCmdChipMidSentence: @cmd dropped mid-text leaves the surrounding prose intact.
func TestCmdChipMidSentence(t *testing.T) {
	m, _ := dbModel(t, database.Node{UUID: "edit", Name: "run "})
	cursorOn(m, "edit")
	m.caret = len([]rune("run "))

	chooseChipMenu(m, chipKindCmd)
	m.press("echo hi")
	m.press("enter")

	c, ok := cmdChipOf(m)
	if !ok {
		t.Fatal("no cmd chip created")
	}
	if c.Value != "echo hi" {
		t.Errorf("cmd value = %q, want %q", c.Value, "echo hi")
	}
	if got := displayAnchors(m.tree.byUUID["edit"].name, m.chips); got != "run $ echo hi" {
		t.Errorf("rendered = %q, want %q", got, "run $ echo hi")
	}
}

// TestCmdChipPreviewIsEphemeral: a run preview lands in the in-memory chip label
// (shown as "$cmd → preview") but is never written to the chips table — the
// command persists, the output does not.
func TestCmdChipPreviewIsEphemeral(t *testing.T) {
	m, db := dbModel(t, database.Node{UUID: "edit", Name: ""})
	cursorOn(m, "edit")
	m.caret = 0
	chooseChipMenu(m, chipKindCmd)
	m.press("ls")
	m.press("enter")

	c, ok := cmdChipOf(m)
	if !ok {
		t.Fatal("no cmd chip created")
	}
	m.runOut = map[string][]outLine{c.ID: {{text: "file-a"}, {text: "file-b"}}}
	m.setCmdPreview(c.ID)

	if got := chipDisplay(m.chips[c.ID]); got != "$ ls → file-a" {
		t.Errorf("chip display = %q, want %q", got, "$ ls → file-a")
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
// the @ menu offers no cmd kind and "@" stays literal — no inline cmd chip forms.
func TestCmdChipNotInBashNode(t *testing.T) {
	if anyChipAllowed(database.TypeBash) {
		t.Fatal("the @ menu must offer nothing on a bash node")
	}
	m, _ := dbModel(t, database.Node{UUID: "edit", Name: "", Type: database.TypeBash})
	cursorOn(m, "edit")
	m.caret = 0
	m.press("@cmd")
	if _, ok := cmdChipOf(m); ok {
		t.Fatal("a bash node must not form an inline cmd chip")
	}
}
