package database

import "testing"

func TestArtifactCRUD(t *testing.T) {
	db := InitTestMemoryDB(t)

	a := Artifact{ID: "art1", Name: "page.html", Kind: "html", Content: "<h1>hi</h1>"}
	if err := UpsertArtifact(db, a); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	got, err := GetArtifact(db, "art1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got != a {
		t.Fatalf("got %+v, want %+v", got, a)
	}

	// overwrite
	a.Content = "<h1>bye</h1>"
	if err := UpsertArtifact(db, a); err != nil {
		t.Fatalf("re-upsert: %v", err)
	}
	if got, _ = GetArtifact(db, "art1"); got.Content != "<h1>bye</h1>" {
		t.Fatalf("overwrite failed: %q", got.Content)
	}

	if _, err := GetArtifact(db, "missing"); err == nil {
		t.Fatal("expected error for missing artifact")
	}

	if err := DeleteArtifact(db, "art1"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := GetArtifact(db, "art1"); err == nil {
		t.Fatal("expected error after delete")
	}
}

func TestGCArtifacts(t *testing.T) {
	db := InitTestMemoryDB(t)

	// keyed by a live artifact node's uuid → kept
	node := Node{UUID: "n1", Name: "page.html", Type: TypeArtifact}
	if err := node.Insert(db); err != nil {
		t.Fatal(err)
	}
	if err := UpsertArtifact(db, Artifact{ID: "n1", Name: "page.html", Kind: "html"}); err != nil {
		t.Fatal(err)
	}

	// referenced by a chip anchor in a live node name → kept
	chipNode := Node{UUID: "n2", Name: "see " + ChipAnchor("c1") + " here", Type: TypeBullets}
	if err := chipNode.Insert(db); err != nil {
		t.Fatal(err)
	}
	if err := UpsertArtifact(db, Artifact{ID: "c1", Name: "doc.md", Kind: "md"}); err != nil {
		t.Fatal(err)
	}

	// orphan → dropped
	if err := UpsertArtifact(db, Artifact{ID: "orphan", Name: "x.html", Kind: "html"}); err != nil {
		t.Fatal(err)
	}

	if err := GCArtifacts(db); err != nil {
		t.Fatalf("gc: %v", err)
	}

	for _, id := range []string{"n1", "c1"} {
		if _, err := GetArtifact(db, id); err != nil {
			t.Errorf("artifact %s was gc'd but should be kept: %v", id, err)
		}
	}
	if _, err := GetArtifact(db, "orphan"); err == nil {
		t.Error("orphan artifact should have been gc'd")
	}
}
