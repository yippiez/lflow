package editor

import (
	"testing"

	"github.com/lflow/lflow/packages/daemon/wire"
	"github.com/lflow/lflow/packages/database"
)

// liveTestModel builds a model the way loadTree would: uuids and snapshots
// consistent with the "DB", so the live-merge dirty shield has real state.
func liveTestModel(names ...string) *Model {
	root := &item{uuid: "r"}
	t := &tree{
		root:          root,
		byUUID:        map[string]*item{"r": root},
		snapshots:     map[string]snapshot{"r": {}},
		externalNames: map[string]string{},
	}
	for i, n := range names {
		it := &item{uuid: "u-" + n, name: n, typ: database.TypeBullets, parent: root}
		root.children = append(root.children, it)
		t.byUUID[it.uuid] = it
		t.snapshots[it.uuid] = snapshot{parentUUID: "r", rank: i, name: n, typ: database.TypeBullets}
	}
	m := &Model{tree: t, viewStack: []*item{root}, width: 80, height: 24}
	m.refreshRows()
	return m
}

func node(uuid, parent string, rank int, name string) database.Node {
	return database.Node{UUID: uuid, ParentUUID: parent, Rank: rank, Name: name, Type: database.TypeBullets}
}

func rowNames(m *Model) []string {
	var out []string
	for _, r := range m.rows {
		out = append(out, r.it.name)
	}
	return out
}

func TestLiveInsertAppears(t *testing.T) {
	m := liveTestModel("a", "b")
	m.applyEvent(wire.Event{Nodes: []database.Node{node("u-new", "r", 1, "from-cli")}})

	got := rowNames(m)
	want := []string{"a", "from-cli", "b"}
	for i := range want {
		if i >= len(got) || got[i] != want[i] {
			t.Fatalf("rows = %v, want %v", got, want)
		}
	}
	// the merged node is persisted state, not a local edit
	if it := m.tree.byUUID["u-new"]; it.isNew || m.tree.changed(it) {
		t.Fatal("external node marked as local edit")
	}
}

func TestLiveRenameAdoptsWhenClean(t *testing.T) {
	m := liveTestModel("a", "b")
	m.applyEvent(wire.Event{Nodes: []database.Node{node("u-a", "r", 0, "a-renamed")}})
	if m.tree.byUUID["u-a"].name != "a-renamed" {
		t.Fatalf("clean node did not adopt external rename: %q", m.tree.byUUID["u-a"].name)
	}
}

func TestLiveDirtyShieldKeepsLocalEdit(t *testing.T) {
	m := liveTestModel("a", "b")
	it := m.tree.byUUID["u-a"]
	it.name = "a-typed-locally" // unsaved local edit → dirty vs snapshot

	m.applyEvent(wire.Event{Nodes: []database.Node{node("u-a", "r", 0, "a-from-agent")}})

	if it.name != "a-typed-locally" {
		t.Fatalf("dirty shield failed: local edit clobbered to %q", it.name)
	}
	// the snapshot now reflects the external DB row, so the next flush still
	// sees the local edit as a change and writes it (local wins globally)
	if !m.tree.changed(it) {
		t.Fatal("local edit no longer flagged for flush")
	}
}

func TestLiveTombstoneRemovesSubtree(t *testing.T) {
	m := liveTestModel("a", "b")
	// give a a child
	child := &item{uuid: "u-a1", name: "a1", parent: m.tree.byUUID["u-a"]}
	m.tree.byUUID["u-a"].children = append(m.tree.byUUID["u-a"].children, child)
	m.tree.byUUID["u-a1"] = child
	m.tree.snapshots["u-a1"] = snapshot{parentUUID: "u-a", rank: 0, name: "a1"}
	m.refreshRows()

	dead := node("u-a", "r", 0, "a")
	dead.Deleted = true
	m.applyEvent(wire.Event{Nodes: []database.Node{dead}})

	if _, ok := m.tree.byUUID["u-a"]; ok {
		t.Fatal("tombstoned node still in byUUID")
	}
	if _, ok := m.tree.byUUID["u-a1"]; ok {
		t.Fatal("tombstoned subtree child still in byUUID")
	}
	// nothing queued for tombstoning: the DB already did it
	if len(m.tree.deleted) != 0 {
		t.Fatalf("external delete queued local tombstones: %v", m.tree.deleted)
	}
	if got := rowNames(m); len(got) != 1 || got[0] != "b" {
		t.Fatalf("rows = %v, want [b]", got)
	}
}

func TestLiveMoveAppliesWhenLocalUnmoved(t *testing.T) {
	m := liveTestModel("a", "b", "c")
	// external writer moved c under a
	m.applyEvent(wire.Event{Nodes: []database.Node{node("u-c", "u-a", 0, "c")}})

	c := m.tree.byUUID["u-c"]
	if c.parent == nil || c.parent.uuid != "u-a" {
		t.Fatalf("external move not applied; parent = %+v", c.parent)
	}
}

func TestLiveUnorderedParentChildInsert(t *testing.T) {
	m := liveTestModel("a")
	// child listed before its parent in the same event (map order is random)
	m.applyEvent(wire.Event{Nodes: []database.Node{
		node("u-p1c", "u-p1", 0, "child"),
		node("u-p1", "r", 1, "parent"),
	}})

	p := m.tree.byUUID["u-p1"]
	if p == nil {
		t.Fatal("parent not inserted")
	}
	c := m.tree.byUUID["u-p1c"]
	if c == nil || c.parent != p {
		t.Fatal("child not inserted under parent despite ordering")
	}
}

func TestLiveCursorStaysOnNode(t *testing.T) {
	m := liveTestModel("a", "b", "c")
	m.cursor = 2 // on c
	// an external insert above the cursor shifts rows
	m.applyEvent(wire.Event{Nodes: []database.Node{node("u-top", "r", 0, "top")}})

	if cur := m.cursorItem(); cur == nil || cur.uuid != "u-c" {
		t.Fatalf("cursor drifted: %+v", cur)
	}
}

func TestLiveEventsDeferWhileModalOpen(t *testing.T) {
	m := liveTestModel("a")
	m.mode = modeNote // a modal surface holds positional state

	m.handleDaemonEv(wire.Event{Nodes: []database.Node{node("u-n", "r", 1, "queued")}})
	if _, ok := m.tree.byUUID["u-n"]; ok {
		t.Fatal("event applied while modal open")
	}
	if len(m.pendingEvs) != 1 {
		t.Fatalf("pendingEvs = %d, want 1", len(m.pendingEvs))
	}

	m.mode = modeOutline
	m.drainLive()
	if _, ok := m.tree.byUUID["u-n"]; !ok {
		t.Fatal("queued event not applied after modal closed")
	}
	if len(m.pendingEvs) != 0 {
		t.Fatal("pending queue not drained")
	}
}

// TestFileSessionSchedulesAutoFlush: a file session (m.live == nil, onSave
// set) must still arm the debounced auto-flush after an edit — the file-open
// path has no daemon to gate on, but its OnSave hook needs the same ~1s
// flush or a crash loses everything since the last explicit ctrl+s.
func TestFileSessionSchedulesAutoFlush(t *testing.T) {
	m := liveTestModel("a")
	saved := 0
	m.onSave = func() error { saved++; return nil }
	m.unsaved = true

	if m.live != nil {
		t.Fatal("test setup: expected no live connection")
	}
	if cmd := m.scheduleSync(); cmd == nil {
		t.Fatal("scheduleSync returned nil for a file session with pending edits")
	}
	if !m.syncPending {
		t.Fatal("syncPending not armed for a file session")
	}

	// a second edit before the tick fires must not double-schedule
	if cmd := m.scheduleSync(); cmd != nil {
		t.Fatal("scheduleSync re-armed while already pending")
	}

	m.flushSync()
	if saved != 1 {
		t.Fatalf("onSave called %d times, want 1", saved)
	}
	if m.unsaved {
		t.Fatal("flushSync left m.unsaved set")
	}
	if m.syncPending {
		t.Fatal("flushSync left syncPending set")
	}
}

func TestLiveUndoDropsOnExternalChange(t *testing.T) {
	m := liveTestModel("a", "b")
	m.pushUndo("")
	if len(m.undoStack) != 1 {
		t.Fatal("undo not pushed")
	}
	m.applyEvent(wire.Event{Nodes: []database.Node{node("u-x", "r", 2, "external")}})
	if len(m.undoStack) != 0 {
		t.Fatal("undo stack survived an external merge; undoing would tombstone the external node")
	}
}
