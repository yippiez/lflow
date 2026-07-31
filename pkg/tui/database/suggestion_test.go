package database

import (
	"testing"
	"time"
)

// seedNode inserts a node for a suggestion to target.
func seedNode(t *testing.T, db *DB, uuid, parent, name string) Node {
	t.Helper()
	now := time.Now().UnixNano()
	n := Node{UUID: uuid, ParentUUID: parent, Name: name, Type: TypeBullets, AddedOn: now, EditedOn: now}
	if err := n.Insert(db); err != nil {
		t.Fatal(err)
	}
	return n
}

func mustSuggest(t *testing.T, db *DB, s *Suggestion) {
	t.Helper()
	if err := s.Insert(db); err != nil {
		t.Fatal(err)
	}
}

// TestSuggestionIsInertUntilApproved is the invariant: a pending suggestion
// changes nothing in the tree.
func TestSuggestionIsInertUntilApproved(t *testing.T) {
	db := InitTestMemoryDB(t)
	if err := EnsureRoot(db); err != nil {
		t.Fatal(err)
	}
	target := seedNode(t, db, "n1", RootUUID, "original")

	add := Suggestion{Kind: SuggestAdd, TargetUUID: RootUUID, Name: "proposed child", Type: TypeBullets}
	mustSuggest(t, db, &add)
	edit := Suggestion{Kind: SuggestEdit, TargetUUID: target.UUID, Name: "rewritten",
		Fields: FieldName, BaseName: target.Name}
	mustSuggest(t, db, &edit)

	after, err := GetNode(db, target.UUID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Name != "original" {
		t.Fatalf("pending suggestion changed the node: %q", after.Name)
	}
	kids, err := GetChildren(db, RootUUID)
	if err != nil {
		t.Fatal(err)
	}
	if len(kids) != 1 {
		t.Fatalf("pending suggestion created a node: %d children", len(kids))
	}
}

func TestApplyAddSuggestionCreatesNode(t *testing.T) {
	db := InitTestMemoryDB(t)
	if err := EnsureRoot(db); err != nil {
		t.Fatal(err)
	}
	parent := seedNode(t, db, "p1", RootUUID, "parent")

	s := Suggestion{Kind: SuggestAdd, TargetUUID: parent.UUID, Name: "new idea",
		Note: "context", Type: TypeTodo}
	mustSuggest(t, db, &s)

	applied, err := ApplySuggestion(db, s)
	if err != nil {
		t.Fatal(err)
	}
	if applied.Status != SuggestApproved {
		t.Fatalf("status = %q", applied.Status)
	}
	if applied.ResultUUID == "" {
		t.Fatal("approved add recorded no result node")
	}

	n, err := GetNode(db, applied.ResultUUID)
	if err != nil {
		t.Fatal(err)
	}
	if n.Name != "new idea" || n.Note != "context" || n.Type != TypeTodo || n.ParentUUID != parent.UUID {
		t.Fatalf("created node = %+v", n)
	}

	// the stored row settled too, so a second approval cannot double-apply
	stored, err := GetSuggestion(db, s.UUID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != SuggestApproved || stored.ResolvedOn == 0 {
		t.Fatalf("stored suggestion = %+v", stored)
	}
	if _, err := ApplySuggestion(db, stored); err == nil {
		t.Fatal("approving twice was allowed")
	}
	kids, err := GetChildren(db, parent.UUID)
	if err != nil {
		t.Fatal(err)
	}
	if len(kids) != 1 {
		t.Fatalf("double-applied: %d children", len(kids))
	}
}

func TestApplyEditSuggestionWritesOnlyProposedFields(t *testing.T) {
	db := InitTestMemoryDB(t)
	if err := EnsureRoot(db); err != nil {
		t.Fatal(err)
	}
	target := seedNode(t, db, "n1", RootUUID, "before")
	if _, err := db.Exec("UPDATE nodes SET note = ? WHERE uuid = ?", "keep me", target.UUID); err != nil {
		t.Fatal(err)
	}

	s := Suggestion{Kind: SuggestEdit, TargetUUID: target.UUID, Name: "after",
		Fields: NormalizeFields([]string{FieldName}), BaseName: "before"}
	mustSuggest(t, db, &s)

	if _, err := ApplySuggestion(db, s); err != nil {
		t.Fatal(err)
	}
	n, err := GetNode(db, target.UUID)
	if err != nil {
		t.Fatal(err)
	}
	if n.Name != "after" {
		t.Fatalf("name = %q", n.Name)
	}
	if n.Note != "keep me" {
		t.Fatalf("unproposed note was overwritten: %q", n.Note)
	}
}

func TestRejectLeavesTheTreeAlone(t *testing.T) {
	db := InitTestMemoryDB(t)
	if err := EnsureRoot(db); err != nil {
		t.Fatal(err)
	}
	target := seedNode(t, db, "n1", RootUUID, "before")

	s := Suggestion{Kind: SuggestEdit, TargetUUID: target.UUID, Name: "after",
		Fields: FieldName, BaseName: "before"}
	mustSuggest(t, db, &s)

	if err := RejectSuggestion(db, s); err != nil {
		t.Fatal(err)
	}
	n, err := GetNode(db, target.UUID)
	if err != nil {
		t.Fatal(err)
	}
	if n.Name != "before" {
		t.Fatalf("reject changed the node: %q", n.Name)
	}
	stored, err := GetSuggestion(db, s.UUID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != SuggestRejected {
		t.Fatalf("status = %q", stored.Status)
	}
	if _, err := ApplySuggestion(db, stored); err == nil {
		t.Fatal("a rejected suggestion was approvable")
	}
}

func TestApplyEditRefusesLockedOrDeletedNodes(t *testing.T) {
	db := InitTestMemoryDB(t)
	if err := EnsureRoot(db); err != nil {
		t.Fatal(err)
	}
	locked := seedNode(t, db, "locked", RootUUID, "locked node")
	gone := seedNode(t, db, "gone", RootUUID, "deleted node")
	if _, err := db.Exec("UPDATE nodes SET readonly = 1 WHERE uuid = ?", locked.UUID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("UPDATE nodes SET deleted = 1 WHERE uuid = ?", gone.UUID); err != nil {
		t.Fatal(err)
	}

	for _, uuid := range []string{locked.UUID, gone.UUID} {
		s := Suggestion{Kind: SuggestEdit, TargetUUID: uuid, Name: "changed", Fields: FieldName}
		mustSuggest(t, db, &s)
		if _, err := ApplySuggestion(db, s); err == nil {
			t.Fatalf("approving an edit to %s was allowed", uuid)
		}
		stored, err := GetSuggestion(db, s.UUID)
		if err != nil {
			t.Fatal(err)
		}
		if stored.Status != SuggestPending {
			t.Fatalf("a failed approval settled the suggestion: %q", stored.Status)
		}
	}
}

func TestTargetDriftedTracksTheNode(t *testing.T) {
	db := InitTestMemoryDB(t)
	if err := EnsureRoot(db); err != nil {
		t.Fatal(err)
	}
	target := seedNode(t, db, "n1", RootUUID, "before")

	s := Suggestion{Kind: SuggestEdit, TargetUUID: target.UUID, Name: "after",
		Fields: FieldName, BaseName: "before"}
	mustSuggest(t, db, &s)

	drifted, err := TargetDrifted(db, s)
	if err != nil {
		t.Fatal(err)
	}
	if drifted {
		t.Fatal("untouched node reported as drifted")
	}

	if _, err := db.Exec("UPDATE nodes SET name = ? WHERE uuid = ?", "somebody else edited", target.UUID); err != nil {
		t.Fatal(err)
	}
	drifted, err = TargetDrifted(db, s)
	if err != nil {
		t.Fatal(err)
	}
	if !drifted {
		t.Fatal("a changed node was not reported as drifted")
	}
}

func TestListAndResolveSuggestions(t *testing.T) {
	db := InitTestMemoryDB(t)
	if err := EnsureRoot(db); err != nil {
		t.Fatal(err)
	}
	target := seedNode(t, db, "n1", RootUUID, "node")

	pending := Suggestion{UUID: "aaaaaa11-1111", Kind: SuggestEdit, TargetUUID: target.UUID,
		Name: "one", Fields: FieldName, CreatedOn: 1}
	mustSuggest(t, db, &pending)
	other := Suggestion{UUID: "bbbbbb22-2222", Kind: SuggestAdd, TargetUUID: RootUUID,
		Name: "two", CreatedOn: 2}
	mustSuggest(t, db, &other)
	if err := RejectSuggestion(db, other); err != nil {
		t.Fatal(err)
	}

	got, err := ListSuggestions(db, SuggestionFilter{Status: SuggestPending})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].UUID != pending.UUID {
		t.Fatalf("pending listing = %+v", got)
	}

	got, err = ListSuggestions(db, SuggestionFilter{Status: "all", TargetUUID: target.UUID})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].UUID != pending.UUID {
		t.Fatalf("target listing = %+v", got)
	}

	got, err = ListSuggestions(db, SuggestionFilter{Status: "all"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("all listing = %d", len(got))
	}

	resolved, err := ResolveSuggestion(db, "aaaaaa")
	if err != nil {
		t.Fatal(err)
	}
	if resolved.UUID != pending.UUID {
		t.Fatalf("prefix resolved to %s", resolved.UUID)
	}
	if _, err := ResolveSuggestion(db, "zzzzzz"); err == nil {
		t.Fatal("a miss resolved")
	} else if _, ok := err.(ErrNoSuggestion); !ok {
		t.Fatalf("miss error = %T", err)
	}
}

// TestResolvePrefersThePendingMatch keeps short prefixes usable once a queue
// has history: an ambiguous prefix with exactly one pending match resolves.
func TestResolvePrefersThePendingMatch(t *testing.T) {
	db := InitTestMemoryDB(t)

	done := Suggestion{UUID: "cccccc11", Kind: SuggestAdd, TargetUUID: RootUUID, Name: "old", CreatedOn: 1}
	mustSuggest(t, db, &done)
	if err := RejectSuggestion(db, done); err != nil {
		t.Fatal(err)
	}
	live := Suggestion{UUID: "cccccc22", Kind: SuggestAdd, TargetUUID: RootUUID, Name: "new", CreatedOn: 2}
	mustSuggest(t, db, &live)

	got, err := ResolveSuggestion(db, "cccccc")
	if err != nil {
		t.Fatal(err)
	}
	if got.UUID != live.UUID {
		t.Fatalf("resolved to %s", got.UUID)
	}

	// two pending matches stay ambiguous rather than guessing
	third := Suggestion{UUID: "cccccc33", Kind: SuggestAdd, TargetUUID: RootUUID, Name: "newer", CreatedOn: 3}
	mustSuggest(t, db, &third)
	if _, err := ResolveSuggestion(db, "cccccc"); err == nil {
		t.Fatal("an ambiguous prefix resolved")
	} else if amb, ok := err.(ErrAmbiguousSuggestion); !ok {
		t.Fatalf("ambiguity error = %T", err)
	} else if len(amb.Matches) != 3 {
		t.Fatalf("ambiguity listed %d matches", len(amb.Matches))
	}
}

func TestNormalizeFieldsIsCanonical(t *testing.T) {
	got := NormalizeFields([]string{FieldType, FieldName, FieldName, ""})
	if got != "name,type" {
		t.Fatalf("NormalizeFields = %q", got)
	}
	s := Suggestion{Fields: got}
	if !s.Proposes(FieldName) || !s.Proposes(FieldType) || s.Proposes(FieldNote) {
		t.Fatalf("Proposes disagrees with %q", got)
	}
}
