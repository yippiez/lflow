package database

import (
	"testing"

	"github.com/lflow/lflow/pkg/utils/assert"
)

func mustInsert(t *testing.T, db *DB, n Node) {
	t.Helper()
	if n.Type == "" {
		n.Type = TypeBullets
	}
	if err := n.Insert(db); err != nil {
		t.Fatalf("inserting node %s: %v", n.UUID, err)
	}
}

func seedTree(t *testing.T, db *DB) {
	t.Helper()
	mustInsert(t, db, Node{UUID: "r1", Name: "experiment results", Type: TypeH1, Rank: 0})
	mustInsert(t, db, Node{UUID: "c1", ParentUUID: "r1", Name: "baseline numbers", Rank: 0})
	mustInsert(t, db, Node{UUID: "g1", ParentUUID: "c1", Name: "parse: 1.42s", Rank: 0})
	mustInsert(t, db, Node{UUID: "c2", ParentUUID: "r1", Name: "attempt 2", Rank: 1})
	mustInsert(t, db, Node{UUID: "r2", Name: "reading list", Rank: 1})
}

func TestNodeCRUD(t *testing.T) {
	db := InitTestMemoryDB(t)
	defer db.Close()

	n := Node{UUID: "n1", Name: "hello", Note: "world", Type: TypeTodo, AddedOn: 1, EditedOn: 2}
	mustInsert(t, db, n)

	got, err := GetNode(db, "n1")
	if err != nil {
		t.Fatal(err)
	}
	assert.Equal(t, got.Name, "hello", "name mismatch")
	assert.Equal(t, got.Note, "world", "note mismatch")
	assert.Equal(t, got.Type, TypeTodo, "type mismatch")

	got.Name = "updated"
	got.CompletedAt = 99
	if err := got.Update(db); err != nil {
		t.Fatal(err)
	}
	got2, err := GetNode(db, "n1")
	if err != nil {
		t.Fatal(err)
	}
	assert.Equal(t, got2.Name, "updated", "updated name mismatch")
	assert.Equal(t, got2.CompletedAt, int64(99), "completed_at mismatch")

	if err := got2.Expunge(db); err != nil {
		t.Fatal(err)
	}
	if _, err := GetNode(db, "n1"); err == nil {
		t.Fatal("expunged node should not be found")
	}
}

func TestUpdateUUIDRewritesReferences(t *testing.T) {
	db := InitTestMemoryDB(t)
	defer db.Close()

	seedTree(t, db)
	mustInsert(t, db, Node{UUID: "m1", Name: "mirror", MirrorOf: "c1", Rank: 2})

	n, err := GetNode(db, "c1")
	if err != nil {
		t.Fatal(err)
	}
	if err := n.UpdateUUID(db, "c1-new"); err != nil {
		t.Fatal(err)
	}

	child, err := GetNode(db, "g1")
	if err != nil {
		t.Fatal(err)
	}
	assert.Equal(t, child.ParentUUID, "c1-new", "child parent_uuid should be rewritten")

	mirror, err := GetNode(db, "m1")
	if err != nil {
		t.Fatal(err)
	}
	assert.Equal(t, mirror.MirrorOf, "c1-new", "mirror_of should be rewritten")
}

func TestGetChildrenAndSubtree(t *testing.T) {
	db := InitTestMemoryDB(t)
	defer db.Close()

	seedTree(t, db)

	roots, err := GetChildren(db, "")
	if err != nil {
		t.Fatal(err)
	}
	assert.Equal(t, len(roots), 2, "root count mismatch")
	assert.Equal(t, roots[0].UUID, "r1", "roots should be rank-ordered")

	subtree, err := GetSubtree(db, "r1")
	if err != nil {
		t.Fatal(err)
	}
	assert.Equal(t, len(subtree), 4, "subtree size mismatch")
	// depth-first: r1, c1, g1, c2
	assert.Equal(t, subtree[1].UUID, "c1", "subtree order mismatch")
	assert.Equal(t, subtree[2].UUID, "g1", "subtree order mismatch")
	assert.Equal(t, subtree[3].UUID, "c2", "subtree order mismatch")
}

func TestMarkSubtreeDeleted(t *testing.T) {
	db := InitTestMemoryDB(t)
	defer db.Close()

	seedTree(t, db)

	count, err := MarkSubtreeDeleted(db, "c1")
	if err != nil {
		t.Fatal(err)
	}
	assert.Equal(t, count, 2, "deleted count mismatch")

	var deleted bool
	MustScan(t, "checking g1", db.QueryRow("SELECT deleted FROM nodes WHERE uuid = 'g1'"), &deleted)
	assert.Equal(t, deleted, true, "descendant should be tombstoned")

	// deleted nodes disappear from subtree walks
	subtree, err := GetSubtree(db, "r1")
	if err != nil {
		t.Fatal(err)
	}
	assert.Equal(t, len(subtree), 2, "tombstoned nodes should be excluded")
}

func TestSearchNodes(t *testing.T) {
	db := InitTestMemoryDB(t)
	defer db.Close()

	seedTree(t, db)
	mustInsert(t, db, Node{UUID: "done1", Name: "experiment archive", Rank: 5, CompletedAt: 10})

	// exact name beats substring
	got, err := SearchNodes(db, "experiment results", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) == 0 {
		t.Fatal("no results")
	}
	assert.Equal(t, got[0].UUID, "r1", "exact match should rank first")

	// substring finds it too
	got, err = SearchNodes(db, "experiment", false)
	if err != nil {
		t.Fatal(err)
	}
	assert.Equal(t, got[0].UUID, "r1", "best match mismatch")
	for _, n := range got {
		if n.UUID == "done1" {
			t.Fatal("completed node should be excluded by default")
		}
	}

	// includeCompleted brings it back
	got, err = SearchNodes(db, "experiment", true)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, n := range got {
		if n.UUID == "done1" {
			found = true
		}
	}
	assert.Equal(t, found, true, "completed node should be included with includeCompleted")

	// FTS word match inside the note field
	mustInsert(t, db, Node{UUID: "note1", Name: "misc", Note: "the flamingo password is azure", Rank: 6})
	got, err = SearchNodes(db, "flamingo", false)
	if err != nil {
		t.Fatal(err)
	}
	found = false
	for _, n := range got {
		if n.UUID == "note1" {
			found = true
		}
	}
	assert.Equal(t, found, true, "fts should match note content")
}

func TestNextRank(t *testing.T) {
	db := InitTestMemoryDB(t)
	defer db.Close()

	seedTree(t, db)

	rank, err := NextRank(db, "r1")
	if err != nil {
		t.Fatal(err)
	}
	assert.Equal(t, rank, 2, "next rank mismatch")

	rank, err = NextRank(db, "g1")
	if err != nil {
		t.Fatal(err)
	}
	assert.Equal(t, rank, 0, "next rank for leaf mismatch")
}

func TestFirstRank(t *testing.T) {
	db := InitTestMemoryDB(t)
	defer db.Close()

	seedTree(t, db)

	// r1's children are ranked 0 and 1, so a top insert sorts before them
	rank, err := FirstRank(db, "r1")
	if err != nil {
		t.Fatal(err)
	}
	assert.Equal(t, rank, -1, "first rank mismatch")

	rank, err = FirstRank(db, "g1")
	if err != nil {
		t.Fatal(err)
	}
	assert.Equal(t, rank, 0, "first rank for leaf mismatch")
}
