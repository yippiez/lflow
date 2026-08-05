package database

import "testing"

// TestUpsertRevivesTombstonedNode pins the undo crash: a node that was created,
// saved, deleted (tombstoned in the DB, its snapshot dropped) and then restored by
// undo is re-saved as if brand new. A plain INSERT crashed on UNIQUE(uuid); Upsert
// must instead revive the row with the restored content and deleted = 0.
func TestUpsertRevivesTombstonedNode(t *testing.T) {
	db := InitTestMemoryDB(t)

	n := Node{UUID: "v1", Name: "voice memo", Type: "voice", AddedOn: 1, EditedOn: 1}
	if err := n.Insert(db); err != nil {
		t.Fatalf("initial insert: %v", err)
	}
	// a saved delete tombstones the row
	MustExec(t, "tombstone", db, "UPDATE nodes SET deleted = 1 WHERE uuid = ?", "v1")

	// undo restores it; the editor believes it is new and Upserts — must not crash
	revived := Node{UUID: "v1", Name: "voice memo back", Type: "voice", AddedOn: 99, EditedOn: 2}
	if err := revived.Upsert(db); err != nil {
		t.Fatalf("upsert of a revived node must not crash: %v", err)
	}

	got, err := GetNode(db, "v1")
	if err != nil {
		t.Fatalf("get revived node: %v", err)
	}
	if got.Deleted {
		t.Error("revived node must have deleted = 0")
	}
	if got.Name != "voice memo back" {
		t.Errorf("revived node should carry the restored content, got %q", got.Name)
	}
	if got.AddedOn != 1 {
		t.Errorf("revive should preserve the original added_on, got %d", got.AddedOn)
	}
}

// TestUpsertInsertsFreshNode: with no existing row, Upsert behaves like Insert.
func TestUpsertInsertsFreshNode(t *testing.T) {
	db := InitTestMemoryDB(t)
	n := Node{UUID: "n1", Name: "fresh", Type: "bullets", AddedOn: 1, EditedOn: 1}
	if err := n.Upsert(db); err != nil {
		t.Fatalf("upsert fresh: %v", err)
	}
	got, err := GetNode(db, "n1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Name != "fresh" || got.Deleted {
		t.Errorf("unexpected node: %+v", got)
	}
}
