package editor

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/lflow/lflow/pkg/tui/context"
	"github.com/lflow/lflow/pkg/tui/wf"
)

// Background mirror scheduler. While the editor is open, workflowy-mirror
// anchors that are VISIBLE on screen sync every 5s; anchors in the loaded
// tree but currently off-screen sync every 60s; an anchor syncs once
// immediately the first time it becomes visible. Nothing is ever displayed
// (no countdowns); only a dim " · syncing" state in the bottom bar while a
// run is actually in flight. The scheduler ticks at 1s resolution and decides
// due anchors per tick from time.Since(lastRun).
const (
	syncVisibleEvery = 5 * time.Second
	syncHiddenEvery  = 60 * time.Second
	syncTickEvery    = time.Second
)

// scheduler holds the background-sync state on the Model.
type scheduler struct {
	enabled  bool // a valid workflowy session was detected at startup
	client   wf.Client
	journal  wf.Journal
	inFlight bool // a sync is running; skip ticks until it finishes
	lastRun  map[string]time.Time
	everSeen map[string]bool
	err      error // last background error, kept for debugging only
}

// syncTickMsg fires every second; it decides which anchor (if any) is due.
type syncTickMsg time.Time

// syncDoneMsg reports the outcome of one background sync.
type syncDoneMsg struct {
	res wf.SyncResult
	err error
}

// initScheduler detects the workflowy session once. A missing/invalid session
// disables the scheduler entirely; building the client never fails fatally.
func (m *Model) initScheduler(ctx context.DnoteCtx) {
	m.sched.lastRun = map[string]time.Time{}
	m.sched.everSeen = map[string]bool{}
	client, err := wf.ClientFromCtx(ctx)
	if err != nil {
		return // not logged in: scheduler stays disabled
	}
	m.sched.client = client
	m.sched.journal = wf.JournalFromCtx(ctx)
	m.sched.enabled = true
}

// schedulerInit returns the first tick command, or nil when disabled.
func (m *Model) schedulerInit() tea.Cmd {
	if !m.sched.enabled {
		return nil
	}
	return syncTick()
}

func syncTick() tea.Cmd {
	return tea.Tick(syncTickEvery, func(t time.Time) tea.Msg { return syncTickMsg(t) })
}

// loadedMirrorAnchors returns the anchor uuids whose node is in the loaded
// tree, paired with whether each is currently visible in the viewport.
func (m *Model) loadedMirrorAnchors() map[string]bool {
	out := map[string]bool{}
	if m.tree == nil {
		return out
	}
	mirrors, err := wf.GetMirrors(m.db)
	if err != nil {
		return out
	}
	visible := m.visibleUUIDs()
	for _, mr := range mirrors {
		if _, inTree := m.tree.byUUID[mr.NodeUUID]; inTree {
			out[mr.NodeUUID] = visible[mr.NodeUUID]
		}
	}
	return out
}

// visibleUUIDs is the set of item uuids in the currently rendered viewport
// slice of m.rows (the same window viewOutline draws). The zoom chain counts
// as visible too: the view root is on screen (it is the bar title) even
// though visibleRows never emits it as a row — opening the editor directly
// on a mirror anchor is the common case and must get the 5s cadence.
func (m *Model) visibleUUIDs() map[string]bool {
	out := map[string]bool{}
	start, end := m.viewport()
	for i := start; i < end && i < len(m.rows); i++ {
		out[m.rows[i].it.uuid] = true
	}
	for _, it := range m.viewStack {
		out[it.uuid] = true
	}
	return out
}

// onSyncTick picks the single due anchor (if any) and starts a sync. It always
// schedules the next tick. A sync already in flight short-circuits the tick.
func (m *Model) onSyncTick(now time.Time) tea.Cmd {
	if !m.sched.enabled {
		return nil
	}
	if m.sched.inFlight {
		return syncTick()
	}

	anchors := m.loadedMirrorAnchors()
	anchor, ok := m.dueAnchor(anchors, now)
	if !ok {
		return syncTick()
	}

	// persist in-memory edits first: the sync goroutine reads the DB, so
	// without this autosave local typing would never reach workflowy
	if m.unsaved {
		if written, err := m.tree.save(); err == nil {
			m.saved.written += written
			m.unsaved = false
		} else {
			m.sched.err = err
			return syncTick()
		}
	}

	m.sched.inFlight = true
	m.sched.lastRun[anchor] = now
	return tea.Batch(m.runSync(anchor, now), syncTick())
}

// dueAnchor returns the first anchor whose cadence has elapsed. An anchor that
// just became visible (not seen before, now visible) is due immediately;
// otherwise visible anchors use the 5s cadence and hidden ones the 60s cadence.
func (m *Model) dueAnchor(anchors map[string]bool, now time.Time) (string, bool) {
	for uuid, vis := range anchors {
		last, ran := m.sched.lastRun[uuid]
		if vis && !m.sched.everSeen[uuid] {
			m.sched.everSeen[uuid] = true
			return uuid, true // first time visible: sync now, then join 5s cadence
		}
		if vis {
			m.sched.everSeen[uuid] = true
		}
		if !ran {
			// off-screen anchor never seen visible: start its 60s clock now
			// without an immediate sync (only visible anchors sync on sight)
			if !vis {
				m.sched.lastRun[uuid] = now
			}
			continue
		}
		every := syncHiddenEvery
		if vis {
			every = syncVisibleEvery
		}
		if now.Sub(last) >= every {
			return uuid, true
		}
	}
	return "", false
}

// runSync performs one anchor sync off the bubbletea goroutine so typing is
// never blocked. Errors are swallowed (remembered on the model only).
func (m *Model) runSync(anchor string, now time.Time) tea.Cmd {
	syncer := &wf.Syncer{DB: m.db, Client: m.sched.client, Journal: m.sched.journal}
	return func() tea.Msg {
		res, err := syncer.Sync(anchor, now.Unix())
		return syncDoneMsg{res: res, err: err}
	}
}

// onSyncDone clears the in-flight flag and, when changes were actually pulled
// or pushed, reloads the tree from the DB preserving the UI state.
func (m *Model) onSyncDone(msg syncDoneMsg) {
	m.sched.inFlight = false
	if msg.err != nil {
		m.sched.err = msg.err
		return
	}
	if msg.res.Pulled == 0 && msg.res.Pushed == 0 {
		return // nothing changed: avoid a needless save+reload
	}
	m.reloadAfterSync()
}

// reloadAfterSync persists the in-memory tree, reloads it from the DB (which
// now holds the synced state), and restores cursor, caret, zoom stack and
// collapsed state by uuid. We save first because the sync wrote to the DB
// directly; reloading without saving would drop unsaved local edits.
func (m *Model) reloadAfterSync() {
	rootUUID := m.tree.root.uuid

	// capture the state to restore, keyed by uuid
	cursorUUID := ""
	if it := m.cursorItem(); it != nil {
		cursorUUID = it.uuid
	}
	caret := m.caret
	zoomUUIDs := make([]string, len(m.viewStack))
	for i, it := range m.viewStack {
		zoomUUIDs[i] = it.uuid
	}
	collapsed := map[string]bool{}
	for uuid, it := range m.tree.byUUID {
		if it.collapsed {
			collapsed[uuid] = true
		}
	}

	if _, err := m.tree.save(); err != nil {
		m.sched.err = err
		return
	}
	t, err := loadTree(m.db, rootUUID)
	if err != nil {
		m.sched.err = err
		return
	}
	m.tree = t

	// restore collapsed state
	for uuid := range collapsed {
		if it, ok := t.byUUID[uuid]; ok {
			it.collapsed = true
		}
	}

	// restore the zoom stack, dropping levels whose nodes vanished
	m.viewStack = m.viewStack[:0]
	for _, uuid := range zoomUUIDs {
		if uuid == rootUUID {
			m.viewStack = append(m.viewStack, t.root)
			continue
		}
		if it, ok := t.byUUID[uuid]; ok {
			m.viewStack = append(m.viewStack, it)
		}
	}
	if len(m.viewStack) == 0 {
		m.viewStack = []*item{t.root}
	}

	m.refreshRows()

	// restore the cursor by uuid, else clamp
	if cursorUUID != "" {
		if idx := m.rowIndexOfUUID(cursorUUID); idx >= 0 {
			m.cursor = idx
		}
	}
	m.caret = caret
	m.clampCaret()
}

// rowIndexOfUUID finds the visible-row index of a node by uuid, or -1.
func (m *Model) rowIndexOfUUID(uuid string) int {
	for i, r := range m.rows {
		if r.it.uuid == uuid {
			return i
		}
	}
	return -1
}
