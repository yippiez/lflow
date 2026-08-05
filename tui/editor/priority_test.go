package editor

import (
	"testing"

	"github.com/lflow/lflow/tui/database"
)

func TestReparentHonorsPriority(t *testing.T) {
	m := newTestModel(80, "target", "existing", "another")
	target := m.tree.root.children[0]
	existing := m.tree.root.children[1]
	another := m.tree.root.children[2]

	target.priority = database.PriorityUp
	if !m.tree.reparent(existing, target) || target.children[0] != existing {
		t.Fatal("priority up must land a moved node on top")
	}
	target.priority = database.PriorityDown
	if !m.tree.reparent(another, target) || target.children[len(target.children)-1] != another {
		t.Fatal("priority down must land a moved node at the bottom")
	}
}

func TestTabIndentAlwaysLandsAtBottom(t *testing.T) {
	m := newTestModel(80, "target", "existing")
	target := m.tree.root.children[0]
	existing := m.tree.root.children[1]
	target.priority = database.PriorityUp
	target.children = []*item{{uuid: "kid", name: "kid", parent: target}}

	if !m.tree.indent(existing) {
		t.Fatal("indent should succeed")
	}
	if got := target.children[len(target.children)-1]; got != existing {
		t.Fatalf("Tab must land the indented node at the bottom regardless of priority, got last child %#v", got)
	}
}

func TestGroupTabIndentAlwaysLandsAtBottom(t *testing.T) {
	m := newTestModel(80, "target", "b", "c")
	target := m.tree.root.children[0]
	b := m.tree.root.children[1]
	c := m.tree.root.children[2]
	target.priority = database.PriorityUp
	target.children = []*item{{uuid: "kid", name: "kid", parent: target}}
	m.refreshRows()

	m.cursor = m.rowIndexOf(b)
	m.press("shift+down") // select b..c
	m.press("tab")        // group-indent under target

	if b.parent != target || c.parent != target {
		t.Fatalf("b,c must indent under target (got %s, %s)", b.parent.uuid, c.parent.uuid)
	}
	got := names(target.children)
	want := []string{"kid", "b", "c"}
	for i := range want {
		if i >= len(got) || got[i] != want[i] {
			t.Fatalf("target order = %v, want %v", got, want)
		}
	}
}

func TestPrioritySlashCommand(t *testing.T) {
	db := database.InitTestMemoryDB(t)
	m := newTestModel(80, "target")
	m.db = db
	target := m.tree.root.children[0]
	target.uuid = "target"
	m.tree.byUUID[target.uuid] = target
	if err := (database.Node{UUID: target.uuid, Name: target.name, Type: database.TypeBullets}).Insert(db); err != nil {
		t.Fatal(err)
	}
	m.cursor = m.rowIndexOf(target)

	m.runSlash("/priority:up")
	if target.priority != database.PriorityUp {
		t.Fatalf("priority = %q, want up", target.priority)
	}
	got, err := database.GetNode(db, target.uuid)
	if err != nil || got.Priority != database.PriorityUp {
		t.Fatalf("persisted priority = %q, err=%v", got.Priority, err)
	}

	m.runSlash("/priority:down")
	if target.priority != database.PriorityDown {
		t.Fatalf("priority = %q, want down", target.priority)
	}
}
