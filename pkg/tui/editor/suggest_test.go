package editor

import (
	"strings"
	"testing"

	"github.com/lflow/lflow/pkg/tui/database"
)

// suggestModel builds a db-backed model over one node, with the node also
// present in the tree so the review surface has something to hang from.
func suggestModel(t *testing.T, name string) (*Model, *database.DB, *item) {
	t.Helper()
	db := database.InitTestMemoryDB(t)
	if err := database.EnsureRoot(db); err != nil {
		t.Fatal(err)
	}
	m := newTestModel(80, name)
	m.db = db
	cur := m.tree.root.children[0]
	cur.uuid = "n1"
	m.tree.byUUID[cur.uuid] = cur
	if err := (database.Node{UUID: cur.uuid, ParentUUID: database.RootUUID, Name: name,
		Type: database.TypeBullets}).Insert(db); err != nil {
		t.Fatal(err)
	}
	m.tree.root.uuid = database.RootUUID
	m.cursor = m.rowIndexOf(cur)
	return m, db, cur
}

func fileSuggestion(t *testing.T, db *database.DB, s database.Suggestion) database.Suggestion {
	t.Helper()
	if err := s.Insert(db); err != nil {
		t.Fatal(err)
	}
	return s
}

// TestSuggestBandShowsPendingProposal: an arriving proposal is visible under
// its node, and it has changed nothing.
func TestSuggestBandShowsPendingProposal(t *testing.T) {
	m, db, cur := suggestModel(t, "ship the thing")
	fileSuggestion(t, db, database.Suggestion{Kind: database.SuggestEdit, TargetUUID: cur.uuid,
		Name: "ship the other thing", Fields: database.FieldName, BaseName: "ship the thing",
		Author: "agent"})
	m.loadSuggests()

	band := strings.Join(m.suggestBandLines(m.rows[m.cursor], false, 80), "\n")
	if !strings.Contains(band, "suggested by agent") {
		t.Fatalf("band did not name the author: %q", band)
	}
	if !strings.Contains(band, "ship the other thing") {
		t.Fatalf("band did not show the proposal: %q", band)
	}
	if !strings.Contains(band, "review") {
		t.Fatalf("band did not offer review: %q", band)
	}
	if cur.name != "ship the thing" {
		t.Fatalf("a pending proposal changed the node: %q", cur.name)
	}
	if got := m.pendingSuggestCount(); got != 1 {
		t.Fatalf("pending count = %d", got)
	}
}

// TestSuggestReviewApprovesEdit: alt+v then y applies the proposal to the node.
func TestSuggestReviewApprovesEdit(t *testing.T) {
	m, db, cur := suggestModel(t, "ship the thing")
	s := fileSuggestion(t, db, database.Suggestion{Kind: database.SuggestEdit, TargetUUID: cur.uuid,
		Name: "ship the other thing", Fields: database.FieldName, BaseName: "ship the thing",
		Message: "clearer", Author: "agent"})
	m.loadSuggests()

	m.press("alt+v")
	if m.mode != modeSuggest {
		t.Fatalf("alt+v did not open review: mode = %v", m.mode)
	}
	band := strings.Join(m.suggestBandLines(m.rows[m.cursor], false, 80), "\n")
	for _, want := range []string{"clearer", "ship the thing", "ship the other thing", "approve", "reject"} {
		if !strings.Contains(band, want) {
			t.Fatalf("review band missing %q: %q", want, band)
		}
	}

	m.press("y")

	stored, err := database.GetSuggestion(db, s.UUID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != database.SuggestApproved {
		t.Fatalf("status = %q", stored.Status)
	}
	n, err := database.GetNode(db, cur.uuid)
	if err != nil {
		t.Fatal(err)
	}
	if n.Name != "ship the other thing" {
		t.Fatalf("approved edit did not reach the node: %q", n.Name)
	}
	if m.mode != modeOutline {
		t.Fatalf("review stayed open after the last proposal: mode = %v", m.mode)
	}
	if m.pendingSuggestCount() != 0 {
		t.Fatal("approved proposal is still queued")
	}
	// the reloaded tree shows the applied text
	if got := m.tree.byUUID[cur.uuid]; got == nil || got.name != "ship the other thing" {
		t.Fatalf("tree did not pick up the approval: %+v", got)
	}
}

// TestSuggestReviewRejectLeavesNode: n settles the proposal and the node is
// untouched.
func TestSuggestReviewRejectLeavesNode(t *testing.T) {
	m, db, cur := suggestModel(t, "ship the thing")
	s := fileSuggestion(t, db, database.Suggestion{Kind: database.SuggestEdit, TargetUUID: cur.uuid,
		Name: "ship the other thing", Fields: database.FieldName, BaseName: "ship the thing"})
	m.loadSuggests()

	m.press("alt+v")
	m.press("n")

	stored, err := database.GetSuggestion(db, s.UUID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != database.SuggestRejected {
		t.Fatalf("status = %q", stored.Status)
	}
	n, err := database.GetNode(db, cur.uuid)
	if err != nil {
		t.Fatal(err)
	}
	if n.Name != "ship the thing" {
		t.Fatalf("reject changed the node: %q", n.Name)
	}
}

// TestSuggestReviewApprovesAdd: approving a proposed child creates it under the
// node, and the reloaded tree shows it.
func TestSuggestReviewApprovesAdd(t *testing.T) {
	m, db, cur := suggestModel(t, "reading list")
	fileSuggestion(t, db, database.Suggestion{Kind: database.SuggestAdd, TargetUUID: cur.uuid,
		Name: "Designing Data-Intensive Applications", Type: database.TypeBullets, Author: "agent"})
	m.loadSuggests()

	m.press("alt+v")
	m.press("y")

	kids, err := database.GetChildren(db, cur.uuid)
	if err != nil {
		t.Fatal(err)
	}
	if len(kids) != 1 || kids[0].Name != "Designing Data-Intensive Applications" {
		t.Fatalf("approved add did not create the child: %+v", kids)
	}
	node := m.tree.byUUID[cur.uuid]
	if node == nil || len(node.children) != 1 {
		t.Fatalf("tree did not reload with the new child: %+v", node)
	}
}

// TestSuggestReviewEscKeepsItPending: esc is "not now", not a verdict.
func TestSuggestReviewEscKeepsItPending(t *testing.T) {
	m, db, cur := suggestModel(t, "ship the thing")
	s := fileSuggestion(t, db, database.Suggestion{Kind: database.SuggestEdit, TargetUUID: cur.uuid,
		Name: "ship the other thing", Fields: database.FieldName, BaseName: "ship the thing"})
	m.loadSuggests()

	m.press("alt+v")
	m.press("esc")

	if m.mode != modeOutline {
		t.Fatalf("esc did not leave review: mode = %v", m.mode)
	}
	stored, err := database.GetSuggestion(db, s.UUID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != database.SuggestPending {
		t.Fatalf("esc settled the proposal: %q", stored.Status)
	}
	if m.pendingSuggestCount() != 1 {
		t.Fatal("the proposal left the queue")
	}
}

// TestSuggestReviewRefusesDriftedTarget: the editor holds back a proposal
// written against text that has since changed, same as the CLI.
func TestSuggestReviewRefusesDriftedTarget(t *testing.T) {
	m, db, cur := suggestModel(t, "ship the thing")
	s := fileSuggestion(t, db, database.Suggestion{Kind: database.SuggestEdit, TargetUUID: cur.uuid,
		Name: "ship the other thing", Fields: database.FieldName, BaseName: "ship the thing"})
	if _, err := db.Exec("UPDATE nodes SET name = ? WHERE uuid = ?", "moved on", cur.uuid); err != nil {
		t.Fatal(err)
	}
	m.loadSuggests()

	m.press("alt+v")
	m.press("y")

	stored, err := database.GetSuggestion(db, s.UUID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != database.SuggestPending {
		t.Fatalf("a drifted proposal was applied: %q", stored.Status)
	}
	if !strings.Contains(m.flash, "changed since suggested") {
		t.Fatalf("flash did not explain the hold: %q", m.flash)
	}
}

// TestSuggestReviewWalksSeveral: two proposals on one node review in turn.
func TestSuggestReviewWalksSeveral(t *testing.T) {
	m, db, cur := suggestModel(t, "inbox")
	fileSuggestion(t, db, database.Suggestion{UUID: "s1", Kind: database.SuggestAdd,
		TargetUUID: cur.uuid, Name: "first idea", CreatedOn: 1})
	fileSuggestion(t, db, database.Suggestion{UUID: "s2", Kind: database.SuggestAdd,
		TargetUUID: cur.uuid, Name: "second idea", CreatedOn: 2})
	m.loadSuggests()

	m.press("alt+v")
	band := strings.Join(m.suggestBandLines(m.rows[m.cursor], false, 80), "\n")
	if !strings.Contains(band, "1/2") {
		t.Fatalf("review did not number the queue: %q", band)
	}
	m.press("down")
	if m.suggestSel != 1 {
		t.Fatalf("down did not walk to the second proposal: %d", m.suggestSel)
	}
	m.press("y") // approve the second one

	s2, err := database.GetSuggestion(db, "s2")
	if err != nil {
		t.Fatal(err)
	}
	if s2.Status != database.SuggestApproved {
		t.Fatalf("second proposal status = %q", s2.Status)
	}
	s1, err := database.GetSuggestion(db, "s1")
	if err != nil {
		t.Fatal(err)
	}
	if s1.Status != database.SuggestPending {
		t.Fatalf("first proposal was settled too: %q", s1.Status)
	}
	if m.mode != modeSuggest {
		t.Fatal("review closed while a proposal was still queued")
	}
}
