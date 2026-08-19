package database

import (
	"testing"
)

// TestBacklinkNodes finds mirrors and [[ link chip referrers of a target, and
// never returns the target itself or deleted rows.
func TestBacklinkNodes(t *testing.T) {
	db := InitTestMemoryDB(t)

	mustInsert(t, db, Node{UUID: "src", Name: "source note", Type: TypeBullets, Rank: 0, AddedOn: 1, EditedOn: 1})
	mustInsert(t, db, Node{UUID: "mir", Name: "", MirrorOf: "src", ParentUUID: "src", Rank: 0, Type: TypeBullets, AddedOn: 2, EditedOn: 2})
	mustInsert(t, db, Node{UUID: "other", Name: "unrelated", Type: TypeBullets, Rank: 1, AddedOn: 3, EditedOn: 3})

	// a live link chip embedded in a node's name
	chipID := "chip-link-1"
	if err := UpsertChip(db, Chip{ID: chipID, Kind: "link", Value: nodeLinkScheme + "src", Label: "source"}); err != nil {
		t.Fatal(err)
	}
	mustInsert(t, db, Node{
		UUID: "linker", Name: "see " + ChipAnchor(chipID) + " later",
		Type: TypeBullets, Rank: 2, AddedOn: 4, EditedOn: 4,
	})

	// a deleted mirror must not surface
	mustInsert(t, db, Node{UUID: "dead-mir", Name: "", MirrorOf: "src", Rank: 3, Type: TypeBullets, Deleted: true, AddedOn: 5, EditedOn: 5})

	got, err := BacklinkNodes(db, "src")
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]bool{}
	for _, n := range got {
		ids[n.UUID] = true
	}
	if !ids["mir"] || !ids["linker"] {
		t.Fatalf("want mir + linker, got %v", ids)
	}
	if ids["src"] || ids["other"] || ids["dead-mir"] {
		t.Fatalf("must not include self/unrelated/deleted: %v", ids)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 backlinks, got %d: %v", len(got), ids)
	}
}

// TestMirrorNodes returns only mirrors of a target, excluding [[-link chip
// referrers and deleted rows.
func TestMirrorNodes(t *testing.T) {
	db := InitTestMemoryDB(t)

	mustInsert(t, db, Node{UUID: "src", Name: "source note", Type: TypeBullets, Rank: 0, AddedOn: 1, EditedOn: 1})
	mustInsert(t, db, Node{UUID: "mir", Name: "", MirrorOf: "src", ParentUUID: "src", Rank: 0, Type: TypeBullets, AddedOn: 2, EditedOn: 2})
	mustInsert(t, db, Node{UUID: "other", Name: "unrelated", Type: TypeBullets, Rank: 1, AddedOn: 3, EditedOn: 3})

	// a link chip referrer — must NOT appear in MirrorNodes
	chipID := "chip-link-1"
	if err := UpsertChip(db, Chip{ID: chipID, Kind: "link", Value: nodeLinkScheme + "src", Label: "source"}); err != nil {
		t.Fatal(err)
	}
	mustInsert(t, db, Node{
		UUID: "linker", Name: "see " + ChipAnchor(chipID) + " later",
		Type: TypeBullets, Rank: 2, AddedOn: 4, EditedOn: 4,
	})

	// a deleted mirror must not surface
	mustInsert(t, db, Node{UUID: "dead-mir", Name: "", MirrorOf: "src", Rank: 3, Type: TypeBullets, Deleted: true, AddedOn: 5, EditedOn: 5})

	got, err := MirrorNodes(db, "src")
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]bool{}
	for _, n := range got {
		ids[n.UUID] = true
	}
	if !ids["mir"] {
		t.Fatalf("want mir, got %v", ids)
	}
	if ids["src"] || ids["other"] || ids["linker"] || ids["dead-mir"] {
		t.Fatalf("must not include self/unrelated/linker/deleted: %v", ids)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 mirror, got %d: %v", len(got), ids)
	}
}

// TestMirrorCounts counts mirrors per source node.
func TestMirrorCounts(t *testing.T) {
	db := InitTestMemoryDB(t)

	mustInsert(t, db, Node{UUID: "src", Name: "source", Type: TypeBullets, Rank: 0, AddedOn: 1, EditedOn: 1})
	mustInsert(t, db, Node{UUID: "mir1", Name: "", MirrorOf: "src", ParentUUID: "src", Rank: 0, Type: TypeBullets, AddedOn: 2, EditedOn: 2})
	mustInsert(t, db, Node{UUID: "mir2", Name: "", MirrorOf: "src", ParentUUID: "src", Rank: 1, Type: TypeBullets, AddedOn: 3, EditedOn: 3})
	mustInsert(t, db, Node{UUID: "other", Name: "other", Type: TypeBullets, Rank: 2, AddedOn: 4, EditedOn: 4})

	counts, err := MirrorCounts(db)
	if err != nil {
		t.Fatal(err)
	}
	if counts["src"] != 2 {
		t.Fatalf("want 2 mirrors of src, got %d", counts["src"])
	}
	if counts["other"] != 0 {
		t.Fatalf("want 0 mirrors of other, got %d", counts["other"])
	}
}
