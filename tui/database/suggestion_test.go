package database

import (
	"testing"
	"time"

	"github.com/lflow/lflow/tui/utils/assert"
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
	edit := Suggestion{Kind: SuggestChange, TargetUUID: target.UUID, Name: "rewritten",
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

func TestCompletionSuggestionsAreInertUntilApproved(t *testing.T) {
	db := InitTestMemoryDB(t)
	if err := EnsureRoot(db); err != nil {
		t.Fatal(err)
	}
	target := seedNode(t, db, "n1", RootUUID, "task")

	complete := Suggestion{Kind: SuggestChange, Fields: FieldDone, TargetUUID: target.UUID}
	mustSuggest(t, db, &complete)
	if n, _ := GetNode(db, target.UUID); n.CompletedAt != 0 {
		t.Fatal("pending completion changed the node")
	}
	if _, err := ApplySuggestion(db, complete); err != nil {
		t.Fatal(err)
	}
	if n, _ := GetNode(db, target.UUID); n.CompletedAt == 0 {
		t.Fatal("approved completion left the node open")
	}

	uncomplete := Suggestion{Kind: SuggestChange, Fields: FieldUndone, TargetUUID: target.UUID}
	mustSuggest(t, db, &uncomplete)
	if _, err := ApplySuggestion(db, uncomplete); err != nil {
		t.Fatal(err)
	}
	if n, _ := GetNode(db, target.UUID); n.CompletedAt != 0 {
		t.Fatal("approved uncompletion left the node complete")
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

	s := Suggestion{Kind: SuggestChange, TargetUUID: target.UUID, Name: "after",
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

	s := Suggestion{Kind: SuggestChange, TargetUUID: target.UUID, Name: "after",
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
		s := Suggestion{Kind: SuggestChange, TargetUUID: uuid, Name: "changed", Fields: FieldName}
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

// TestSweepStaleSuggestions: a pending proposal whose target node was deleted
// or expunged can never be approved, so the startup sweep settles it. Live
// targets — and the root (an add's empty target) — stay pending.
func TestSweepStaleSuggestions(t *testing.T) {
	db := InitTestMemoryDB(t)
	if err := EnsureRoot(db); err != nil {
		t.Fatal(err)
	}
	live := seedNode(t, db, "live", RootUUID, "still here")
	gone := seedNode(t, db, "gone", RootUUID, "tombstoned")
	missing := seedNode(t, db, "missing", RootUUID, "expunged")
	if _, err := db.Exec("UPDATE nodes SET deleted = 1 WHERE uuid = ?", gone.UUID); err != nil {
		t.Fatal(err)
	}
	if err := (Node{UUID: missing.UUID}).Expunge(db); err != nil {
		t.Fatal(err)
	}

	mustSuggest(t, db, &Suggestion{UUID: "s-live", Kind: SuggestChange, Fields: FieldDone, TargetUUID: live.UUID, CreatedOn: 1})
	mustSuggest(t, db, &Suggestion{UUID: "s-gone", Kind: SuggestChange, TargetUUID: gone.UUID,
		Name: "changed", Fields: FieldName, CreatedOn: 2})
	mustSuggest(t, db, &Suggestion{UUID: "s-missing", Kind: SuggestAdd, TargetUUID: missing.UUID,
		Name: "child", CreatedOn: 3})
	mustSuggest(t, db, &Suggestion{UUID: "s-root", Kind: SuggestAdd, TargetUUID: "", Name: "root child", CreatedOn: 4})

	n, err := SweepStaleSuggestions(db)
	if err != nil {
		t.Fatal(err)
	}
	assert.Equal(t, n, 2, "swept count mismatch")

	check := func(uuid, want string) {
		t.Helper()
		var got string
		MustScan(t, "getting status", db.QueryRow("SELECT status FROM suggestions WHERE uuid = ?", uuid), &got)
		if got != want {
			t.Fatalf("suggestion %s status = %q, want %q", uuid, got, want)
		}
	}
	check("s-live", SuggestPending)
	check("s-root", SuggestPending)
	check("s-gone", SuggestRejected)
	check("s-missing", SuggestRejected)

	// settled rows carry a resolution time like any rejection
	var resolved int64
	MustScan(t, "getting resolved_on", db.QueryRow("SELECT resolved_on FROM suggestions WHERE uuid = 's-gone'"), &resolved)
	if resolved == 0 {
		t.Fatal("swept suggestion has no resolved_on")
	}

	// a second sweep is a no-op
	n, err = SweepStaleSuggestions(db)
	if err != nil {
		t.Fatal(err)
	}
	assert.Equal(t, n, 0, "second sweep should settle nothing")
}

// TestDeletingANodeSettlesItsSuggestions is the live-session half of the ghost
// problem: the boot sweep only runs at boot, so the delete itself has to settle
// the proposals it orphans — including the ones about descendants.
func TestDeletingANodeSettlesItsSuggestions(t *testing.T) {
	db := InitTestMemoryDB(t)
	if err := EnsureRoot(db); err != nil {
		t.Fatal(err)
	}
	parent := seedNode(t, db, "parent", RootUUID, "doomed")
	child := seedNode(t, db, "child", parent.UUID, "doomed child")
	bystander := seedNode(t, db, "bystander", RootUUID, "untouched")

	mustSuggest(t, db, &Suggestion{UUID: "s-parent", Kind: SuggestChange, TargetUUID: parent.UUID,
		Name: "renamed", Fields: FieldName, CreatedOn: 1})
	mustSuggest(t, db, &Suggestion{UUID: "s-child", Kind: SuggestChange, Fields: FieldDone, TargetUUID: child.UUID, CreatedOn: 2})
	mustSuggest(t, db, &Suggestion{UUID: "s-bystander", Kind: SuggestChange, Fields: FieldDone, TargetUUID: bystander.UUID, CreatedOn: 3})

	if _, err := MarkSubtreeDeleted(db, parent.UUID); err != nil {
		t.Fatal(err)
	}

	check := func(uuid, want string) {
		t.Helper()
		var got string
		MustScan(t, "getting status", db.QueryRow("SELECT status FROM suggestions WHERE uuid = ?", uuid), &got)
		if got != want {
			t.Fatalf("suggestion %s status = %q, want %q", uuid, got, want)
		}
	}
	check("s-parent", SuggestRejected)
	check("s-child", SuggestRejected)
	check("s-bystander", SuggestPending)

	// nothing is left for the boot sweep to find
	n, err := SweepStaleSuggestions(db)
	if err != nil {
		t.Fatal(err)
	}
	assert.Equal(t, n, 0, "delete left stale suggestions for the sweep")
}

// TestSettleSuggestionsForNodesIsNarrow guards the statement's edges: an empty
// set is a no-op rather than a SQL error, and a settled suggestion is never
// re-settled.
func TestSettleSuggestionsForNodesIsNarrow(t *testing.T) {
	db := InitTestMemoryDB(t)
	if err := EnsureRoot(db); err != nil {
		t.Fatal(err)
	}
	target := seedNode(t, db, "n1", RootUUID, "node")
	mustSuggest(t, db, &Suggestion{UUID: "s-done", Kind: SuggestChange, Fields: FieldDone, TargetUUID: target.UUID, CreatedOn: 1})

	n, err := SettleSuggestionsForNodes(db, nil)
	if err != nil {
		t.Fatalf("empty node set: %v", err)
	}
	assert.Equal(t, n, 0, "empty set settled something")

	if n, err = SettleSuggestionsForNodes(db, []string{target.UUID}); err != nil {
		t.Fatal(err)
	}
	assert.Equal(t, n, 1, "first settle count mismatch")

	// already rejected: settling again must not touch it
	if n, err = SettleSuggestionsForNodes(db, []string{target.UUID}); err != nil {
		t.Fatal(err)
	}
	assert.Equal(t, n, 0, "re-settled an already settled suggestion")
}

func TestTargetGone(t *testing.T) {
	db := InitTestMemoryDB(t)
	if err := EnsureRoot(db); err != nil {
		t.Fatal(err)
	}
	live := seedNode(t, db, "live", RootUUID, "still here")
	gone := seedNode(t, db, "gone", RootUUID, "tombstoned")
	if _, err := db.Exec("UPDATE nodes SET deleted = 1 WHERE uuid = ?", gone.UUID); err != nil {
		t.Fatal(err)
	}

	goneEdit := Suggestion{Kind: SuggestChange, TargetUUID: gone.UUID}
	if got, err := TargetGone(db, goneEdit); err != nil || !got {
		t.Fatalf("deleted target gone = %v, %v", got, err)
	}
	goneMissing := Suggestion{Kind: SuggestChange, TargetUUID: "never-existed"}
	if got, err := TargetGone(db, goneMissing); err != nil || !got {
		t.Fatalf("missing target gone = %v, %v", got, err)
	}
	liveEdit := Suggestion{Kind: SuggestChange, TargetUUID: live.UUID}
	if got, err := TargetGone(db, liveEdit); err != nil || got {
		t.Fatalf("live target gone = %v, %v", got, err)
	}
	rootAdd := Suggestion{Kind: SuggestAdd, TargetUUID: ""}
	if got, err := TargetGone(db, rootAdd); err != nil || got {
		t.Fatalf("root add target gone = %v, %v", got, err)
	}
}

func TestTargetDriftedTracksTheNode(t *testing.T) {
	db := InitTestMemoryDB(t)
	if err := EnsureRoot(db); err != nil {
		t.Fatal(err)
	}
	target := seedNode(t, db, "n1", RootUUID, "before")

	s := Suggestion{Kind: SuggestChange, TargetUUID: target.UUID, Name: "after",
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

	pending := Suggestion{UUID: "aaaaaa11-1111", Kind: SuggestChange, TargetUUID: target.UUID,
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

// TestApplyRemoveTakesTheSubtree: approving a removal tombstones the node and
// everything under it — and the proposal that ordered it settles as APPROVED,
// not swept away as "a proposal about a deleted node" by its own delete.
func TestApplyRemoveTakesTheSubtree(t *testing.T) {
	db := InitTestMemoryDB(t)
	if err := EnsureRoot(db); err != nil {
		t.Fatal(err)
	}
	parent := seedNode(t, db, "p", RootUUID, "going away")
	seedNode(t, db, "c", parent.UUID, "under it")
	bystander := seedNode(t, db, "b", RootUUID, "staying")

	s := Suggestion{UUID: "s1", Kind: SuggestRemove, TargetUUID: parent.UUID, BaseName: parent.Name}
	mustSuggest(t, db, &s)
	if n, _ := GetNode(db, parent.UUID); n.Deleted {
		t.Fatal("a pending removal deleted the node")
	}

	applied, err := ApplySuggestion(db, s)
	if err != nil {
		t.Fatal(err)
	}
	if applied.Status != SuggestApproved {
		t.Fatalf("the approved removal settled as %q", applied.Status)
	}
	stored, err := GetSuggestion(db, s.UUID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != SuggestApproved {
		t.Fatalf("stored status = %q, want approved", stored.Status)
	}
	for _, uuid := range []string{parent.UUID, "c"} {
		n, err := GetNode(db, uuid)
		if err != nil {
			t.Fatal(err)
		}
		if !n.Deleted {
			t.Fatalf("%s survived the removal", uuid)
		}
	}
	if n, _ := GetNode(db, bystander.UUID); n.Deleted {
		t.Fatal("the removal reached a node outside the subtree")
	}
}

// TestApplyRemoveRefusesLockedNodes: a locked node cannot be deleted through a
// proposal any more than it can by hand.
func TestApplyRemoveRefusesLockedNodes(t *testing.T) {
	db := InitTestMemoryDB(t)
	if err := EnsureRoot(db); err != nil {
		t.Fatal(err)
	}
	target := seedNode(t, db, "n1", RootUUID, "locked")
	if _, err := db.Exec("UPDATE nodes SET readonly = ? WHERE uuid = ?", LockReadWrite, target.UUID); err != nil {
		t.Fatal(err)
	}
	s := Suggestion{Kind: SuggestRemove, TargetUUID: target.UUID}
	mustSuggest(t, db, &s)

	if _, err := ApplySuggestion(db, s); err == nil {
		t.Fatal("approving a removal on a locked node succeeded")
	}
	if n, _ := GetNode(db, target.UUID); n.Deleted {
		t.Fatal("the refused removal still deleted the node")
	}
}

// TestLegacyKindsReadAsChanges: rows written before the three-verb API — edit,
// complete, uncomplete — come back as changes, so nothing downstream has to know
// the old spelling. The stored rows are left as they are.
func TestLegacyKindsReadAsChanges(t *testing.T) {
	db := InitTestMemoryDB(t)
	if err := EnsureRoot(db); err != nil {
		t.Fatal(err)
	}
	target := seedNode(t, db, "n1", RootUUID, "task")
	for _, legacy := range []struct {
		uuid, kind, field string
	}{
		{"s-edit", "edit", FieldName},
		{"s-done", "complete", FieldDone},
		{"s-open", "uncomplete", FieldUndone},
	} {
		fields := ""
		if legacy.kind == "edit" {
			fields = FieldName
		}
		if _, err := db.Exec(`INSERT INTO suggestions (uuid, kind, target_uuid, name, fields, status)
			VALUES (?, ?, ?, ?, ?, ?)`, legacy.uuid, legacy.kind, target.UUID, "renamed", fields, SuggestPending); err != nil {
			t.Fatal(err)
		}
		s, err := GetSuggestion(db, legacy.uuid)
		if err != nil {
			t.Fatal(err)
		}
		if s.Kind != SuggestChange {
			t.Fatalf("%s read as kind %q, want change", legacy.uuid, s.Kind)
		}
		if !s.Proposes(legacy.field) {
			t.Fatalf("%s lost its %s field: %q", legacy.uuid, legacy.field, s.Fields)
		}
	}

	// …and a change filter reaches them, whatever they are spelled like on disk
	list, err := ListSuggestions(db, SuggestionFilter{Status: SuggestPending, Kind: SuggestChange})
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 3 {
		t.Fatalf("change filter matched %d of 3 legacy rows", len(list))
	}
}
