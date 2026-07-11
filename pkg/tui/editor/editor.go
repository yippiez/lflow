// Package editor implements the inline scrollback-mode outline editor:
// black background, muted gray ○/●/◆/□ glyphs and connectors plus 1/2/3
// heading digits, the selected row marked by its glyph turning red, a block
// cursor that inverts the cell beneath it, a minimal dim bottom bar, a
// type-to-filter slash menu above the bar, and a full-panel fuzzy finder for
// /mirror:to /mirror:from /move:to /move:here /goto. It never enters the alternate screen.
package editor

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/lflow/lflow/pkg/agent"
	"github.com/lflow/lflow/pkg/tui/client"
	"github.com/lflow/lflow/pkg/tui/consts"
	"github.com/lflow/lflow/pkg/tui/context"
	"github.com/lflow/lflow/pkg/tui/database"
	"github.com/lflow/lflow/pkg/tui/tag"
	"github.com/lflow/lflow/pkg/tui/wf"
	"github.com/lflow/lflow/pkg/tui/wire"
	"github.com/mattn/go-runewidth"
	"github.com/pkg/errors"
)

type mode int

const (
	modeOutline mode = iota
	modeSlash
	modeFinder
	modeNote
	modeConfirm  // inline delete confirmation for nodes with children
	modeType     // the /type picker: choose one of the node types
	modeStyle    // the /style picker: toggle bold, italic, underline, strikethrough, color
	modeTheme    // the /theme picker: choose a color palette
	modeSettings // the /settings picker: global preferences (theme, image preview, …)
	modeComplete // the inline completer: "#" tags, ":" query commands
	modeLinkEdit // the alt+e link-chip editor: edit a link's name and target
	modeFlash    // flash jump/act: every visible row's actions get a typed label (see flash.go)
	modeTagColor // the alt+e tag color picker: assign a pill color to a tag
	modePaint    // the painter: a window over the node's text places a /style choice (p inside /style)
)

type finderAction int

const (
	actMirrorHere finderAction = iota // /mirror:to — a mirror of the picked node lands at the cursor
	actMirrorFrom                     // /mirror:from — a mirror of the cursor node lands under the picked node
	actMoveTo                         // /move:to — the cursor node moves under the picked node
	actGoto
	actBringHere  // /move:here — the picked node moves to the cursor
	actLinkInsert // [[ — insert an inline link chip at the caret (node or URL)
)

type slashCommand struct {
	name string
	desc string
}

var slashCommands = []slashCommand{
	{"/complete", "Toggle done (alt+enter)"},
	{"/duplicate", "Duplicate this node and its subtree next to it"},
	{"/goto", "Jump the editor to another node"},
	{"/hide:complete", "Hide or show completed nodes"},
	{"/link", "Insert an inline [[ link to a node or URL"},
	{"/lock", "Lock or unlock this node as read-only"},
	{"/mirror:from", "Mirror this node under another node"},
	{"/mirror:to", "Mirror another node here"},
	{"/move:here", "Move another node here"},
	{"/move:to", "Move this node under another node"},
	{"/note", "Edit this node's note"},
	{"/settings", "Editor preferences: theme, image preview"},
	{"/star", "Star this node — ranks first in pickers and search hits"},
	{"/style", "Set this node's text style or color"},
	{"/type", "Set this node's type"},
	{"/undo", "Undo the last action"},
}

// stylePickerItem groups the text-attribute toggles and the color choices
// into a single /style picker list.
type stylePickerItem struct {
	kind  string // "toggle" or "color"
	value string
}

var stylePickerItems = []stylePickerItem{
	{"toggle", "bold"},
	{"toggle", "italic"},
	{"toggle", "underline"},
	{"toggle", "strike"},
	{"color", "red"},
	{"color", "orange"},
	{"color", "yellow"},
	{"color", "green"},
	{"color", "cyan"},
	{"color", "blue"},
	{"color", "purple"},
	{"color", "gray"},
}

var stylePickerLabels = map[string]string{
	"bold":      "Bold",
	"italic":    "Italic",
	"underline": "Underline",
	"strike":    "Strikethrough",
	"red":       "Red",
	"orange":    "Orange",
	"yellow":    "Yellow",
	"green":     "Green",
	"cyan":      "Cyan",
	"blue":      "Blue",
	"purple":    "Purple",
	"gray":      "Gray",
}

// Model is the bubbletea model for the editor.
type Model struct {
	db    *database.DB
	ctx   context.DnoteCtx // for config and node context
	tree  *tree
	chips map[string]database.Chip // inline chip records, keyed by id (see chip.go)

	viewStack []*item // zoom stack; last is the current view root
	cursor    int     // index into visibleRows
	caret     int     // rune index in the edited field
	rows      []row   // cached visible rows

	width  int
	height int
	// pageRows counts the frame's leading MAIN-region lines — the rows above
	// the status bar. A theme's page background (bgPage) paints exactly these;
	// the bar (divider) and the temp panel below it always stay transparent.
	pageRows int

	mode mode

	// list is the shared modal picker (slash, /type, /style, /theme, completer);
	// only one is active at a time. It owns the selection + search query; each
	// picker's behavior is a pickerSource (see picker_list.go / picker_sources.go).
	list listPicker

	slashStart  int  // rune index of the "/" that opened the menu
	slashInline bool // the slash and query are typed into the node text

	// finder is the shared full-body node picker (/mirror:to, /mirror:from,
	// /move:to, /move:here, /goto, "[[" link); it owns the query, selection, and
	// results (see picker_finder.go).
	finder bodyFinder

	notePrev string // note backup for esc in note mode

	// alt+e link-chip editor (modeLinkEdit)
	linkEditID     string // chip id being edited
	linkEditName   string // working copy of the link's display name
	linkEditTarget string // working copy of the link's target (URL or lflow://node/<uuid>)
	linkEditField  int    // 0 = name field, 1 = target field
	linkEditCaret  int    // caret inside the active field — same movement keys as the outline

	// the focused cmd chip (alt+e): its output renders as an inline band beneath
	// the node — the same surface as a focused bash node — keyed by this chip id.
	focusChip string

	// live cmd-chip draft gate: where the last text edit left the caret.
	// activeCmdDraftRange is purely positional, so without this gate merely
	// walking the caret into pre-existing "$…" prose (e.g. an agent reply
	// quoting a command) would tint it as a draft; the tint shows only while
	// the caret still sits where typing left it (see cmdDraftLive).
	cmdDraftUUID  string
	cmdDraftCaret int

	// flash mode (modeFlash): each visible row's actions carry a typed label;
	// typing a label narrows (matched prefix grays, the rest stays lit) until one
	// completes and fires. flashInput is the prefix typed so far. See flash.go.
	flashTargets []flashTarget
	flashInput   string

	// inline completer anchor ("#" tags, ":" query commands); the live query and
	// selection live on m.list.

	// /settings picker selection (index into settingDefs) + the loaded preferences
	settingsSel int
	settings    map[string]string

	compl complState

	// Shared RUN machinery — the generic spawn/stream/cancel infrastructure the
	// runnable node types use (bash, query, voice). Ephemeral, in-memory only,
	// keyed by node uuid (or cmd-chip id — same string keyspace). Run output is
	// NEVER in the DB or synced. One runState per id so every lifecycle event
	// (cancel/finish/delete) drops all of it atomically — see run/ensureRun.
	runs map[string]*runState

	// Temporary Domain — a scratch outline region (a second root, 7-day retention)
	tempActive bool
	tempTree   *tree
	mainStash  tempStash

	// inline expanded view: when focused, the cursor node's nodeView captures keys
	// and renders bands beneath it (replaces the per-feature full-screen modes).
	focused     bool // is the cursor node's inline view capturing input
	focusScroll int  // first visible line of the focused view's self-window

	// Manual viewport scroll (pgup/pgdown): scrolling pins the body window at
	// scrollTop instead of following the cursor — to read a long note/subtree that
	// runs past the footer without moving the cursor. Any other key clears the pin;
	// cursor-follow then keeps viewTop when the cursor is already on screen so a
	// page does not snap back on the next type or arrow. viewTop/viewRows cache
	// the last frame's window so a page step is relative to what is on screen.
	scrolling bool
	scrollTop int
	viewTop   int
	viewRows  int
	// nodeData is a generic ephemeral per-node store (uuid → key → value), never
	// persisted or synced — node views keep live/edit state here instead of
	// growing the Model with one named map per type.
	nodeData map[string]map[string]any

	// modPending holds tea.Cmds a mod view raised while entering (an Enter that
	// returns an initial effect) — Enter's nodeView signature can't return a Cmd,
	// so it queues here and the KeyMsg path drains it. See nodemod_view.go.
	modPending []tea.Cmd

	// @mention agent sessions (see agent.go and pkg/tui/tag): configured agents
	// and one client per agent (tagClients is keyed by agent NAME, a different
	// keyspace). Per-thread turn state (busy flag, cancel, live tool band, and
	// whether a node's mention already sent this session) lives in ONE
	// agentThread per uuid so a lifecycle event drops it all at once — see
	// thread/ensureThread. (alt+r starts/re-sends; Enter never starts a session.)
	agents     []tag.Agent
	tagClients map[string]tag.Client
	threads    map[string]*agentThread
	// agentErr is the last agent failure (backend missing, unknown @name, or a
	// turn error) — shown in the status bar as "Error: …" like the thinking
	// indicator, cleared when the next turn is fired. Never lands in the outline.
	agentErr string
	// blur-send state (see blurSendCheck): the item the cursor sat on at the
	// last key, and the item last typed into — leaving a typed node inside an
	// active thread ships it without waiting for Enter
	focusUUID string
	typedUUID string

	// Workflowy mirror (see wf.go and pkg/tui/wf): node uuid → workflowy id for
	// every pulled node, busy flags per pull root, and the API client (lazy;
	// tests inject one pointed at a mock server)
	wfMap    map[string]string
	wfBusy   map[string]bool
	wfClient *wf.Client

	// :tree: query breadcrumbs, memoized per source uuid (see query.go rowCrumb);
	// cleared whenever a query re-runs
	qCrumbs map[string]string

	tagColorWord string // the tag word the alt+e color picker is assigning

	// hideCompleted is the /hide:complete toggle: when true, completed nodes (and their
	// subtrees) drop out of the visible outline. Session-only — not persisted.
	hideCompleted bool

	// multi-select (see multisel.go): shift+up/down grows a row range from the
	// anchor; structural ops act on the selection roots
	selOn     bool
	selAnchor int

	// /undo: snapshots of the tree taken before each action
	undoStack []undoState
	undoMark  string

	// breadcrumb names above the loaded root, from the forest root down
	ancestors []string

	// live sync (see livesync.go): the daemon connection, its subscribe feed,
	// and the deferred-apply queue for events arriving while a modal surface
	// holds positional state. unsaved now means "edits not yet flushed" — the
	// debounced auto-flush ships them ~1s after typing pauses.
	live        *client.Client
	liveFeed    <-chan wire.Event
	feedCancel  func()
	syncPending bool // a flush tick is scheduled
	pendingEvs  []wire.Event
	needResync  bool

	escPending  bool
	unsaved     bool
	quitting    bool
	animTicking bool   // the magic-keyword animation tick is currently scheduled
	flash       string // one-shot status for the bottom bar, cleared on keypress
	err         error

	saved struct {
		written int
	}
}

func (m *Model) viewRoot() *item { return m.viewStack[len(m.viewStack)-1] }

// refreshAncestors recomputes the breadcrumb names above the loaded root by
// walking the db parent chain up to the forest root.
func (m *Model) refreshAncestors() {
	m.ancestors = nil
	base := m.viewStack[0]
	if m.db == nil || base == nil || base.uuid == "" || base.uuid == database.RootUUID {
		return
	}
	puuid := ""
	if n, err := database.GetNode(m.db, base.uuid); err == nil {
		puuid = n.ParentUUID
	}
	for puuid != "" && puuid != database.RootUUID {
		n, err := database.GetNode(m.db, puuid)
		if err != nil {
			break
		}
		name := displayAnchors(n.Name, m.chips) // resolve chip anchors for the breadcrumb
		if name == "" {
			name = "untitled"
		}
		m.ancestors = append([]string{name}, m.ancestors...)
		puuid = n.ParentUUID
	}
}

// saveAll persists both the Root tree and the Temp tree, regardless of which is
// focused, and returns the total nodes written. Temp is a real persisted subtree
// now, so it must be written alongside the main outline.
func (m *Model) saveAll() (int, error) {
	main, temp := m.tree, m.tempTree
	if m.tempActive {
		main, temp = m.mainStash.tree, m.tree
	}
	w := 0
	if main != nil {
		n, err := main.save()
		if err != nil {
			return w, err
		}
		w += n
	}
	if temp != nil {
		n, err := temp.save()
		if err != nil {
			return w, err
		}
		w += n
	}
	return w, nil
}

// reopenAt saves, reloads the tree rooted at rootUUID, and focuses focusUUID. It
// is how alt+left walks up past the loaded root into the rest of the forest.
func (m *Model) reopenAt(rootUUID, focusUUID string) {
	if _, err := m.saveAll(); err != nil {
		m.flash = "save: " + err.Error()
		return
	}
	m.unsaved = false
	t, err := loadTree(m.db, rootUUID)
	if err != nil {
		m.flash = err.Error()
		return
	}
	m.tree = t
	m.viewStack = []*item{t.root}
	m.undoStack = nil // a reload is a fresh editing context
	m.refreshAncestors()
	m.refreshRows()
	m.cursor = 0
	m.caret = 0
	if it, ok := t.byUUID[focusUUID]; ok {
		m.cursor = m.rowIndexOf(it)
	}
}

// undoState is a snapshot of the editable tree and cursor taken before an action.
type undoState struct {
	root    *item
	deleted []string
	view    []string // viewStack uuids
	cursor  int
	caret   int
}

// pushUndo snapshots the tree before a mutating action. The mark coalesces a run
// of same-kind edits — typing a word is one undo step, not one per character —
// by skipping the snapshot when the mark matches the previous one.
func (m *Model) pushUndo(mark string) {
	if mark != "" && mark == m.undoMark {
		return
	}
	m.undoMark = mark
	st := undoState{
		root:    cloneItem(m.tree.root, nil),
		deleted: append([]string(nil), m.tree.deleted...),
		view:    make([]string, len(m.viewStack)),
		cursor:  m.cursor,
		caret:   m.caret,
	}
	for i, v := range m.viewStack {
		st.view[i] = v.uuid
	}
	m.undoStack = append(m.undoStack, st)
	const maxUndo = 100
	if len(m.undoStack) > maxUndo {
		m.undoStack = m.undoStack[len(m.undoStack)-maxUndo:]
	}
}

// snapshotForKey pushes an undo snapshot before a mutating outline key. A run of
// typed characters on one node coalesces into a single undo step; each
// structural action is its own step.
func (m *Model) snapshotForKey(key string, k tea.KeyMsg) {
	cur := m.cursorItem()
	switch key {
	case "enter", "tab", "shift+tab",
		"alt+shift+up", "ctrl+shift+up", "ctrl+alt+up",
		"alt+shift+down", "ctrl+shift+down", "ctrl+alt+down",
		"ctrl+d", "alt+d", "ctrl+shift+backspace", "ctrl+backspace", "ctrl+h", "ctrl+w",
		"ctrl+t":
		m.pushUndo("")
	case "backspace":
		if cur == nil {
			return
		}
		if m.caret == 0 {
			m.pushUndo("") // a merge or remove is its own step
		} else {
			m.pushUndo("type:" + cur.uuid)
		}
	default:
		if (k.Type == tea.KeyRunes || k.Type == tea.KeySpace) && !k.Alt && cur != nil {
			m.pushUndo("type:" + cur.uuid)
		}
	}
}

// undo restores the most recent snapshot.
func (m *Model) undo() {
	if len(m.undoStack) == 0 {
		m.flash = "nothing to undo"
		return
	}
	st := m.undoStack[len(m.undoStack)-1]
	m.undoStack = m.undoStack[:len(m.undoStack)-1]
	m.undoMark = "" // the next edit starts a fresh snapshot

	m.tree.root = cloneItem(st.root, nil) // clone so the stacked entry stays pristine
	m.tree.rebuildByUUID()

	// Reconcile the restored tree with what's actually in the DB (the snapshots map)
	// so the next save is correct AND safe — this is what makes undo robust:
	//   - a node already in the DB must UPDATE, never re-INSERT (the UNIQUE-constraint
	//     crash); a never-saved node stays new.
	//   - a node that was in the DB but is gone from the restored tree is tombstoned
	//     (e.g. undoing a just-created node removes it), and any live process stops.
	m.tree.deleted = nil
	for uuid, it := range m.tree.byUUID {
		_, inDB := m.tree.snapshots[uuid]
		it.isNew = !inDB
	}
	for uuid := range m.tree.snapshots {
		if _, present := m.tree.byUUID[uuid]; !present {
			m.tree.deleted = append(m.tree.deleted, uuid)
			if r := m.run(uuid); r != nil && r.cancel != nil {
				r.cancel()
				r.cancel = nil
			}
		}
	}

	// restore the zoom path by uuid, falling back to the tree root
	var vs []*item
	for _, uuid := range st.view {
		if it, ok := m.tree.byUUID[uuid]; ok {
			vs = append(vs, it)
		}
	}
	if len(vs) == 0 {
		vs = []*item{m.tree.root}
	}
	m.viewStack = vs
	m.unsaved = true
	m.refreshRows()
	m.cursor = st.cursor
	m.clampCursor()
	m.caret = st.caret
	m.clampCaret()
}

func (m *Model) refreshRows() {
	m.rows = m.tree.visibleRows(m.viewRoot(), m.hideCompleted)
	m.clampCursor()
}

// clampCursor holds the cursor inside the current rows. A single delete can drop
// more than one row when the node is also shown through a mirror, so any code
// that nudges the cursor after a structural change must reclamp.
func (m *Model) clampCursor() {
	if m.cursor >= len(m.rows) {
		m.cursor = len(m.rows) - 1
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
}

// rowBudget is how many screen lines the outline body may occupy: the terminal
// height minus the two chrome lines (bottom bar plus its breathing room). When
// the height is known we honour it down to a single line so the selected row
// always stays on screen at tiny sizes; the default only covers the window
// before the first WindowSizeMsg sets a real height.
// scrollStart returns the first index of a fixed-height scrolling window of size
// `window` over `total` items that keeps the selected index `sel` in view.
func scrollStart(sel, total, window int) int {
	if window < 1 || total <= window || sel < window {
		return 0
	}
	start := sel - window + 1
	if start > total-window {
		start = total - window
	}
	if start < 0 {
		start = 0
	}
	return start
}

func (m *Model) rowBudget() int {
	if m.height <= 0 {
		return 18
	}
	return max(1, m.height-2)
}

func (m *Model) cursorItem() *item {
	if m.cursor < 0 || m.cursor >= len(m.rows) {
		return nil
	}
	return m.rows[m.cursor].it
}

// persistCollapsed writes an item's fold state to the DB. Collapse is local
// view-state.
func (m *Model) persistCollapsed(it *item) {
	if it == nil {
		return
	}
	if err := database.SetCollapsed(m.db, it.uuid, it.collapsed); err != nil {
		m.err = err
	}
}

// Init implements tea.Model.
func (m *Model) Init() tea.Cmd {
	cmd := m.startAnim(nil)
	switch {
	case m.liveFeed != nil:
		return tea.Batch(cmd, waitDaemonEv(m.liveFeed))
	case m.live != nil:
		return tea.Batch(cmd, feedRetryTick()) // no feed yet — keep trying
	}
	return cmd
}

// Update implements tea.Model.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		// A width change reflows the physical terminal before bubbletea repaints:
		// a row that occupied one physical line at the old width may now wrap to
		// two (or collapse to one), so the inline (no-alt-screen) renderer's
		// cursor-up count — measured in old-width lines — no longer matches the
		// reflowed layout. It then rewrites the new frame starting one row off and
		// strands the wider row's first physical line above it (the F7 leftover:
		// the old 60-col row survives, truncated, above the fresh 40-col render).
		// Per-line clear-to-EOL cannot reach a row the renderer never revisits.
		// tea.ClearScreen wipes the whole terminal and homes the cursor, so the
		// next frame repaints from a known-empty screen with no stale rows.
		widthChanged := msg.Width != m.width
		m.width = msg.Width
		m.height = msg.Height
		if widthChanged {
			return m, tea.ClearScreen
		}
		return m, nil
	case tea.KeyMsg:
		_, cmd := m.handleKey(msg)
		// the key may have moved the cursor off a typed follow-up inside an
		// active agent thread — leaving the node sends it (Enter still owns
		// fresh @mentions)
		if bc := m.blurSendCheck(); bc != nil {
			cmd = tea.Batch(cmd, bc)
		}
		// a mod view's Enter (alt+e / flash) can't return a Cmd through the
		// nodeView interface, so it queued any initial effect — drain it here.
		if pc := m.drainModPending(); pc != nil {
			cmd = tea.Batch(cmd, pc)
		}
		// live sync: arm the debounced flush for fresh edits, and fold in any
		// external events that queued while a modal surface was open
		if sc := m.scheduleSync(); sc != nil {
			cmd = tea.Batch(cmd, sc)
		}
		m.drainLive()
		// a keyword may have just been typed (or scrolled into view) — kick the
		// animation tick if it isn't already running.
		return m, m.startAnim(cmd)
	case daemonEvMsg:
		m.handleDaemonEv(msg.ev)
		return m, waitDaemonEv(m.liveFeed)
	case daemonFeedClosedMsg, feedRetryMsg:
		// the feed dropped (daemon restart, lagging cut) or never opened:
		// reconnect and resync what was missed, or retry shortly
		if m.startFeed() {
			m.needResync = true
			m.drainLive()
			return m, waitDaemonEv(m.liveFeed)
		}
		return m, feedRetryTick()
	case syncFlushMsg:
		return m, m.flushSync()
	case animTickMsg:
		animFrame++
		if m.animActive() {
			return m, animTick() // keep animating while a keyword or paste spinner is live
		}
		m.animTicking = false // nothing animating — stop redrawing
		return m, nil
	case bashLinesMsg:
		r := m.run(msg.uuid)
		if r == nil || r.cancel == nil {
			return m, nil // canceled — stop streaming
		}
		// one bounded batch per ~50ms window (see startBash) — a torrential
		// command costs the UI a handful of renders a second, never a freeze
		for _, l := range msg.lines {
			m.appendRunOut(msg.uuid, l)
		}
		return m, waitBashCmd(r.ch)
	case bashDoneMsg:
		m.finishRun(msg.uuid) // cache the finished band so it survives a restart
		return m, nil
	case modUpdateMsg:
		// an exec/fetch effect finished — feed its result to the mod view. Applied
		// even if the node is no longer focused (harmless; state just updates).
		return m, m.handleModUpdate(msg.key, msg.uuid, msg.msg)
	case modTickMsg:
		// a mod animation frame — delivered only while its node is the focused
		// view, so a tick loop can't keep animating a node the user has left.
		if !m.focused {
			return m, nil
		}
		if cur := m.cursorItem(); cur == nil || cur.uuid != msg.uuid {
			return m, nil
		}
		return m, m.handleModUpdate(msg.key, msg.uuid, map[string]any{"kind": "tick"})
	case agentEvMsg:
		return m.handleAgentEvent(msg)
	case agentStreamEndMsg:
		if t := m.thread(msg.thread); t != nil {
			t.busy = false
			if t.cancel != nil {
				t.cancel()
				t.cancel = nil
			}
		}
		return m, nil
	case wfDoneMsg:
		m.handleWFDone(msg)
		return m, nil
	case fzfPickedMsg:
		if it := m.tree.byUUID[msg.uuid]; it != nil {
			switch {
			case msg.path != "":
				m.insertPathChip(it, msg.caret, absolutizePath(msg.path))
			case msg.onCancel != "":
				// dismissed without a pick: the ">" that opened the picker types
				// literally, so a bash redirect (or any literal ">") still works.
				m.insertLiteralAt(it, msg.caret, msg.onCancel)
			}
		}
		return m, nil
	case voiceDoneMsg:
		m.setVoiceWave(msg.uuid, msg.env, msg.dur)
		return m, nil
	case imagePastedMsg:
		m.setImagePasting(msg.uuid, false)
		switch {
		case msg.err != nil:
			m.flash = "image: " + msg.err.Error()
		case m.db == nil:
			m.flash = "image: no database"
		default:
			blob := database.Blob{UUID: msg.uuid, Mime: "image/png", Bytes: msg.data, W: msg.w, H: msg.h}
			if err := database.PutBlob(m.db, blob); err != nil {
				m.err = err
			} else {
				m.imageInvalidate(msg.uuid) // reload the decode from the fresh blob
				m.flash = "image pasted"
			}
		}
		return m, nil
	}
	return m, nil
}

// mirrorContext is the single source of truth for how a mirror shapes the
// structural keypress about to run. Read it — and the rules below — to audit in
// one place every way mirrors react to keys.
//
// A mirror (mirrorOf != "") renders another node's live subtree "through" it, so
// the same real node can appear twice: at its original location, where its rows
// carry ctx == nil, and under a mirror of it, where its rows carry ctx == that
// mirror. Two invariants drive all mirror behaviour:
//
//   - EDITS act on the one real node. The rows shown through a mirror are the
//     real items, so a structural edit mutates the original and reflects in every
//     mirror at once.
//   - NAVIGATION stays local. After an edit the cursor is restored by (item, ctx)
//     via findRow, so it stays in the mirror view the user is working in rather
//     than jumping to the original copy.
//
// Per-key behaviour, all expressed through the fields below:
//
//	enter      · editable is false on a mirror reference, so Enter does not split
//	             its text; it opens an empty sibling. Locked nodes (readonly) also
//	             skip the split — newline only. Otherwise it splits at the caret.
//	             The cursor is restored into ctx.
//	tab        · indenting under a mirror attaches the child to the mirror's
//	             source; indentInto names that mirror so the cursor follows into
//	             its view instead of snapping back to the original.
//	shift+tab  · outdent is bounded by localRoot — the mirror's source when the
//	             cursor is inside a mirror — so a through-child cannot escape the
//	             mirror view, and the cursor stays in ctx.
//	reorder    · alt+shift+up/down move the real node among its siblings; the
//	             cursor is restored into ctx.
//	collapse   · fold/unfold counts the resolved children and restores into ctx.
//	ctrl+t     · the date pill only converts on an editable node, never a mirror
//	             reference.
//	zoom       · alt/ctrl+right into a mirror enters its source node so the
//	             original's children render rather than the empty reference.
//	delete     · removing a node drops it from the original AND every mirror at
//	             once, so the row set can shrink by more than one — clampCursor
//	             reclamps after any manual cursor nudge.
type mirrorContext struct {
	ctx        *item // the mirror the cursor sits under, nil at the original location
	editable   bool  // false on a mirror reference: its text is edited at the source
	localRoot  *item // outdent boundary: the mirror's source, else the view root
	indentInto *item // the mirror a Tab would indent under, so the cursor follows it
}

func (m *Model) mirrorContext() mirrorContext {
	cur := m.cursorItem()
	ctx := m.cursorCtx()

	localRoot := m.viewRoot()
	if ctx != nil {
		localRoot = m.tree.resolve(ctx) // the mirror's source is the local root
	}

	var indentInto *item
	if cur != nil {
		// Tab indents under the previous visible sibling; when that sibling is a
		// mirror the child attaches to its source, so the cursor should follow
		// into that mirror's view rather than snap back to the original.
		if idx := indexOf(cur); idx > 0 && cur.parent.children[idx-1].mirrorOf != "" {
			indentInto = cur.parent.children[idx-1]
		}
	}

	return mirrorContext{
		ctx:        ctx,
		editable:   cur == nil || cur.mirrorOf == "",
		localRoot:  localRoot,
		indentInto: indentInto,
	}
}

// pathChipTrigger reports whether ">" should open the file picker on this type.
// Every inline-editable type gets it — including bash/code/query where ">" is real
// syntax — because the picker is cancelable and dismissing it types a literal ">"
// instead, so file chips work in any node without losing the literal character.
func pathChipTrigger(typ string) bool {
	return typeOf(typ).inlineEditable
}

// linkChipTrigger reports whether "[[" should open the link picker on this type.
// Unlike the file picker it has no cancel-to-literal path, so it stays off where
// "[" is real syntax (bash test brackets, code, query, quote, json).
func linkChipTrigger(typ string) bool {
	switch typ {
	case database.TypeCode, database.TypeQuery, database.TypeQuote, database.TypeJSON:
		return false
	}
	return typeOf(typ).inlineEditable
}

// runeBeforeCaretIs reports whether the rune just left of caret is r.
func runeBeforeCaretIs(cur *item, caret int, r rune) bool {
	runes := []rune(cur.name)
	return caret > 0 && caret <= len(runes) && runes[caret-1] == r
}

// atWordStart reports whether the caret sits at the start of a word — at the
// node start or just after whitespace — so "#"/":" only open a completer there,
// keeping "C#" and "a:b" as literal mid-word text.
func atWordStart(cur *item, caret int) bool {
	if caret <= 0 {
		return true
	}
	runes := []rune(cur.name)
	return caret <= len(runes) && runes[caret-1] == ' '
}

// tagPickerTrigger reports whether "#" should open the tag completer on this
// type. Text-ish nodes (incl. query) get it; bash and code keep "#" literal
// since it is a comment there.
func tagPickerTrigger(typ string) bool {
	switch typ {
	case database.TypeCode:
		return false
	}
	return typeOf(typ).inlineEditable
}

// pasteLines normalizes pasted text into one line per logical row. tmux ONLCR
// rewrites \r\n into \r\r\n, so a naive \r\n then \r replacement would yield
// blank rows; instead we strip every \r before splitting on \n so any CR/LF run
// collapses to a single break, then drop the trailing blank from a final
// newline. Empty interior lines are preserved as the source intended.
func pasteLines(text string) []string {
	text = newlineRunRe.ReplaceAllString(text, "\n")
	text = strings.TrimRight(text, "\n")
	if text == "" {
		return nil
	}
	lines := strings.Split(text, "\n")
	for i := range lines {
		lines[i] = sanitizeName(lines[i])
	}
	return lines
}

// newlineRunRe matches any run of CR/LF as a single line break, so tmux ONLCR's
// \r\r\n collapses to one break instead of spawning empty ghost rows.
var newlineRunRe = regexp.MustCompile(`[\r\n]+`)

// bracketedPasteRe matches the bracketed-paste markers a terminal wraps around
// pasted text. A paste that itself contains the literal start/end sequences
// would otherwise smuggle them into a node name and toggle paste mode on render.
var bracketedPasteRe = regexp.MustCompile(`\x1b\[20[01]~`)

// sanitizeName makes pasted or inserted text safe to store as a node name and
// echo back to the terminal. It drops bracketed-paste markers and every C0
// control byte (0x00-0x1F) plus DEL (0x7F), so an embedded ESC[H/ESC[2J never
// executes as a cursor-home or clear-screen when the outline is rendered.
// Newlines are the paste fan-out separator and are handled before this step;
// tabs are normalized on the F3 path, so no control bytes should survive here.
func sanitizeName(text string) string {
	text = bracketedPasteRe.ReplaceAllString(text, "")
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7F {
			return -1
		}
		return r
	}, text)
}

// pasteFanOut spreads a multiline paste over the outline: the first line
// continues the current row at the caret, every following line becomes a new
// sibling below it. Lines are already sanitized by pasteLines; a line that
// sanitized to empty (only C0/DEL bytes) creates no sibling so the paste never
// leaves a ghost empty-named node between two real lines.
func (m *Model) pasteFanOut(cur *item, lines []string) (tea.Model, tea.Cmd) {
	runes := []rune(cur.name)
	m.boundCaret(len(runes))
	cur.name = string(runes[:m.caret]) + lines[0] + string(runes[m.caret:])

	last := cur
	for _, l := range lines[1:] {
		if l == "" {
			continue
		}
		it, err := m.tree.insertSiblingAfter(last)
		if err != nil {
			m.err = err
			return m.quit()
		}
		it.name = l
		last = it
	}

	m.unsaved = true
	m.refreshRows()
	m.cursor = m.rowIndexOf(last)
	m.caret = len([]rune(last.name))
	m.maybeLinkToMirror(last)
	return m, nil
}

var mirrorLinkRe = regexp.MustCompile(`^lflow://node/([0-9a-fA-F-]{6,})$`)

// maybeLinkToMirror turns a row whose whole text is a node link into a
// mirror of that node: paste a copied link, get a mirror.
func (m *Model) maybeLinkToMirror(it *item) {
	trimmed := strings.TrimSpace(it.name)
	if !strings.HasPrefix(trimmed, "lflow://") {
		return
	}
	match := mirrorLinkRe.FindStringSubmatch(trimmed)
	if match == nil {
		return
	}
	uuid := match[1]
	if uuid == it.uuid {
		m.flash = "a node cannot mirror itself"
		return
	}
	target, err := database.GetNode(m.db, uuid)
	if err != nil {
		m.flash = "link points at no node"
		return
	}

	target = m.resolveSourceNode(target)
	it.name = ""
	it.mirrorOf = target.UUID
	m.caret = 0
	if _, inTree := m.tree.byUUID[target.UUID]; !inTree {
		m.tree.externalNames[target.UUID] = target.Name
	}
	m.unsaved = true
	m.flash = fmt.Sprintf("mirrored %q", target.Name)
}

// resolveSourceNode follows a node's mirror chain to its ultimate
// non-mirror original, so a new mirror points at the real node and shows
// its name. A node that is not a mirror is returned unchanged.
func (m *Model) resolveSourceNode(n database.Node) database.Node {
	// last tracks the deepest node we could fetch; on a broken chain (a mirror
	// pointing at a missing node) it stays on the last good node, matching the
	// original fall-through. The start node n is already in hand, so the closure
	// serves it from last rather than re-fetching by uuid.
	last := n
	followMirrorChain(n.UUID, func(uuid string) (string, bool) {
		if uuid == last.UUID {
			return last.MirrorOf, true
		}
		orig, err := database.GetNode(m.db, uuid)
		if err != nil {
			return "", false
		}
		last = orig
		return orig.MirrorOf, true
	})
	return last
}

// toggleComplete flips completed_at on it (same as /complete and alt+enter).
// Caller owns the undo snapshot. When /hide:complete is hiding completed, the outline
// refreshes so a just-completed node disappears (or reappears on uncomplete).
func (m *Model) toggleComplete(it *item) {
	if it == nil {
		return
	}
	if it.completedAt > 0 {
		it.completedAt = 0
	} else {
		it.completedAt = time.Now().Unix()
	}
	m.unsaved = true
	if m.hideCompleted {
		m.refreshRows()
	}
}

// deleteNode removes the node and its subtree from the tree.
func (m *Model) deleteNode(it *item) {
	// kill any agent still running on a thread root inside this subtree —
	// otherwise the CLI process outlives the mention that owned it
	m.stopAgentsUnder(it)
	// drop each removed node's persisted run-output cache so it doesn't outlive it
	var dropRunOut func(x *item)
	dropRunOut = func(x *item) {
		m.deleteRunOut(x.uuid)
		delete(nodeSpans, x.uuid)
		if m.db != nil {
			_ = database.DeleteNodeSpans(m.db, x.uuid)
		}
		for _, c := range x.children {
			dropRunOut(c)
		}
	}
	dropRunOut(it)
	m.tree.remove(it)
	m.unsaved = true
	m.ensureViewNonEmpty()
	m.refreshRows()
	m.caret = 0
}

// runState is the per-id run band: the captured output plus the live bookkeeping
// for a running command. All of it is keyed by ONE id (node uuid or cmd-chip id),
// so dropping a run = delete(m.runs, id) — one atomic delete instead of six.
type runState struct {
	out     []outLine    // captured stdout/stderr lines (the band)
	cancel  func()       // cancels the running command; nil once finished
	ch      chan tea.Msg // stream channel for a running command
	loaded  bool         // band hydrated from node_output (see runout.go)
	dropped int          // lines dropped off the band's head (see maxRunLines)
	pwd     string       // cwd captured when the band was run
}

// run returns the existing run state for an id, or nil if none — nil-safe for
// read/comma-ok sites (an absent id reads as "no output, not running").
func (m *Model) run(id string) *runState { return m.runs[id] }

// ensureRun returns the run state for an id, lazily creating m.runs and the entry.
// Every write site goes through here (mirrors nodeStore).
func (m *Model) ensureRun(id string) *runState {
	if m.runs == nil {
		m.runs = map[string]*runState{}
	}
	r := m.runs[id]
	if r == nil {
		r = &runState{}
		m.runs[id] = r
	}
	return r
}

// nodeStore returns the ephemeral per-node data bag for a uuid, creating it on
// first use. Node views stash live/edit state here (never persisted or synced).
func (m *Model) nodeStore(uuid string) map[string]any {
	if m.nodeData == nil {
		m.nodeData = map[string]map[string]any{}
	}
	d := m.nodeData[uuid]
	if d == nil {
		d = map[string]any{}
		m.nodeData[uuid] = d
	}
	return d
}

// ensureViewNonEmpty keeps the current section from going empty: if the view root
// has no children left (e.g. the last node was deleted), insert a fresh empty one
// so there is always a node to type into.
func (m *Model) ensureViewNonEmpty() {
	root := m.viewRoot()
	if root != nil && len(root.children) == 0 {
		_, _ = m.tree.insertFirstChild(root)
	}
}

// subtreeSize counts the node and everything below it.
func subtreeSize(it *item) int {
	n := 1
	for _, c := range it.children {
		n += subtreeSize(c)
	}
	return n
}

// handleConfirmKey answers the inline delete confirmation.
func (m *Model) handleConfirmKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "enter", "y":
		m.mode = modeOutline
		if m.selOn {
			m.selDelete()
		} else if cur := m.cursorItem(); cur != nil {
			m.deleteNode(cur)
		}
	case "esc", "n":
		m.mode = modeOutline
	}
	return m, nil
}

// selectedVisualRows returns the rune offsets at which each soft-wrapped
// visual line of the selected node begins, measured with the same width and
// hanging indent the renderer wraps the row at. A node that fits on one line
// returns a single-element slice, so Up/Down can tell when the caret is on the
// first or last visual line and only then cross to another node.
func (m *Model) selectedVisualRows() []int {
	if m.cursor < 0 || m.cursor >= len(m.rows) {
		return []int{0}
	}
	r := m.rows[m.cursor]
	glyph, _ := glyphFor(r.it)
	name := m.tree.displayName(r.it)
	maxLine := m.width - 1
	firstCol := visibleWidth(" " + connector(r) + glyph + " ")
	below := m.cursor+1 < len(m.rows) && m.rows[m.cursor+1].depth > r.depth
	hang := visibleWidth(continuationPrefix(r, below))
	if hasAnchor(name) {
		return chipVisualRows(name, maxLine, firstCol, hang, m.chips)
	}
	return visualRows(name, maxLine, firstCol, hang)
}


// caretColumn returns the caret's display column within its visual line: the
// width of the runes between the line's start offset and the caret.
func (m *Model) caretColumn(starts []int, line int) int {
	cur := m.cursorItem()
	if cur == nil || line < 0 || line >= len(starts) {
		return 0
	}
	runes := []rune(m.tree.displayName(cur))
	start := starts[line]
	if m.caret < start {
		return 0
	}
	end := m.caret
	if end > len(runes) {
		end = len(runes)
	}
	if spans := anchorSpans(runes); len(spans) > 0 {
		return chipDispWidth(runes, start, end, spans, m.chips)
	}
	return visibleWidth(string(runes[start:end]))
}

// caretAtColumn returns the caret index on the given visual line nearest the
// target display column, clamped to that line's runes. It is the inverse of
// caretColumn and keeps vertical movement on a stable horizontal column.
func (m *Model) caretAtColumn(starts []int, line, col int) int {
	cur := m.cursorItem()
	if cur == nil || len(starts) == 0 {
		return m.caret
	}
	if line < 0 {
		line = 0
	}
	if line >= len(starts) {
		line = len(starts) - 1
	}
	runes := []rune(m.tree.displayName(cur))
	start := starts[line]
	end := len(runes)
	if line+1 < len(starts) {
		// stop before the next line's start; the trailing space that wrapped
		// is consumed by the break, so land on the last rune of this line
		end = starts[line+1]
	}
	if spans := anchorSpans(runes); len(spans) > 0 {
		return chipCaretAtColumn(runes, start, end, col, spans, m.chips)
	}
	w := 0
	for i := start; i < end; i++ {
		rw := runewidth.RuneWidth(runes[i])
		if w+rw > col {
			return i
		}
		w += rw
	}
	return end
}

func (m *Model) clampCaret() {
	if cur := m.cursorItem(); cur != nil {
		runes := []rune(cur.name)
		if m.caret > len(runes) {
			m.caret = len(runes)
		}
		// a chip anchor is atomic — never leave the caret stranded inside one
		if sp := spanContaining(anchorSpans(runes), m.caret); sp != nil {
			m.caret = sp.end
		}
	}
}

// boundCaret clamps the caret into [0, n] and returns it — a guard for every name
// edit against a caret left stale by a cursor move (e.g. landing on a shorter
// node), which would otherwise panic slicing runes[:m.caret].
func (m *Model) boundCaret(n int) int {
	if m.caret > n {
		m.caret = n
	}
	if m.caret < 0 {
		m.caret = 0
	}
	return m.caret
}

// nextWordBoundary returns the caret index at the start of the next word: it
// skips the rest of the current word, then any spaces, like a normal editor.
func nextWordBoundary(runes []rune, caret int) int {
	i := caret
	for i < len(runes) && runes[i] != ' ' {
		i++
	}
	for i < len(runes) && runes[i] == ' ' {
		i++
	}
	return i
}

// prevWordBoundary returns the caret index at the start of the previous word.
func prevWordBoundary(runes []rune, caret int) int {
	i := caret
	for i > 0 && runes[i-1] == ' ' {
		i--
	}
	for i > 0 && runes[i-1] != ' ' {
		i--
	}
	return i
}

func (m *Model) rowIndexOf(it *item) int {
	for i, r := range m.rows {
		if r.it == it {
			return i
		}
	}
	return 0
}

// cursorCtx is the mirror the cursor row is shown under, or nil at the real
// location. Structural edits use it to keep the cursor in the same mirror view.
func (m *Model) cursorCtx() *item {
	if m.cursor >= 0 && m.cursor < len(m.rows) {
		return m.rows[m.cursor].ctx
	}
	return nil
}

// findRow locates the row showing it within mirror context ctx, preferring an
// exact context match so the cursor stays local to the mirror the user is in,
// then any row for it. The same node can appear under the original and through
// every mirror of it, so the context disambiguates which copy to land on.
func (m *Model) findRow(it *item, ctx *item) int {
	for i, r := range m.rows {
		if r.it == it && r.ctx == ctx {
			return i
		}
	}
	return m.rowIndexOf(it)
}

func (m *Model) filteredSlash(query string) []slashCommand {
	if query == "" {
		return slashCommands
	}
	var ret []slashCommand
	for _, c := range slashCommands {
		if strings.Contains(c.name, strings.ToLower(query)) {
			ret = append(ret, c)
		}
	}
	return ret
}

// openSlashMenu opens the command palette (modeSlash). When inline is true the
// "/" trigger is typed into the node name and stripped on run/cancel (the /
// key path). alt+P (alt+shift+p) opens non-inline so the name is left alone while filtering.
func (m *Model) openSlashMenu(inline bool) {
	m.mode = modeSlash
	m.list = listPicker{searchable: true}
	m.slashInline = inline
	if !inline {
		return
	}
	cur := m.cursorItem()
	if cur == nil {
		return
	}
	runes := []rune(cur.name)
	m.boundCaret(len(runes))
	cur.name = string(runes[:m.caret]) + "/" + string(runes[m.caret:])
	m.slashStart = m.caret
	m.caret++
	m.unsaved = true
}

// stripSlashText removes the typed "/query" from the node text and parks the
// caret where the slash was. Called before a slash command runs.
func (m *Model) stripSlashText() {
	if !m.slashInline {
		return
	}
	cur := m.cursorItem()
	if cur == nil {
		return
	}
	runes := []rune(cur.name)
	end := m.slashStart + 1 + len([]rune(m.list.query))
	if end > len(runes) {
		end = len(runes)
	}
	cur.name = string(runes[:m.slashStart]) + string(runes[end:])
	m.caret = m.slashStart
}

// Slash-menu key handling now lives in the shared listPicker (picker_list.go)
// via slashSource (picker_sources.go); the "/query" text-mirroring is its
// inlineTextSource hooks.

// filteredTypes returns the node-type keys matching the picker's search query,
// in registry order: built-ins first, then installed artifacts. The query
// fuzzy-matches label and key (subsequence, case-insensitive), so twenty
// forgotten artifacts never bury Todo.
func (m *Model) filteredTypes(query string) []string {
	q := strings.ToLower(query)
	var ret []string
	for _, t := range typeOrder() {
		if typeOf(t).internal {
			continue // internal types (agent replies) are app-created, never user-picked
		}
		if typeOf(t).tempOnly && !m.tempActive {
			continue // temp-only types are not offered outside the Temporary Domain
		}
		if q != "" && !fuzzyMatch(strings.ToLower(typeLabel(t)), q) && !fuzzyMatch(t, q) {
			continue
		}
		ret = append(ret, t)
	}
	return ret
}

// /type, /style, and /theme key handling now live in the shared listPicker
// (picker_list.go) via their pickerSources (picker_sources.go).

// fuzzyMatch reports whether needle is an in-order subsequence of hay — the
// picker's filter ("hd3" hits "Heading 3").
func fuzzyMatch(hay, needle string) bool {
	i := 0
	for _, r := range hay {
		if i < len(needle) && r == rune(needle[i]) {
			i++
		}
	}
	return i == len(needle)
}

// handleSettingsKey drives the /settings picker: up/down pick a preference,
// left/right (or space) cycle its value with a live apply + DB persist, esc/enter
// close.
func (m *Model) handleSettingsKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "esc", "enter":
		m.mode = modeOutline
		return m, nil
	case "up":
		if m.settingsSel > 0 {
			m.settingsSel--
		}
	case "down":
		if m.settingsSel < len(settingDefs)-1 {
			m.settingsSel++
		}
	case "left", "right", " ", "space", "h", "l":
		if m.settingsSel >= 0 && m.settingsSel < len(settingDefs) {
			d := settingDefs[m.settingsSel]
			dir := 1
			if s := k.String(); s == "left" || s == "h" {
				dir = -1
			}
			m.setSetting(d.key, cycleSetting(d, m.setting(d.key), dir))
		}
	}
	return m, nil
}

func (m *Model) runSlash(name string) (tea.Model, tea.Cmd) {
	m.mode = modeOutline
	cur := m.cursorItem()
	if cur == nil {
		return m, nil
	}

	switch name {
	case "/type":
		// reload the nodes dir first — the user (or an agent) may have edited
		// the type files out-of-band since the last load
		loadNodeMods()
		// open the picker; pre-select the type already in effect (see typeSource)
		m.mode = modeType
		m.list.open(m, typeSource{}, true)
	case "/style":
		// open the picker; pre-select the active toggle/color (see styleSource)
		m.mode = modeStyle
		m.list.open(m, styleSource{}, true)
	case "/theme":
		// open the palette picker; pre-select the active theme (see themeSource)
		m.mode = modeTheme
		m.list.open(m, themeSource{}, false)
	case "/settings":
		// open the global-preferences picker (theme, link color, image preview)
		m.mode = modeSettings
		m.settingsSel = 0
	case "/lock":
		// toggle the read-only lock: locked nodes ignore inline text edits (a file
		// node locks itself on Enter); unlock to edit, Enter re-locks a file node.
		m.pushUndo("")
		cur.readonly = !cur.readonly
		m.unsaved = true
		if cur.readonly {
			m.flash = "locked · /lock to unlock"
		} else {
			m.flash = "unlocked"
		}
	case "/complete":
		m.pushUndo("")
		m.toggleComplete(cur)
	case "/hide:complete":
		// hide or show completed nodes in the outline (session toggle)
		m.hideCompleted = !m.hideCompleted
		m.refreshRows()
		if m.hideCompleted {
			m.flash = "hiding completed"
		} else {
			m.flash = "showing completed"
		}
	case "/star":
		// toggle the star on the real node (a mirror stars its original): starred
		// nodes wear a yellow ★ and rank first in the move/goto/mirror pickers
		cur = m.tree.resolve(cur)
		cur.starred = !cur.starred
		if m.db != nil {
			_ = database.SetStarred(m.db, cur.uuid, cur.starred)
		}
		if cur.starred {
			m.flash = "★ starred"
		} else {
			m.flash = "unstarred"
		}
	case "/duplicate":
		// deep-copy this node (and its subtree) in as the next sibling, then
		// land the cursor on the copy so it is ready to rename/edit
		m.pushUndo("")
		ctx := m.cursorCtx()
		clone, err := m.tree.duplicate(cur)
		if err != nil {
			m.flash = err.Error()
			return m, nil
		}
		m.unsaved = true
		m.refreshRows()
		m.cursor = m.findRow(clone, ctx)
	case "/note":
		// a mirror is the same node everywhere: edit the original's note
		cur = m.tree.resolve(cur)
		m.mode = modeNote
		m.notePrev = cur.note
		m.caret = len([]rune(cur.note))
	case "/move:here":
		// pick any node (incl. a Temporary Domain node) and move it here
		m.openFinder(actBringHere)
	case "/mirror:to":
		m.openFinder(actMirrorHere)
	case "/mirror:from":
		m.openFinder(actMirrorFrom)
	case "/link":
		// splice an inline link chip at the caret (same as the [[ trigger)
		m.openFinder(actLinkInsert)
	case "/move:to":
		m.openFinder(actMoveTo)
	case "/goto":
		m.openFinder(actGoto)
	case "/undo":
		m.undo()
	}
	return m, nil
}

func (m *Model) handleNoteKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	// a mirror's note is its original's note: edit the one real node
	cur := m.tree.resolve(m.cursorItem())
	if cur == nil {
		m.mode = modeOutline
		return m, nil
	}
	switch k.String() {
	case "esc":
		cur.note = m.notePrev
		m.mode = modeOutline
		m.caret = len([]rune(cur.name))
		return m, nil
	case "enter":
		m.mode = modeOutline
		m.unsaved = true
		m.caret = len([]rune(cur.name))
		return m, nil
	}
	// text editing is consistent everywhere: the note field honors the same
	// caret vocabulary as the link editor, via the shared textField primitive.
	f := textField{value: cur.note, caret: m.caret}
	if f.handleKey(k) {
		cur.note = f.value
		m.caret = f.caret
	}
	return m, nil
}

func (m *Model) quit() (tea.Model, tea.Cmd) {
	// stop any live run processes (bash/query/voice) still going
	for _, r := range m.runs {
		if r.cancel != nil {
			r.cancel()
		}
	}
	if m.feedCancel != nil {
		m.feedCancel()
		m.feedCancel = nil
	}
	if m.tempActive {
		m.exitTemp() // back to the main tree so save persists it, not the scratch
	}
	if m.err == nil {
		written, err := m.saveAll()
		if err != nil {
			m.err = err
		} else {
			m.saved.written += written
			// drop chip rows no surviving node references (anchors deleted by
			// edits, or nodes tombstoned this session)
			if m.ctx.DB != nil {
				_ = database.GCChips(m.ctx.DB)
				_ = database.GCBlobs(m.ctx.DB)   // drop image blobs whose node is gone
				_ = database.GCModData(m.ctx.DB) // drop mod state whose node is gone
			}
		}
	}
	m.quitting = true
	return m, tea.Quit
}

// Run opens the inline node editor on the given node.
func Run(ctx context.DnoteCtx, nodeUUID string) error {
	initNodeMods(ctx.Paths.Config, ctx.DB) // runtime node types must exist before the first render

	// materialize the embedded lflow skill (pkg/agent/skills) into the data
	// dir; every agent turn passes it to the CLI agent (pi --skill)
	if dir, err := agent.MaterializeSkills(filepath.Join(ctx.Paths.Data, consts.LflowDirName)); err == nil {
		tag.SetSkillDir(dir)
	}

	t, err := loadTree(ctx.DB, nodeUUID)
	if err != nil {
		return errors.Wrap(err, "loading node tree")
	}

	tempTree, err := loadTree(ctx.DB, database.TempUUID)
	if err != nil {
		return errors.Wrap(err, "loading temp tree")
	}

	chips, err := database.LoadChips(ctx.DB)
	if err != nil {
		return errors.Wrap(err, "loading chips")
	}

	wfMap, err := database.AllWFNodes(ctx.DB)
	if err != nil {
		return errors.Wrap(err, "loading workflowy map")
	}

	if tc, err := database.AllTagColors(ctx.DB); err == nil {
		tagColors = tc // package var, like linkColorMode: the render path is Model-free
	}
	if sp, err := database.AllNodeSpans(ctx.DB); err == nil {
		nodeSpans = sp // painter runs (see paint.go)
	}

	m := &Model{
		db:        ctx.DB,
		ctx:       ctx,
		tree:      t,
		chips:     chips,
		tempTree:  tempTree, // the Temp root subtree, persisted alongside Root
		viewStack: []*item{t.root},
		agents:    tag.LoadAgents(ctx.Paths.Config),
		wfMap:     wfMap,
		live:      ctx.Live, // daemon connection: live sync (nil in direct runs)
	}
	m.startFeed() // subscribe to external changes; Init retries if it failed
	m.loadSettings() // apply persisted preferences (theme, …) before the first render
	m.refreshAncestors()
	m.refreshRows()
	m.ensureTempTree()    // the panel is always visible, so it must always have >=1 node
	m.backfillChipsOnce() // one-time: convert legacy plain-text #tags/dates to chips
	m.refreshRows()

	// WARNING (invariant): inline scrollback only — NEVER pass tea.WithAltScreen.
	// The alt-screen erases the styled outline on quit and breaks scriptable
	// scrollback output. Lint-enforced (see rules/).
	p := tea.NewProgram(m) // inline: no alt screen
	final, err := p.Run()
	if err != nil {
		return errors.Wrap(err, "running editor")
	}

	fm, ok := final.(*Model)
	if !ok {
		fm = m
	}
	// the agent bridge dies with the editor: park in-flight sessions so their
	// ids resume the remote context on the next mention
	_ = database.PauseRunningSessions(ctx.DB)
	if fm.err != nil {
		return fm.err
	}

	total, _ := fm.tree.stats()
	fmt.Print(savedSummary(fm.tree.displayName(fm.tree.root), fm.chips, total, fm.saved.written))

	return nil
}

// savedSummary formats the one-line "→ saved" report printed after the editor
// exits. It resolves chip anchors in the root name first: a loaded/zoomed root
// whose name carries chips would otherwise leak the raw sentinel-wrapped chip
// ids into the summary (the chip invariant — every surface that reads a name
// resolves anchors through the chip store).
func savedSummary(name string, chips map[string]database.Chip, total, written int) string {
	name = displayAnchors(name, chips)
	// muted gray throughout, only the node name in yellow
	return fmt.Sprintf("%s→ saved %s%q%s - %s - %s written%s\n",
		cDim, cYellow, name, cDim,
		nodeNoun(total), nodeNoun(written), cReset)
}

func nodeNoun(n int) string {
	if n == 1 {
		return "1 node"
	}
	return fmt.Sprintf("%d nodes", n)
}
