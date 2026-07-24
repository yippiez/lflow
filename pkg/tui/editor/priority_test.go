package editor

import (
	"testing"

	"github.com/lflow/lflow/pkg/tui/database"
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
