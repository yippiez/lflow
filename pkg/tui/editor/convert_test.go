package editor

import (
	"testing"

	"github.com/lflow/lflow/pkg/tui/database"
)

// No keyboard sign converts a node's type: "$" is cmd-chip territory (double
// space commits the chip — see cmdchip_test.go) and a bare "$ " stays literal
// text; bash and every other type are set via /type only.
func TestDollarSignStaysText(t *testing.T) {
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
	if _, ok := cmdChipOf(m); ok {
		t.Fatal("an empty '$' must not commit a cmd chip")
	}
	if edit.name != "$  " {
		t.Fatalf("name = %q, want literal %q", edit.name, "$  ")
	}
}
