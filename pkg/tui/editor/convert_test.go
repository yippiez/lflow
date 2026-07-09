package editor

import (
	"testing"

	"github.com/lflow/lflow/pkg/tui/database"
)

// No keyboard sign converts a node's type: "$" is cmd-chip territory — double
// space commits the chip even when the command is still EMPTY (a blank $ chip
// to fill in); bash and every other type are set via /type only.
func TestDollarSignChipsEvenEmpty(t *testing.T) {
	m, _ := dbModel(t, database.Node{UUID: "edit", Name: ""})
	cursorOn(m, "edit")
	m.caret = 0

	m.press("$")
	m.press(" ")
	m.press(" ")

	edit := m.tree.byUUID["edit"]
	if edit.typ == database.TypeBash {
		t.Fatal("'$ ' must not convert the node to bash")
	}
	c, ok := cmdChipOf(m)
	if !ok {
		t.Fatal("a bare '$' + double space must commit an empty cmd chip")
	}
	if c.Value != "" {
		t.Fatalf("empty chip value = %q, want \"\"", c.Value)
	}
	if !hasAnchor(edit.name) {
		t.Fatalf("node must carry the chip anchor, got %q", edit.name)
	}
}
