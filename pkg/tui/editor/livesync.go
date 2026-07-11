package editor

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/lflow/lflow/pkg/tui/database"
	"github.com/lflow/lflow/pkg/tui/wire"
)

// Live sync: the editor is one client of the lflow daemon among many. Its
// edits flush automatically (debounced ~1s, "syncing" in the bar — there is
// no unsaved state to lose anymore), and every other client's committed
// change arrives on the subscribe feed and folds into the in-memory tree in
// place. Conflict policy is errorless last-writer-wins at node granularity,
// with one shield: a node the user has DIRTY (edited since the last flush)
// never adopts a remote version — the local flush lands ~1s later and wins
// globally. Concurrent edits to different nodes lose nothing, ever.

// syncEvery is the flush debounce: how long after an edit its ops ship.
const syncEvery = time.Second

// feedRetryEvery paces resubscribe attempts after the feed drops.
const feedRetryEvery = 2 * time.Second

type daemonEvMsg struct{ ev wire.Event }
type daemonFeedClosedMsg struct{}
type feedRetryMsg struct{}
type syncFlushMsg struct{}

// waitDaemonEv parks on the subscribe feed and delivers the next event.
func waitDaemonEv(ch <-chan wire.Event) tea.Cmd {
	return func() tea.Msg {
		ev, ok := <-ch
		if !ok {
			return daemonFeedClosedMsg{}
		}
		return daemonEvMsg{ev: ev}
	}
}

func feedRetryTick() tea.Cmd {
	return tea.Tick(feedRetryEvery, func(time.Time) tea.Msg { return feedRetryMsg{} })
}

// startFeed opens (or reopens) the subscription. Returns false when the
// daemon is unreachable; the caller schedules a retry.
func (m *Model) startFeed() bool {
	if m.live == nil {
		return false
	}
	if m.feedCancel != nil {
		m.feedCancel()
		m.feedCancel = nil
	}
	ch, cancel, err := m.live.Subscribe()
	if err != nil {
		m.liveFeed = nil
		return false
	}
	m.liveFeed = ch
	m.feedCancel = cancel
	return true
}

// scheduleSync arms the debounced auto-flush after an edit. With no daemon
// (tests, LFLOW_NO_DAEMON) editing keeps the classic save-on-ctrl+s model.
func (m *Model) scheduleSync() tea.Cmd {
	if m.live == nil || !m.unsaved || m.syncPending {
		return nil
	}
	m.syncPending = true
	return tea.Tick(syncEvery, func(time.Time) tea.Msg { return syncFlushMsg{} })
}

// flushSync ships local edits to the daemon. The bar's "syncing" clears with
// m.unsaved; on failure the edits stay local and the next edit retries.
func (m *Model) flushSync() tea.Cmd {
	m.syncPending = false
	if !m.unsaved {
		return nil
	}
	written, err := m.saveAll()
	if err != nil {
		m.flash = "sync: " + err.Error()
		return nil
	}
	m.saved.written += written
	m.unsaved = false
	return nil
}

// canApplyLive reports whether external events may fold in right now. While
// a picker, note edit, multi-select, or focused mod view is holding
// positional state, events queue in pendingEvs instead.
func (m *Model) canApplyLive() bool {
	return m.mode == modeOutline && !m.focused && !m.selOn
}

// maxPendingEvs bounds the deferred-event queue; past it the editor resyncs
// wholesale instead of replaying.
const maxPendingEvs = 512

// handleDaemonEv is the Update arm for one feed event.
func (m *Model) handleDaemonEv(ev wire.Event) {
	if m.live != nil && ev.Instance == m.live.Instance() {
		return // our own write echoed back
	}
	if ev.Resync {
		m.needResync = true
		return
	}
	if !m.canApplyLive() {
		if len(m.pendingEvs) >= maxPendingEvs {
			m.needResync = true
			m.pendingEvs = nil
		} else {
			m.pendingEvs = append(m.pendingEvs, ev)
		}
		return
	}
	m.applyEvent(ev)
}

// drainLive folds in whatever queued while a modal surface was open. Called
// from the key path once the editor is back in plain outline mode.
func (m *Model) drainLive() {
	if !m.canApplyLive() {
		return
	}
	if m.needResync {
		m.needResync = false
		m.pendingEvs = nil
		m.resync()
		return
	}
	for _, ev := range m.pendingEvs {
		m.applyEvent(ev)
	}
	m.pendingEvs = nil
}

// applyEvent merges one committed external change into the loaded trees.
// Nodes arrive unordered (map-collected), so inserts whose parent is later
// in the same event retry over a few passes.
func (m *Model) applyEvent(ev wire.Event) {
	if ev.Aux {
		m.reloadAux()
	}
	pending := ev.Nodes
	changed := false
	for pass := 0; pass < 4 && len(pending) > 0; pass++ {
		var next []database.Node
		for _, n := range pending {
			applied, mutated := m.applyNode(n)
			if !applied {
				next = append(next, n)
				continue
			}
			changed = changed || mutated
		}
		if len(next) == len(pending) {
			break // no progress: the rest lives outside the loaded trees
		}
		pending = next
	}
	if !changed {
		return
	}

	// external structure invalidates the undo stack: undoing across it would
	// resurrect or tombstone the other client's work
	m.undoStack = nil
	m.undoMark = ""

	cur := m.cursorItem()
	m.refreshRows()
	if cur != nil {
		if it, ok := m.tree.byUUID[cur.uuid]; ok {
			m.cursor = m.rowIndexOf(it)
		}
	}
	m.clampCursor()
	m.clampCaret()
	m.refreshAncestors()
}

// liveTrees lists the loaded trees external nodes may belong to: the active
// one first, then the other of main/temp. The ephemeral fallback temp tree
// (nil db) never syncs.
func (m *Model) liveTrees() []*tree {
	ts := []*tree{m.tree}
	other := m.tempTree
	if m.tempActive {
		other = m.mainStash.tree
	}
	if other != nil && other != m.tree && other.db != nil {
		ts = append(ts, other)
	}
	return ts
}

// applyNode routes one external node to the tree that owns (or gains) it.
// applied=false means "parent not loaded yet, retry this event pass".
func (m *Model) applyNode(n database.Node) (applied, mutated bool) {
	for _, t := range m.liveTrees() {
		if _, ok := t.byUUID[n.UUID]; ok {
			return true, t.applyExternal(n)
		}
	}
	if n.Deleted {
		return true, false // not loaded anywhere: nothing to remove
	}
	for _, t := range m.liveTrees() {
		if _, ok := t.byUUID[n.ParentUUID]; ok {
			return true, t.applyExternal(n)
		}
	}
	return false, false
}

// snapFromNode records a fresh DB row as a snapshot, so the next save diffs
// local edits against what is actually persisted.
func snapFromNode(n database.Node) snapshot {
	return snapshot{
		parentUUID:  n.ParentUUID,
		rank:        n.Rank,
		name:        n.Name,
		note:        n.Note,
		typ:         n.Type,
		style:       n.Style,
		mirrorOf:    n.MirrorOf,
		completedAt: n.CompletedAt,
		collapsed:   n.Collapsed,
		readonly:    n.Readonly,
		starred:     n.Starred,
	}
}

// applyExternal folds one fresh DB row into this tree in place, preserving
// item pointers so every uuid-keyed store (runs, threads, nodeData) and the
// cursor survive. Reports whether anything visible changed.
func (t *tree) applyExternal(n database.Node) bool {
	it, exists := t.byUUID[n.UUID]

	if n.Deleted {
		if !exists || it == t.root {
			return false
		}
		t.dropSubtree(it)
		return true
	}

	if !exists {
		parent, ok := t.byUUID[n.ParentUUID]
		if !ok {
			return false
		}
		it = &item{
			uuid:        n.UUID,
			name:        n.Name,
			note:        n.Note,
			typ:         n.Type,
			style:       n.Style,
			mirrorOf:    n.MirrorOf,
			completedAt: n.CompletedAt,
			collapsed:   n.Collapsed,
			readonly:    n.Readonly,
			starred:     n.Starred,
			addedOn:     n.AddedOn,
			parent:      parent,
		}
		t.byUUID[n.UUID] = it
		t.insertChildAt(parent, clampIdx(n.Rank, len(parent.children)), it)
		t.snapshots[n.UUID] = snapFromNode(n)
		return true
	}

	oldSnap, hadSnap := t.snapshots[n.UUID]
	mutated := false

	// content: adopt unless the user has local unsaved edits on this node —
	// the dirty shield; the local flush lands within a second and wins
	if !t.changed(it) {
		if it.name != n.Name || it.note != n.Note || it.typ != n.Type ||
			it.style != n.Style || it.mirrorOf != n.MirrorOf ||
			it.completedAt != n.CompletedAt || it.readonly != n.Readonly ||
			it.starred != n.Starred {
			mutated = true
		}
		it.name, it.note, it.typ = n.Name, n.Note, n.Type
		it.style, it.mirrorOf = n.Style, n.MirrorOf
		it.completedAt, it.readonly, it.starred = n.CompletedAt, n.Readonly, n.Starred
	}

	// structure: adopt an external move only when the node has not moved
	// locally since its snapshot (same shield, structural flavor)
	if it != t.root && it.parent != nil {
		localParent, localRank := it.parent.uuid, indexOf(it)
		localMoved := !hadSnap || oldSnap.parentUUID != localParent || oldSnap.rank != localRank
		externMoved := n.ParentUUID != localParent || n.Rank != localRank
		if !localMoved && externMoved {
			if dest, ok := t.byUUID[n.ParentUUID]; ok && !inSubtree(dest, it) {
				detach(it)
				it.parent = dest
				t.insertChildAt(dest, clampIdx(n.Rank, len(dest.children)), it)
				mutated = true
			} else if !ok {
				// moved outside the loaded trees: it leaves this view
				t.dropSubtree(it)
				return true
			}
		}
	}

	t.snapshots[n.UUID] = snapFromNode(n)
	return mutated
}

// dropSubtree detaches an item and forgets its subtree — an external delete
// or move-away. Unlike remove(), nothing is queued for tombstoning: the DB
// already reflects it.
func (t *tree) dropSubtree(it *item) {
	detach(it)
	var forget func(x *item)
	forget = func(x *item) {
		delete(t.byUUID, x.uuid)
		delete(t.snapshots, x.uuid)
		for _, c := range x.children {
			forget(c)
		}
	}
	forget(it)
}

func detach(it *item) {
	if idx := indexOf(it); idx >= 0 {
		p := it.parent
		p.children = append(p.children[:idx], p.children[idx+1:]...)
	}
}

// inSubtree reports whether p sits inside root's subtree (cycle guard for
// external reparents racing local moves).
func inSubtree(p, root *item) bool {
	for ; p != nil; p = p.parent {
		if p == root {
			return true
		}
	}
	return false
}

func clampIdx(i, n int) int {
	if i < 0 {
		return 0
	}
	if i > n {
		return n
	}
	return i
}

// reloadAux refreshes the render-support stores an aux event flags: chips,
// tag colors, painter spans. All tiny, reloaded wholesale.
func (m *Model) reloadAux() {
	if m.db == nil {
		return
	}
	if chips, err := database.LoadChips(m.db); err == nil {
		m.chips = chips
	}
	if tc, err := database.AllTagColors(m.db); err == nil {
		tagColors = tc
	}
	if sp, err := database.AllNodeSpans(m.db); err == nil {
		nodeSpans = sp
	}
}

// resync is the wholesale fallback: flush local edits, reload both trees
// fresh, and re-anchor the view by uuid. Used when the feed dropped (events
// may have been missed) or the deferred queue overflowed.
func (m *Model) resync() {
	if _, err := m.saveAll(); err != nil {
		m.flash = "sync: " + err.Error()
		return
	}
	m.unsaved = false

	mainTree := m.tree
	if m.tempActive {
		mainTree = m.mainStash.tree
	}
	freshMain, err := loadTree(m.db, mainTree.root.uuid)
	if err != nil {
		m.flash = "sync: " + err.Error()
		return
	}
	var freshTemp *tree
	if m.tempTree != nil && m.tempTree.db != nil {
		if ft, err := loadTree(m.db, database.TempUUID); err == nil {
			freshTemp = ft
		}
	}

	cur := m.cursorItem()
	var curUUID string
	if cur != nil {
		curUUID = cur.uuid
	}

	if m.tempActive {
		m.mainStash.tree = freshMain
		m.mainStash.viewStack = remapStack(m.mainStash.viewStack, freshMain)
		if freshTemp != nil {
			m.tree = freshTemp
			m.tempTree = freshTemp
			m.viewStack = remapStack(m.viewStack, freshTemp)
		}
	} else {
		m.tree = freshMain
		m.viewStack = remapStack(m.viewStack, freshMain)
		if freshTemp != nil {
			m.tempTree = freshTemp
		}
	}

	m.undoStack = nil
	m.undoMark = ""
	m.refreshAncestors()
	m.refreshRows()
	if it, ok := m.tree.byUUID[curUUID]; ok && curUUID != "" {
		m.cursor = m.rowIndexOf(it)
	}
	m.clampCursor()
	m.clampCaret()
}

// remapStack rebuilds a zoom stack against a freshly loaded tree by uuid,
// truncating at the first vanished node.
func remapStack(stack []*item, t *tree) []*item {
	out := []*item{t.root}
	for _, it := range stack {
		if it == nil || it.uuid == t.root.uuid {
			continue
		}
		f, ok := t.byUUID[it.uuid]
		if !ok {
			break
		}
		out = append(out, f)
	}
	return out
}
