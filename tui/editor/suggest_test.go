package editor

import (
	"strings"
	"testing"

	"github.com/lflow/lflow/tui/database"
	"github.com/lflow/lflow/tui/runtime"
)

// suggestModel builds a db-backed model over one node, with the node also
// present in the tree so a proposal has something to hang from.
func suggestModel(t *testing.T, name string) (*Model, *database.DB, *item) {
	t.Helper()
	db := database.InitTestMemoryDB(t)
	if err := database.EnsureRoot(db); err != nil {
		t.Fatal(err)
	}
	m := newTestModel(80, name)
	m.db = db
	m.chips = map[string]database.Chip{}
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

// loadAndRefresh reloads the queue and rebuilds the rows, the way any feed
// event does — ghost rows only exist after both.
func loadAndRefresh(m *Model) {
	m.loadSuggests()
	m.refreshRows()
}

// rowLine renders one visible row the way the editor draws it.
func rowLine(m *Model, i int) string {
	groups, _ := m.viewRenderRows(120)
	return strings.Join(groups[i], "\n")
}

// ghostRow finds the ghost row for a proposal, or -1.
func ghostRow(m *Model, uuid string) int {
	for i, r := range m.rows {
		if r.it != nil && r.it.ghost == uuid {
			return i
		}
	}
	return -1
}

// TestAddProposalIsAGhostRow: an add proposal shows as the node it would be,
// where it would be — a yellow row under the parent, carrying the verdict
// prompt — and it has not touched the tree.
func TestAddProposalIsAGhostRow(t *testing.T) {
	m, db, cur := suggestModel(t, "reading list")
	fileSuggestion(t, db, database.Suggestion{UUID: "s1", Kind: database.SuggestAdd,
		TargetUUID: cur.uuid, Name: "Designing Data-Intensive Applications", Author: "agent"})
	loadAndRefresh(m)

	i := ghostRow(m, "s1")
	if i < 0 {
		t.Fatal("the proposal drew no ghost row")
	}
	if m.rows[i].depth != m.rows[m.rowIndexOf(cur)].depth+1 {
		t.Fatalf("ghost depth = %d, want one under its parent", m.rows[i].depth)
	}
	line := rowLine(m, i)
	if !strings.Contains(stripSGR(line), "Designing Data-Intensive Applications") {
		t.Fatalf("ghost row does not read as the proposed node: %q", stripSGR(line))
	}
	if !strings.Contains(line, cYellow) {
		t.Fatalf("the proposed node is not yellow: %q", line)
	}
	if !strings.Contains(stripSGR(line), "· add ⌥y/⌥n") {
		t.Fatalf("ghost row carries no verdict prompt: %q", stripSGR(line))
	}

	// …and it is a reading of the queue, not a node: nothing in the tree, and a
	// save does not create it
	if len(cur.children) != 0 {
		t.Fatalf("the proposal entered the tree: %+v", cur.children)
	}
	if _, err := m.saveAll(); err != nil {
		t.Fatal(err)
	}
	kids, err := database.GetChildren(db, cur.uuid)
	if err != nil {
		t.Fatal(err)
	}
	if len(kids) != 0 {
		t.Fatalf("saving wrote a pending proposal to the outline: %+v", kids)
	}
}

// TestEveryPendingRowCarriesThePrompt: the prompt is on each proposal, not just
// the one under the cursor — a row you cannot answer where it stands is a row
// you have to go looking for.
func TestEveryPendingRowCarriesThePrompt(t *testing.T) {
	m, db, cur := suggestModel(t, "reading list")
	fileSuggestion(t, db, database.Suggestion{UUID: "s1", Kind: database.SuggestAdd,
		TargetUUID: cur.uuid, Name: "first idea", CreatedOn: 1})
	fileSuggestion(t, db, database.Suggestion{UUID: "s2", Kind: database.SuggestAdd,
		TargetUUID: cur.uuid, Name: "second idea", CreatedOn: 2})
	loadAndRefresh(m)

	for _, uuid := range []string{"s1", "s2"} {
		i := ghostRow(m, uuid)
		if i < 0 {
			t.Fatalf("%s drew no ghost row", uuid)
		}
		if m.cursor == i {
			continue // the prompt under the cursor proves nothing about the rest
		}
		if !strings.Contains(stripSGR(rowLine(m, i)), "⌥y/⌥n") {
			t.Fatalf("%s carries no prompt away from the cursor: %q", uuid, stripSGR(rowLine(m, i)))
		}
	}
}

// TestGhostRowApprovesAndRejects: ⌥y on the ghost row creates the node, ⌥n
// drops the proposal and leaves the outline alone.
func TestGhostRowApprovesAndRejects(t *testing.T) {
	m, db, cur := suggestModel(t, "reading list")
	fileSuggestion(t, db, database.Suggestion{UUID: "s1", Kind: database.SuggestAdd,
		TargetUUID: cur.uuid, Name: "keep me", CreatedOn: 1})
	fileSuggestion(t, db, database.Suggestion{UUID: "s2", Kind: database.SuggestAdd,
		TargetUUID: cur.uuid, Name: "drop me", CreatedOn: 2})
	loadAndRefresh(m)

	m.cursor = ghostRow(m, "s1")
	m.press("alt+y")
	kids, err := database.GetChildren(db, cur.uuid)
	if err != nil {
		t.Fatal(err)
	}
	if len(kids) != 1 || kids[0].Name != "keep me" {
		t.Fatalf("⌥y did not add the proposed node: %+v", kids)
	}

	m.cursor = ghostRow(m, "s2")
	if m.cursor < 0 {
		t.Fatal("the second proposal lost its row after the first was answered")
	}
	m.press("alt+n")
	kids, err = database.GetChildren(db, cur.uuid)
	if err != nil {
		t.Fatal(err)
	}
	if len(kids) != 1 {
		t.Fatalf("⌥n wrote to the outline: %+v", kids)
	}
	s2, err := database.GetSuggestion(db, "s2")
	if err != nil {
		t.Fatal(err)
	}
	if s2.Status != database.SuggestRejected {
		t.Fatalf("rejected proposal status = %q", s2.Status)
	}
	if m.pendingSuggestCount() != 0 {
		t.Fatalf("queue still holds %d after both verdicts", m.pendingSuggestCount())
	}
}

// TestGhostRowRefusesOutlineKeys: a ghost row is not a node, so typing into it,
// splitting it or deleting it does nothing — the outline cannot be edited
// through a proposal.
func TestGhostRowRefusesOutlineKeys(t *testing.T) {
	m, db, cur := suggestModel(t, "reading list")
	fileSuggestion(t, db, database.Suggestion{UUID: "s1", Kind: database.SuggestAdd,
		TargetUUID: cur.uuid, Name: "proposed", CreatedOn: 1})
	loadAndRefresh(m)
	m.cursor = ghostRow(m, "s1")
	rows := len(m.rows)

	for _, k := range []string{"x", "enter", "tab", "backspace"} {
		m.press(k)
		if len(m.rows) != rows {
			t.Fatalf("%q changed the outline through a ghost row: %d rows, want %d", k, len(m.rows), rows)
		}
		if cur.children != nil {
			t.Fatalf("%q created a node under the parent: %+v", k, cur.children)
		}
	}
	if g := m.cursorItem(); g == nil || g.name != "proposed" {
		t.Fatalf("the ghost row's text was edited: %+v", g)
	}
	if !strings.Contains(m.flash, "⌥y") {
		t.Fatalf("a refused key says nothing about how to answer: %q", m.flash)
	}
	// …and it is still only a proposal in the database
	if _, err := m.saveAll(); err != nil {
		t.Fatal(err)
	}
	kids, err := database.GetChildren(db, cur.uuid)
	if err != nil {
		t.Fatal(err)
	}
	if len(kids) != 0 {
		t.Fatalf("keys on a ghost row reached the outline: %+v", kids)
	}
}

// TestRemoveProposalShinesTheRow: a node proposed for removal keeps its text and
// its place, painted with the sliding shine and told what ⌥y would do.
func TestRemoveProposalShinesTheRow(t *testing.T) {
	m, db, cur := suggestModel(t, "obsolete step")
	fileSuggestion(t, db, database.Suggestion{UUID: "s1", Kind: database.SuggestRemove,
		TargetUUID: cur.uuid, BaseName: "obsolete step", Author: "agent"})
	loadAndRefresh(m)

	line := rowLine(m, m.rowIndexOf(cur))
	plain := stripSGR(line)
	if !strings.Contains(plain, "obsolete step") {
		t.Fatalf("the row lost its own text: %q", plain)
	}
	if !strings.Contains(plain, "· remove ⌥y/⌥n") {
		t.Fatalf("the row does not say it is proposed for removal: %q", plain)
	}
	// the shine paints per-rune color, so the row carries SGR runs rather than
	// one flat span
	if strings.Count(line, "\x1b[38;2;") < 4 {
		t.Fatalf("the row is not shining: %q", line)
	}
	if !m.animActive() {
		t.Error("a proposed removal must keep the animation tick alive")
	}
	if cur.name != "obsolete step" || m.tree.byUUID[cur.uuid] == nil {
		t.Fatal("a pending removal changed the outline")
	}
}

// TestApproveRemoveTakesTheSubtree: ⌥y on a removal tombstones the node and
// everything under it — and marks the proposal that ordered it approved, rather
// than settling itself in the sweep it triggers.
func TestApproveRemoveTakesTheSubtree(t *testing.T) {
	m, db, cur := suggestModel(t, "obsolete step")
	if err := (database.Node{UUID: "kid", ParentUUID: cur.uuid, Name: "under it",
		Type: database.TypeBullets}).Insert(db); err != nil {
		t.Fatal(err)
	}
	s := fileSuggestion(t, db, database.Suggestion{UUID: "s1", Kind: database.SuggestRemove,
		TargetUUID: cur.uuid, BaseName: "obsolete step"})
	loadAndRefresh(m)

	m.cursor = m.rowIndexOf(cur)
	m.press("alt+y")

	stored, err := database.GetSuggestion(db, s.UUID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != database.SuggestApproved {
		t.Fatalf("the approved removal settled as %q", stored.Status)
	}
	for _, uuid := range []string{cur.uuid, "kid"} {
		n, err := database.GetNode(db, uuid)
		if err != nil {
			t.Fatal(err)
		}
		if !n.Deleted {
			t.Fatalf("%s survived the approved removal", uuid)
		}
	}
	if m.tree.byUUID[cur.uuid] != nil {
		t.Fatal("the removed node is still on screen")
	}
}

// TestChangeProposalDiffsInPlace: a proposed rewording shows only what it
// touches — the removed words muted and struck through, the new ones yellow,
// everything else left alone.
func TestChangeProposalDiffsInPlace(t *testing.T) {
	m, db, cur := suggestModel(t, "ship the release notes")
	fileSuggestion(t, db, database.Suggestion{UUID: "s1", Kind: database.SuggestChange,
		TargetUUID: cur.uuid, Name: "ship the launch notes", Fields: database.FieldName,
		BaseName: "ship the release notes", Author: "agent"})
	loadAndRefresh(m)

	line := rowLine(m, m.rowIndexOf(cur))
	plain := stripSGR(line)
	if !strings.Contains(plain, "release") || !strings.Contains(plain, "launch") {
		t.Fatalf("the diff dropped a side: %q", plain)
	}
	if strings.Count(plain, "ship the") != 1 {
		t.Fatalf("unchanged words were repeated: %q", plain)
	}
	if !strings.Contains(line, cDim+cStrike+"release") {
		t.Fatalf("the old wording is not struck through and muted: %q", line)
	}
	if !strings.Contains(line, cYellow+"launch") {
		t.Fatalf("the new wording is not yellow: %q", line)
	}
	if !strings.Contains(plain, "· ⌥y/⌥n") {
		t.Fatalf("the change carries no prompt: %q", plain)
	}
	if cur.name != "ship the release notes" {
		t.Fatalf("a pending change edited the node: %q", cur.name)
	}
}

// TestChangeKeepsTheCaretOnTheRow: the row is still yours while a proposal sits
// on it — the caret stays where the next keystroke would land.
func TestChangeKeepsTheCaretOnTheRow(t *testing.T) {
	m, db, cur := suggestModel(t, "ship the release notes")
	fileSuggestion(t, db, database.Suggestion{UUID: "s1", Kind: database.SuggestChange,
		TargetUUID: cur.uuid, Name: "ship the launch notes", Fields: database.FieldName,
		BaseName: "ship the release notes"})
	loadAndRefresh(m)
	m.cursor, m.caret = m.rowIndexOf(cur), 2

	if !strings.Contains(rowLine(m, m.cursor), cInvert) {
		t.Fatalf("the caret went missing under the diff: %q", rowLine(m, m.cursor))
	}
}

// TestChangeShowsFieldsTheRowCannot: note, type and done state have no place in
// the row's own text, so they read as before → after after it.
func TestChangeShowsFieldsTheRowCannot(t *testing.T) {
	m, db, cur := suggestModel(t, "quarter plan")
	fileSuggestion(t, db, database.Suggestion{UUID: "s1", Kind: database.SuggestChange,
		TargetUUID: cur.uuid, Type: database.TypeTodo,
		Fields: database.NormalizeFields([]string{database.FieldType, database.FieldDone})})
	loadAndRefresh(m)

	plain := stripSGR(rowLine(m, m.rowIndexOf(cur)))
	for _, want := range []string{"type", database.TypeBullets, database.TypeTodo, "complete", "⌥y/⌥n"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("the row does not show %q: %q", want, plain)
		}
	}

	m.cursor = m.rowIndexOf(cur)
	m.press("alt+y")
	n, err := database.GetNode(db, cur.uuid)
	if err != nil {
		t.Fatal(err)
	}
	if n.Type != database.TypeTodo || n.CompletedAt == 0 {
		t.Fatalf("approving the change did not apply both fields: %+v", n)
	}
}

// TestSuggestCountsTheRestOnOneRow: several proposals about the SAME node stack
// behind one prompt, so the row says how many more are waiting.
func TestSuggestCountsTheRestOnOneRow(t *testing.T) {
	m, db, cur := suggestModel(t, "ship it")
	fileSuggestion(t, db, database.Suggestion{UUID: "s1", Kind: database.SuggestChange,
		TargetUUID: cur.uuid, Name: "ship it now", Fields: database.FieldName,
		BaseName: "ship it", CreatedOn: 1})
	fileSuggestion(t, db, database.Suggestion{UUID: "s2", Kind: database.SuggestChange,
		TargetUUID: cur.uuid, Fields: database.FieldDone, CreatedOn: 2})
	loadAndRefresh(m)

	if plain := stripSGR(rowLine(m, m.rowIndexOf(cur))); !strings.Contains(plain, "+1") {
		t.Fatalf("the row does not count the rest: %q", plain)
	}
}

// TestAltYStillYanksWithoutAProposal: the verdict chord only diverts ⌥y on a row
// that carries a proposal; everywhere else it is still the yank.
func TestAltYStillYanksWithoutAProposal(t *testing.T) {
	m, _, cur := suggestModel(t, "just a node")
	m.cursor = m.rowIndexOf(cur)
	m.press("alt+y")
	if !strings.Contains(m.flash, "copied") && !strings.Contains(m.flash, "clipboard") {
		t.Fatalf("⌥y stopped yanking on an ordinary row: %q", m.flash)
	}
}

// TestSuggestRefusesDriftedTarget: the editor holds back a proposal written
// against text that has since changed, same as the CLI.
func TestSuggestRefusesDriftedTarget(t *testing.T) {
	m, db, cur := suggestModel(t, "ship the thing")
	s := fileSuggestion(t, db, database.Suggestion{Kind: database.SuggestChange, TargetUUID: cur.uuid,
		Name: "ship the other thing", Fields: database.FieldName, BaseName: "ship the thing"})
	if _, err := db.Exec("UPDATE nodes SET name = ? WHERE uuid = ?", "moved on", cur.uuid); err != nil {
		t.Fatal(err)
	}
	loadAndRefresh(m)

	m.cursor = m.rowIndexOf(cur)
	m.press("alt+y")

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

// TestDeletingANodeSettlesItsSuggestions: deleting the node a proposal is about
// settles the proposal in the same save. Otherwise the queue counts a node that
// is not on screen and cannot be answered, and ⌥v walks onto a ghost for the
// rest of the session — the boot sweep is too late to help.
func TestDeletingANodeSettlesItsSuggestions(t *testing.T) {
	m, db, cur := suggestModel(t, "doomed")
	// the helper wires the db onto the Model but not the tree, and leaves the
	// item flagged new; a node loaded from the DB is neither, and only a tree
	// with a db and a non-new item tombstones anything on save
	m.tree.db = db
	cur.isNew = false
	fileSuggestion(t, db, database.Suggestion{Kind: database.SuggestChange, TargetUUID: cur.uuid,
		Name: "renamed", Fields: database.FieldName, BaseName: "doomed"})
	loadAndRefresh(m)
	if got := m.pendingSuggestCount(); got != 1 {
		t.Fatalf("queue size before delete = %d, want 1", got)
	}

	m.deleteNode(cur)
	if _, err := m.saveAll(); err != nil {
		t.Fatal(err)
	}

	if got := m.pendingSuggestCount(); got != 0 {
		t.Fatalf("queue still holds %d proposal(s) about the deleted node", got)
	}
	var status string
	database.MustScan(t, "getting status",
		db.QueryRow("SELECT status FROM suggestions WHERE target_uuid = ?", cur.uuid), &status)
	if status != database.SuggestRejected {
		t.Fatalf("suggestion status = %q, want %q", status, database.SuggestRejected)
	}

	// and the walk has nowhere to get stuck
	m.gotoSuggestion()
	if !strings.Contains(m.flash, "no suggestions") {
		t.Fatalf("flash = %q, want it to report an empty queue", m.flash)
	}
}

func TestGotoSuggestionSingleKey(t *testing.T) {
	m, db, cur := suggestModel(t, "ship the thing")
	fileSuggestion(t, db, database.Suggestion{Kind: database.SuggestChange, TargetUUID: cur.uuid,
		Fields: database.FieldDone})
	loadAndRefresh(m)
	m.cursor = 0

	// alt+v walks to the next node carrying a proposal — the single-key
	// successor of the old alt+g s chord.
	m.press("alt+v")
	if got := m.cursorItem(); got == nil || got.uuid != cur.uuid {
		t.Fatalf("alt+v did not land on the proposal: %+v (flash %q)", got, m.flash)
	}

	// alt+g on plain text opens the node finder directly — the single-key
	// successor of the old alt+g g chord.
	m.press("alt+g")
	if m.mode != modeFinder || m.finder.act != actGoto {
		t.Fatalf("alt+g did not open goto: mode=%v action=%v", m.mode, m.finder.act)
	}

	hasNew, hasOld := false, false
	for _, c := range slashCommands {
		hasNew = hasNew || c.name == "/goto:suggestion"
		hasOld = hasOld || c.name == "/suggestions"
	}
	if !hasNew || hasOld {
		t.Fatalf("suggestion commands: new=%v old=%v", hasNew, hasOld)
	}
}

// TestAltVLandsOnTheGhostRow: an add proposal is answered on the ghost row, so
// that is where the walk has to put the cursor.
func TestAltVLandsOnTheGhostRow(t *testing.T) {
	m, db, cur := suggestModel(t, "reading list")
	fileSuggestion(t, db, database.Suggestion{UUID: "s1", Kind: database.SuggestAdd,
		TargetUUID: cur.uuid, Name: "a proposed child"})
	loadAndRefresh(m)
	m.cursor = 0

	m.press("alt+v")
	if got := m.cursorItem(); got == nil || got.ghost != "s1" {
		t.Fatalf("alt+v did not land on the ghost row: %+v (flash %q)", got, m.flash)
	}
}

// forestModel builds a db-backed model over a two-branch forest and opens the
// view on one branch, so the other branch's nodes are not loaded at all — the
// shape that used to dead-end goto-suggestion with "none in this view".
func forestModel(t *testing.T) (*Model, *database.DB) {
	t.Helper()
	db := database.InitTestMemoryDB(t)
	if err := database.EnsureRoot(db); err != nil {
		t.Fatal(err)
	}
	for _, n := range []database.Node{
		{UUID: "a", ParentUUID: database.RootUUID, Name: "branch a", Rank: 0},
		{UUID: "a1", ParentUUID: "a", Name: "here", Rank: 0},
		{UUID: "b", ParentUUID: database.RootUUID, Name: "branch b", Rank: 1},
		{UUID: "b1", ParentUUID: "b", Name: "over there", Rank: 0},
		{UUID: "b2", ParentUUID: "b", Name: "also over there", Rank: 1},
	} {
		n.Type = database.TypeBullets
		if err := n.Insert(db); err != nil {
			t.Fatal(err)
		}
	}
	tr, err := loadTree(db, "a") // the view is branch a; branch b is off-tree
	if err != nil {
		t.Fatal(err)
	}
	m := &Model{db: db, ctx: runtime.Ctx{DB: db}, tree: tr,
		viewStack: []*item{tr.root}, width: 80, height: 24,
		chips: map[string]database.Chip{}}
	m.refreshRows()
	return m, db
}

// TestAltVFallsThroughToTheNextSuggestion: the cursor sits on a node with
// nothing pending, but the queue has proposals elsewhere — alt+v walks the
// global queue to the next one instead of dead-ending on the current node.
func TestAltVFallsThroughToTheNextSuggestion(t *testing.T) {
	m, db := forestModel(t)
	fileSuggestion(t, db, database.Suggestion{UUID: "s1", Kind: database.SuggestChange,
		TargetUUID: "b1", Name: "renamed", Fields: database.FieldName, CreatedOn: 1})
	loadAndRefresh(m)

	if cur := m.cursorItem(); cur != nil && m.suggestsFor(cur.uuid) != nil {
		t.Fatalf("the cursor node should have no proposal: %q", cur.uuid)
	}
	m.press("alt+v")
	if cur := m.cursorItem(); cur == nil || cur.uuid != "b1" {
		t.Fatalf("alt+v did not walk to b1: %+v (flash %q)", cur, m.flash)
	}
}

// TestGotoSuggestionReachesOffViewTargets: the queue is global, so a proposal
// filed against a node outside the loaded view reopens the outline at that
// node's PARENT — the target has to be a row to be answered.
func TestGotoSuggestionReachesOffViewTargets(t *testing.T) {
	m, db := forestModel(t)
	fileSuggestion(t, db, database.Suggestion{UUID: "s1", Kind: database.SuggestChange,
		TargetUUID: "b1", Name: "renamed", Fields: database.FieldName, CreatedOn: 1})
	loadAndRefresh(m)

	if m.rowIndexOfUUID("b1") >= 0 {
		t.Fatal("b1 should not be on screen before the jump")
	}
	m.gotoSuggestion()

	if m.tree.root.uuid != "b" {
		t.Fatalf("view root = %q, want the target's parent b", m.tree.root.uuid)
	}
	if cur := m.cursorItem(); cur == nil || cur.uuid != "b1" {
		t.Fatalf("cursor did not land on the target: %+v", cur)
	}
}

// TestGotoSuggestionWalksTheQueue: the first target is the oldest one filed —
// deterministically, not whatever the map iterated first — and repeated presses
// walk the rest of the queue and wrap.
func TestGotoSuggestionWalksTheQueue(t *testing.T) {
	m, db := forestModel(t)
	fileSuggestion(t, db, database.Suggestion{UUID: "s2", Kind: database.SuggestChange,
		TargetUUID: "b2", Fields: database.FieldDone, CreatedOn: 2})
	fileSuggestion(t, db, database.Suggestion{UUID: "s1", Kind: database.SuggestChange,
		TargetUUID: "b1", Fields: database.FieldDone, CreatedOn: 1})
	fileSuggestion(t, db, database.Suggestion{UUID: "s3", Kind: database.SuggestChange,
		TargetUUID: "a1", Fields: database.FieldDone, CreatedOn: 3})
	loadAndRefresh(m)

	if want := []string{"b1", "b2", "a1"}; !equalStrs(m.suggestOrder, want) {
		t.Fatalf("queue order = %v, want %v", m.suggestOrder, want)
	}
	for _, want := range []string{"b1", "b2", "a1", "b1"} { // wraps back around
		m.gotoSuggestion()
		if cur := m.cursorItem(); cur == nil || cur.uuid != want {
			t.Fatalf("walk landed on %+v, want %q (flash %q)", cur, want, m.flash)
		}
	}
}

// TestGotoSuggestionCleansZombieTargets: a proposal whose node was deleted is
// not just skipped — the walk settles it so the queue never trips on the same
// ghost again, then moves on to the next answerable target.
func TestGotoSuggestionCleansZombieTargets(t *testing.T) {
	m, db := forestModel(t)
	fileSuggestion(t, db, database.Suggestion{UUID: "s1", Kind: database.SuggestChange,
		TargetUUID: "b1", Fields: database.FieldDone, CreatedOn: 1})
	fileSuggestion(t, db, database.Suggestion{UUID: "s2", Kind: database.SuggestChange,
		TargetUUID: "b2", Fields: database.FieldDone, CreatedOn: 2})
	if _, err := db.Exec("UPDATE nodes SET deleted = 1 WHERE uuid = 'b1'"); err != nil {
		t.Fatal(err)
	}
	loadAndRefresh(m)
	if m.pendingSuggestCount() != 2 {
		t.Fatalf("queue size = %d", m.pendingSuggestCount())
	}

	m.gotoSuggestion()

	// the deleted target was cleaned, the walk landed on the live one
	if cur := m.cursorItem(); cur == nil || cur.uuid != "b2" {
		t.Fatalf("cursor did not land on b2: %+v (flash %q)", cur, m.flash)
	}
	if m.pendingSuggestCount() != 1 {
		t.Fatalf("queue size after cleanup = %d, want 1", m.pendingSuggestCount())
	}
	var status string
	database.MustScan(t, "checking cleaned status",
		db.QueryRow("SELECT status FROM suggestions WHERE uuid = 's1'"), &status)
	if status != database.SuggestRejected {
		t.Fatalf("cleaned suggestion status = %q, want rejected", status)
	}
	database.MustScan(t, "checking surviving status",
		db.QueryRow("SELECT status FROM suggestions WHERE uuid = 's2'"), &status)
	if status != database.SuggestPending {
		t.Fatalf("live suggestion status = %q, want pending", status)
	}
}

// TestGotoSuggestionCleansAllWhenEverythingIsZombie: a queue of nothing but
// ghosts settles it all and reports the cleanup instead of dead-ending.
func TestGotoSuggestionCleansAllWhenEverythingIsZombie(t *testing.T) {
	m, db := forestModel(t)
	fileSuggestion(t, db, database.Suggestion{UUID: "s1", Kind: database.SuggestChange,
		TargetUUID: "b1", Fields: database.FieldDone, CreatedOn: 1})
	fileSuggestion(t, db, database.Suggestion{UUID: "s2", Kind: database.SuggestChange,
		TargetUUID: "b2", Fields: database.FieldDone, CreatedOn: 2})
	if _, err := db.Exec("UPDATE nodes SET deleted = 1"); err != nil {
		t.Fatal(err)
	}
	loadAndRefresh(m)

	m.gotoSuggestion()

	if m.pendingSuggestCount() != 0 {
		t.Fatalf("queue size = %d, want 0", m.pendingSuggestCount())
	}
	if !strings.Contains(m.flash, "cleaned") {
		t.Fatalf("flash = %q, want a cleaned report", m.flash)
	}
}

func equalStrs(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestSuggestShowsOnBlockFacedTypes: a Code node's row IS its block, so the
// proposal hangs as a line under the block instead of a row suffix. Every other
// type gets it through the shared row suffix.
func TestSuggestShowsOnBlockFacedTypes(t *testing.T) {
	for _, typ := range []string{database.TypeCode, database.TypeNLPCompute} {
		t.Run(typ, func(t *testing.T) {
			m, db, cur := suggestModel(t, "func main() {}")
			cur.typ = typ
			if _, err := db.Exec("UPDATE nodes SET type = ? WHERE uuid = ?", typ, cur.uuid); err != nil {
				t.Fatal(err)
			}
			fileSuggestion(t, db, database.Suggestion{Kind: database.SuggestChange,
				TargetUUID: cur.uuid, Name: "func main() { run() }", Fields: database.FieldName,
				BaseName: "func main() {}", Author: "agent"})
			loadAndRefresh(m)

			lines := stripSGR(strings.Join(m.suggestBlockLines(m.rows[m.rowIndexOf(cur)], false, 120), "\n"))
			if !strings.Contains(lines, "run()") || !strings.Contains(lines, "⌥y/⌥n") {
				t.Fatalf("%s block carried no answerable proposal: %q", typ, lines)
			}
			glyph, color := m.suggestGlyph(cur, glyphOpen, cDim)
			if glyph != glyphSuggest || color != cYellow {
				t.Fatalf("%s glyph = %q/%q, want a yellow outline circle", typ, glyph, color)
			}
		})
	}
}

// TestSuggestShowsOnRowTypes: every row-shaped type carries the proposal through
// the same suffix, so a table or a heading reads like a bullet.
func TestSuggestShowsOnRowTypes(t *testing.T) {
	for _, typ := range []string{database.TypeTable, database.TypeH1, database.TypeTodo,
		database.TypeQuery, database.TypeMath, database.TypeJSON} {
		t.Run(typ, func(t *testing.T) {
			m, db, cur := suggestModel(t, "quarter plan")
			cur.typ = typ
			if _, err := db.Exec("UPDATE nodes SET type = ? WHERE uuid = ?", typ, cur.uuid); err != nil {
				t.Fatal(err)
			}
			fileSuggestion(t, db, database.Suggestion{UUID: "s1", Kind: database.SuggestAdd,
				TargetUUID: cur.uuid, Name: "add a risks column", Author: "agent"})
			loadAndRefresh(m)

			i := ghostRow(m, "s1")
			if i < 0 {
				t.Fatalf("%s parent grew no ghost row", typ)
			}
			if plain := stripSGR(rowLine(m, i)); !strings.Contains(plain, "add a risks column") {
				t.Fatalf("%s ghost row carried no proposal: %q", typ, plain)
			}
		})
	}
}

// TestSuggestToolbarCountHasNoCircle: the bar counts what waits, without the
// glyph — the circle belongs on the node.
func TestSuggestToolbarCountHasNoCircle(t *testing.T) {
	m, db, cur := suggestModel(t, "ship the thing")
	fileSuggestion(t, db, database.Suggestion{Kind: database.SuggestChange, TargetUUID: cur.uuid,
		Name: "ship the other thing", Fields: database.FieldName, BaseName: "ship the thing"})
	loadAndRefresh(m)

	bar := stripSGR(strings.Join(m.bottomBar(80), "\n"))
	if !strings.Contains(bar, "1 suggestion") {
		t.Fatalf("bar dropped the count: %q", bar)
	}
	if strings.Contains(bar, glyphSuggest+" 1 suggestion") {
		t.Fatalf("bar still wears the circle: %q", bar)
	}
}

// TestSuggestSurvivesTheTempPanel: stepping into the Temporary Domain renders
// the main outline through the read-only region — a proposal must still mark its
// node there, or it looks like it went away.
func TestSuggestSurvivesTheTempPanel(t *testing.T) {
	m, db, cur := suggestModel(t, "ship the thing")
	fileSuggestion(t, db, database.Suggestion{Kind: database.SuggestChange, TargetUUID: cur.uuid,
		Name: "ship the other thing", Fields: database.FieldName, BaseName: "ship the thing",
		Author: "agent"})
	loadAndRefresh(m)

	lines := m.readonlyRegionLines(m.tree, m.tree.root, 0, 6, 80, false, -1)
	joined := stripSGR(strings.Join(lines, "\n"))
	if !strings.Contains(joined, glyphSuggest+" ship the thing") {
		t.Fatalf("read-only region dropped the node's circle: %q", joined)
	}
}
