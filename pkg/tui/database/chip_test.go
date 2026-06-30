package database

import "testing"

// A chip's embedded content (the .html/.md file-chip snapshot) round-trips
// through upsert/load/get, and a contentless chip stays empty.
func TestChipContentRoundTrip(t *testing.T) {
	db := InitTestMemoryDB(t)

	embed := Chip{ID: "c1", Kind: "path", Value: "/home/u/report.html", Content: "<h1>hi</h1>"}
	plain := Chip{ID: "c2", Kind: "tag", Value: "work"}
	for _, c := range []Chip{embed, plain} {
		if err := UpsertChip(db, c); err != nil {
			t.Fatalf("upsert %s: %v", c.ID, err)
		}
	}

	got, err := GetChip(db, "c1")
	if err != nil {
		t.Fatalf("get c1: %v", err)
	}
	if got.Content != "<h1>hi</h1>" {
		t.Errorf("c1 content = %q, want the embedded html", got.Content)
	}

	all, err := LoadChips(db)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if all["c1"].Content != "<h1>hi</h1>" {
		t.Errorf("loaded c1 content = %q", all["c1"].Content)
	}
	if all["c2"].Content != "" {
		t.Errorf("plain chip c2 content = %q, want empty", all["c2"].Content)
	}

	// overwriting the content sticks
	embed.Content = "<h1>bye</h1>"
	if err := UpsertChip(db, embed); err != nil {
		t.Fatal(err)
	}
	if got, _ = GetChip(db, "c1"); got.Content != "<h1>bye</h1>" {
		t.Errorf("overwrite content failed: %q", got.Content)
	}
}
