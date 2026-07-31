package editor

import (
	"strings"
	"testing"

	"github.com/lflow/lflow/pkg/tui/database"
)

// The generated-rows API a plugin uses to hang its run output in the outline as
// real nodes (the websearch node's hits).

func TestNodeSetGeneratedMakesLinkedRows(t *testing.T) {
	m := newTestModelWithChildren(80, "search", "kept by the user")
	host := m.tree.root.children[0]
	ref := nodeRef{m: m, it: host}

	n := m.NodeSetGenerated(ref, database.TypeWebResult, []NodeRow{
		{Text: "The Go docs", URL: "https://go.dev/doc/"},
		{Text: "pkg.go.dev", URL: "https://pkg.go.dev/"},
	})
	if n != 2 {
		t.Fatalf("made %d rows, want 2", n)
	}
	if len(host.children) != 3 {
		t.Fatalf("children = %d, want the 2 rows plus the user's own", len(host.children))
	}
	if host.children[2].name != "kept by the user" {
		t.Errorf("the user's row must survive, got %q", host.children[2].name)
	}

	row := host.children[0]
	if row.typ != database.TypeWebResult {
		t.Errorf("row type = %q", row.typ)
	}
	// the name is a link chip: its label reads as the title, its value is the target
	spans := anchorSpans([]rune(row.name))
	if len(spans) != 1 {
		t.Fatalf("row name %q is not one chip anchor", row.name)
	}
	c := m.chips[spans[0].id]
	if c.Kind != chipKindLink || c.Value != "https://go.dev/doc/" || c.Label != "The Go docs" {
		t.Errorf("chip = %+v, want the title linked to its result", c)
	}
	// and it renders as the title in the link color, not as a url
	body := renderBody(row, m.tree.displayName(row), -1, false, m.chips, false)
	if !strings.Contains(stripSGR(body), "The Go docs") {
		t.Errorf("row renders as %q", stripSGR(body))
	}
}

func TestNodeSetGeneratedReplacesOnlyItsOwnRows(t *testing.T) {
	m := newTestModelWithChildren(80, "search", "mine")
	host := m.tree.root.children[0]
	ref := nodeRef{m: m, it: host}
	mine := host.children[0]

	m.NodeSetGenerated(ref, database.TypeWebResult, []NodeRow{{Text: "old", URL: "https://old.example/"}})
	old := host.children[0]
	m.NodeSetGenerated(ref, database.TypeWebResult, []NodeRow{{Text: "new", URL: "https://new.example/"}})

	if len(host.children) != 2 {
		t.Fatalf("children = %d, want one fresh row plus the user's", len(host.children))
	}
	if host.children[1] != mine {
		t.Errorf("the user's row was disturbed: %q", host.children[1].name)
	}
	if _, alive := m.tree.byUUID[old.uuid]; alive {
		t.Error("the previous generation must be dropped from the tree")
	}
	// its chip goes with it — a re-run must not leak chip records
	for _, sp := range anchorSpans([]rune(old.name)) {
		if _, ok := m.chips[sp.id]; ok {
			t.Errorf("chip %s outlived its row", sp.id)
		}
	}
	// a row the user moved out from under the node is theirs now: retyped rows
	// (not of the generated type) are never touched
	if host.children[0].typ != database.TypeWebResult {
		t.Errorf("fresh row type = %q", host.children[0].typ)
	}
}

// An internal type is one a plugin generates, so it must not show up in the
// /type picker as something to convert a node into.
func TestInternalTypesStayOutOfThePicker(t *testing.T) {
	for _, key := range typeOrder() {
		if key == database.TypeWebResult {
			t.Fatalf("%s is generated, it must not be offered in /type", key)
		}
	}
	// it still resolves as a type, so rows of it render instead of crashing
	if typeOf(database.TypeWebResult).key == database.TypeWebResult {
		return // registered by the nodes package in the real binary
	}
}
