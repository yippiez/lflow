// Package editor implements the inline scrollback-mode outline editor:
// black background, muted gray ○/●/◆/□ glyphs and connectors plus 1/2/3
// heading digits, the selected row marked by its glyph turning red, a block
// cursor that inverts the cell beneath it, a minimal dim bottom bar, a
// type-to-filter slash menu above the bar, and a full-panel fuzzy finder for
// /mirror /mirror_to /move_to /goto. It never enters the alternate screen.
package editor

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	osc52 "github.com/aymanbagabas/go-osc52/v2"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/lflow/lflow/pkg/browser"
	"github.com/lflow/lflow/pkg/tui/context"
	"github.com/lflow/lflow/pkg/tui/database"
	"github.com/lflow/lflow/pkg/tui/tag"
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
	modeComplete // the inline completer: "#" tags, ":" query commands
	modeLinkEdit    // the alt+e link-chip editor: edit a link's name and target
	modeCmdView     // the alt+e cmd-chip viewer: scroll a cmd chip's full run output
	modeArtifacts   // the /artifacts management view: list, enable/disable, uninstall
	modeArtifactSrc // the artifact source pager (enter on an /artifacts row)
)

type finderAction int

const (
	actMirrorHere finderAction = iota
	actMoveTo
	actGoto
	actBringHere
	actLinkInsert // [[ — insert an inline link chip at the caret (node or URL)
)

type slashCommand struct {
	name string
	desc string
}

var slashCommands = []slashCommand{
	{"/artifacts", "Manage installed artifact node types"},
	{"/bring", "Bring another node (or an agent) here"},
	{"/complete", "Toggle done"},
	{"/duplicate", "Duplicate this node (and its subtree) next to it"},
	{"/file", "Insert a file path chip (fuzzy fzf picker)"},
	{"/goto", "Jump the editor to another node"},
	{"/link", "Insert an inline link to a node or URL ([[)"},
	{"/lock", "Lock or unlock this node (read-only)"},
	{"/mirror", "Mirror a node here via the fuzzy finder"},
	{"/move", "Move this node under another node"},
	{"/note", "Edit this node's note"},
	{"/style", "Set this node's text style or color"},
	{"/theme", "Switch the color theme"},
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

	mode        mode
	slashQuery  string
	slashSel    int
	slashStart  int  // rune index of the "/" that opened the menu
	slashInline bool // the slash and query are typed into the node text
	finderQuery string
	finderSel   int
	finderHits  []database.Node
	finderAct   finderAction
	notePrev    string // note backup for esc in note mode

	// alt+e link-chip editor (modeLinkEdit)
	linkEditID     string // chip id being edited
	linkEditName   string // working copy of the link's display name
	linkEditTarget string // working copy of the link's target (URL or lflow://node/<uuid>)
	linkEditField  int    // 0 = name field, 1 = target field

	cmdViewID  string // cmd chip id whose output the alt+e viewer is showing
	cmdViewCmd string // that chip's command, for the viewer header

	// /type picker selection (index into the filtered list) and search query
	typeSel   int
	typeQuery string

	// /style picker selection (index into stylePickerItems)
	styleSel int

	// /theme picker selection (index into the themes registry)
	themeSel int

	// /artifacts management view: selected row, inspected key, source scroll
	artSel       int
	artKey       string
	artSrcScroll int

	// inline completer state ("#" tags, ":" query commands)
	compl complState

	// Shared RUN machinery — the generic spawn/stream/cancel infrastructure the
	// runnable node types use (bash, query, voice). Ephemeral, in-memory only,
	// keyed by node uuid. Run output is NEVER in the DB or synced.
	runOut       map[string][]outLine
	runCancel    map[string]func()       // cancel a running command
	runCh        map[string]chan tea.Msg // stream channel for a running command
	runOutLoaded map[string]bool         // uuids whose run band is hydrated (see runout.go)

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
	// runs past the footer without moving the cursor. Any other key resets it to
	// cursor-follow. viewTop/viewRows cache the last frame's window so a page step
	// is relative to what is on screen.
	scrolling bool
	scrollTop int
	viewTop   int
	viewRows  int
	// nodeData is a generic ephemeral per-node store (uuid → key → value), never
	// persisted or synced — node views keep live/edit state here instead of
	// growing the Model with one named map per type.
	nodeData map[string]map[string]any

	// @mention agent sessions (see agent.go and pkg/tui/tag): configured
	// agents, one client per agent, busy flags per thread root, and the nodes
	// whose mention already sent this session (Enter sends once; alt+r re-sends)
	agents      []tag.Agent
	tagClients  map[string]tag.Client
	agentBusy   map[string]bool
	mentionSent map[string]bool

	// /undo: snapshots of the tree taken before each action
	undoStack []undoState
	undoMark  string

	// breadcrumb names above the loaded root, from the forest root down
	ancestors []string

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
	if base == nil || base.uuid == "" || base.uuid == database.RootUUID {
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
	//     (e.g. undoing a just-created agent removes it), and any live process stops.
	m.tree.deleted = nil
	for uuid, it := range m.tree.byUUID {
		_, inDB := m.tree.snapshots[uuid]
		it.isNew = !inDB
	}
	for uuid := range m.tree.snapshots {
		if _, present := m.tree.byUUID[uuid]; !present {
			m.tree.deleted = append(m.tree.deleted, uuid)
			if m.runCancel != nil {
				if cancel, running := m.runCancel[uuid]; running {
					cancel()
					delete(m.runCancel, uuid)
				}
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
	m.rows = m.tree.visibleRows(m.viewRoot())
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
func (m *Model) Init() tea.Cmd { return m.startAnim(nil) }

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
		// a keyword may have just been typed (or scrolled into view) — kick the
		// animation tick if it isn't already running.
		return m, m.startAnim(cmd)
	case animTickMsg:
		animFrame++
		if m.hasMagicKeyword() {
			return m, animTick() // keep animating while a keyword is on screen
		}
		m.animTicking = false // none visible — stop redrawing
		return m, nil
	case bashLineMsg:
		if _, running := m.runCancel[msg.uuid]; !running {
			return m, nil // canceled — stop streaming
		}
		m.runOut[msg.uuid] = append(m.runOut[msg.uuid], outLine{text: msg.text, err: msg.err})
		return m, waitBashCmd(m.runCh[msg.uuid])
	case bashDoneMsg:
		delete(m.runCancel, msg.uuid)
		delete(m.runCh, msg.uuid)
		m.persistRunOut(msg.uuid) // cache the finished band so it survives a restart
		m.setCmdPreview(msg.uuid) // a cmd chip: refresh its inline "→ preview"
		return m, nil
	case agentEvMsg:
		return m.handleAgentEvent(msg)
	case agentStreamEndMsg:
		delete(m.agentBusy, msg.thread)
		return m, nil
	case fzfPickedMsg:
		if msg.path != "" {
			if it := m.tree.byUUID[msg.uuid]; it != nil {
				m.insertPathChip(it, msg.caret, absolutizePath(msg.path))
			}
		}
		return m, nil
	case voiceDoneMsg:
		m.setVoiceWave(msg.uuid, msg.env, msg.dur)
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
//	             its text; it opens an empty sibling. Otherwise it splits at the
//	             caret. The cursor is restored into ctx.
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

func (m *Model) handleKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := k.String()
	m.flash = "" // one-shot: whatever this key does sets the next status

	// page keys scroll the viewport in place; every other key returns the view to
	// following the cursor.
	if key != "pgup" && key != "pgdown" {
		m.scrolling = false
	}

	// esc-esc quits from outline mode — but not while a focused inline view is up
	// (there esc defocuses; handled in the focused block below)
	if m.mode == modeOutline && key == "esc" && !m.focused {
		if m.escPending {
			return m.quit()
		}
		m.escPending = true
		return m, nil
	}
	if key != "esc" {
		m.escPending = false
	}

	switch m.mode {
	case modeSlash:
		return m.handleSlashKey(k)
	case modeFinder:
		return m.handleFinderKey(k)
	case modeLinkEdit:
		return m.handleLinkEditKey(k)
	case modeCmdView:
		return m.handleCmdViewKey(k)
	case modeNote:
		return m.handleNoteKey(k)
	case modeConfirm:
		return m.handleConfirmKey(k)
	case modeType:
		return m.handleTypeKey(k)
	case modeArtifacts:
		return m.handleArtifactsKey(k)
	case modeArtifactSrc:
		return m.handleArtifactSrcKey(k)
	case modeStyle:
		return m.handleStyleKey(k)
	case modeTheme:
		return m.handleThemeKey(k)
	case modeComplete:
		return m.handleCompleteKey(k)
	}

	// A focused inline node view captures input first (it stays inside the outline,
	// so we're still modeOutline). The view handles its own keys; esc defocuses
	// (flushing edits); ctrl+c/ctrl+q fall through to quit; everything else is
	// swallowed so it can't leak into outline navigation.
	if m.focused && m.mode == modeOutline {
		cur := m.cursorItem()
		if v := nodeViewOf(cur); v != nil {
			if cmd, handled := v.Key(m, cur, k); handled {
				return m, cmd
			}
			switch key {
			case "esc":
				v.Leave(m, cur)
				m.focused = false
				return m, nil
			case "ctrl+c", "ctrl+q":
				// fall through to the quit handler below
			default:
				return m, nil
			}
		} else {
			m.focused = false
		}
	}

	// snapshot the tree before a mutating outline key so /undo can reverse it
	m.snapshotForKey(key, k)

	switch key {
	case "pgdown", "pgup":
		// scroll the body a page at a time without moving the cursor — for reading a
		// long note/subtree that runs past the footer. A small overlap keeps context.
		step := m.viewRows - 2
		if step < 1 {
			step = 1
		}
		if !m.scrolling {
			m.scrolling = true
			m.scrollTop = m.viewTop // start from what is currently on screen
		}
		if key == "pgdown" {
			m.scrollTop += step
		} else {
			m.scrollTop -= step
			if m.scrollTop < 0 {
				m.scrollTop = 0
			}
		}
		return m, nil
	case "ctrl+q", "ctrl+c":
		return m.quit()
	case "ctrl+s":
		written, err := m.saveAll()
		if err != nil {
			m.err = err
			return m.quit()
		}
		m.saved.written += written
		m.unsaved = false
		return m, nil
	case "ctrl+z", "alt+z":
		// undo the last action (alt+z is the fallback where ctrl+z suspends)
		m.undo()
		return m, nil
	case "enter":
		cur := m.cursorItem()
		// commit a #tag or date token under the caret into a chip before splitting
		if cur != nil {
			m.chipifyBeforeCaret(cur)
		}
		// a node with a fresh @AgentName mention: Enter IS the send (the
		// keyboard stand-in for Slack's send button) — the reply lands beneath
		// this node instead of a sibling opening. alt+r re-sends any time.
		if cmd, sent := m.mentionSendOnEnter(cur); sent {
			return m, cmd
		}
		mc := m.mirrorContext()
		// caret at the very start of a node that has text: don't split — keep the
		// node and its whole subtree intact and push it down, opening an empty node
		// above it with the cursor there.
		if cur != nil && mc.editable && m.caret == 0 && cur.name != "" {
			it, err := m.tree.insertSiblingBefore(cur)
			if err != nil {
				m.err = err
				return m.quit()
			}
			if cur.typ == database.TypeTodo {
				it.typ = database.TypeTodo // keep the todo list going
			}
			m.unsaved = true
			m.refreshRows()
			m.cursor = m.findRow(it, mc.ctx)
			m.caret = 0
			return m, nil
		}
		var it *item
		var err error
		// On an expanded node that already has children, the new node belongs
		// inside it as the first child — not as a sibling after the whole subtree.
		expandedParent := cur != nil && cur.mirrorOf == "" && !cur.collapsed && len(m.tree.childItems(cur)) > 0
		switch {
		case cur == nil:
			it, err = m.tree.insertFirstChild(m.viewRoot())
		case expandedParent:
			it, err = m.tree.insertFirstChild(cur)
		default:
			it, err = m.tree.insertSiblingAfter(cur)
		}
		if err != nil {
			m.err = err
			return m.quit()
		}
		if it != nil {
			// pressing Enter from a todo continues the todo list — the fresh node
			// is a todo too (unchecked, since completedAt defaults to 0).
			if cur != nil && cur.typ == database.TypeTodo {
				it.typ = database.TypeTodo
			}
			// split the node at the caret: text after the caret moves into the new
			// sibling, the part before — and the node's children — stays. A mirror
			// reference, or a non-inline-editable type (json), is not split — it just
			// opens an empty sibling.
			if cur != nil && mc.editable && typeOf(cur.typ).inlineEditable {
				runes := []rune(cur.name)
				at := m.caret
				if at < 0 {
					at = 0
				}
				if at > len(runes) {
					at = len(runes)
				}
				it.name = string(runes[at:])
				cur.name = string(runes[:at])
			}
			m.unsaved = true
			m.refreshRows()
			m.cursor = m.findRow(it, mc.ctx)
			m.caret = 0
		}
		return m, nil
	case "tab":
		// path chips are inserted via the /file fuzzy picker now, not a "#…" token,
		// so Tab just indents (# is tags-only again)
		if m.tempActive {
			return m, nil // no indenting in the Agent Domain
		}
		if cur := m.cursorItem(); cur != nil {
			mc := m.mirrorContext()
			if m.tree.indent(cur) {
				m.unsaved = true
				m.refreshRows()
				// follow the cursor into the mirror we indented under, if any
				ctx := mc.ctx
				if mc.indentInto != nil {
					ctx = mc.indentInto
				}
				m.cursor = m.findRow(cur, ctx)
			}
		}
		return m, nil
	case "shift+tab":
		if m.tempActive {
			return m, nil // no outdenting in the Agent Domain
		}
		if cur := m.cursorItem(); cur != nil {
			mc := m.mirrorContext()
			if m.tree.outdent(cur, mc.localRoot) {
				m.unsaved = true
				m.refreshRows()
				m.cursor = m.findRow(cur, mc.ctx)
			}
		}
		return m, nil
	case "ctrl+@", "ctrl+space":
		if cur := m.cursorItem(); cur != nil && len(m.tree.childItems(cur)) > 0 {
			cur.collapsed = !cur.collapsed
			m.persistCollapsed(cur)
			m.refreshRows()
		}
		return m, nil
	case "ctrl+d", "alt+d", "ctrl+shift+backspace":
		// delete the whole node (its subtree confirms inline first)
		if cur := m.cursorItem(); cur != nil {
			if len(cur.children) > 0 {
				// children go with the node: confirm inline first
				m.mode = modeConfirm
			} else {
				m.deleteNode(cur)
			}
		}
		return m, nil
	// Delete the word to the left (or the whole chip just before the caret),
	// mirroring ctrl+left. ctrl+backspace arrives as ctrl+h in most terminals;
	// ctrl+w is the reliable readline alias (ctrl+shift+backspace is NOT a separable
	// key — the terminal sends the same byte as ctrl+backspace, so node-delete is
	// ctrl+d, not a backspace combo).
	case "ctrl+backspace", "ctrl+h", "ctrl+w":
		cur := m.cursorItem()
		if cur == nil || cur.mirrorOf != "" || !typeOf(cur.typ).inlineEditable || cur.readonly {
			return m, nil
		}
		runes := []rune(cur.name)
		if m.boundCaret(len(runes)) == 0 {
			return m, nil
		}
		spans := anchorSpans(runes)
		// caret right after a chip → delete just that chip
		if sp := spanEndingAt(spans, m.caret); sp != nil {
			m.deleteChipID(sp.id)
			cur.name = string(runes[:sp.start]) + string(runes[sp.end:])
			m.caret = sp.start
			m.unsaved = true
			return m, nil
		}
		target := prevWordBoundary(runes, m.caret)
		if sp := spanContaining(spans, target); sp != nil {
			target = sp.start // don't cut into a chip — take the whole thing
		}
		for _, sp := range spans { // drop chip records the deletion removes
			if sp.start >= target && sp.end <= m.caret {
				m.deleteChipID(sp.id)
			}
		}
		cur.name = string(runes[:target]) + string(runes[m.caret:])
		m.caret = target
		m.unsaved = true
		return m, nil
	case "ctrl+t":
		// convert a time phrase under the cursor to canonical date text (the renderer
		// then chips it)
		if cur := m.cursorItem(); cur != nil && m.mirrorContext().editable {
			if d := detectDate(cur.name, m.caret, time.Now()); d != nil && d.phrase != d.canonical() {
				runes := []rune(cur.name)
				date := d.canonical()
				cur.name = string(runes[:d.start]) + date + string(runes[d.end:])
				m.caret = d.start + len([]rune(date))
				m.unsaved = true
			}
		}
		return m, nil
	// every alt+arrow chord has a ctrl twin: terminals like windows
	// terminal grab alt+arrows for pane focus and never deliver them
	case "alt+shift+up", "ctrl+shift+up", "ctrl+alt+up":
		cur := m.cursorItem()
		if cur == nil {
			return m, nil
		}
		// at the top of the Agent Domain (the topmost agent, just under the compose
		// line), alt+shift+up moves the node out into the notes — crossing the divider
		// as if the two regions were one space.
		if m.tempActive && cur.parent == m.tempTree.root && indexOf(cur) == 1 {
			m.crossToNotes(cur)
			return m, nil
		}
		mc := m.mirrorContext()
		if m.tree.move(cur, -1, m.viewRoot()) {
			m.unsaved = true
			m.refreshRows()
			m.cursor = m.findRow(cur, mc.ctx)
		}
		return m, nil
	case "alt+shift+down", "ctrl+shift+down", "ctrl+alt+down":
		if cur := m.cursorItem(); cur != nil {
			mc := m.mirrorContext()
			if m.tree.move(cur, 1, m.viewRoot()) {
				m.unsaved = true
				m.refreshRows()
				m.cursor = m.findRow(cur, mc.ctx)
			}
		}
		return m, nil
	case "ctrl+right":
		// jump forward one word; at the end of the text, cross to the next node
		cur := m.cursorItem()
		if cur == nil {
			return m, nil
		}
		runes := []rune(cur.name)
		if m.caret >= len(runes) {
			if m.cursor < len(m.rows)-1 {
				m.cursor++
				m.caret = 0
			}
			return m, nil
		}
		m.caret = nextWordBoundary(runes, m.caret)
		if sp := spanContaining(anchorSpans(runes), m.caret); sp != nil {
			m.caret = sp.end // a chip is atomic
		}
		return m, nil
	case "ctrl+left":
		// jump back one word; at the start, cross to the previous node's end
		cur := m.cursorItem()
		if cur == nil {
			return m, nil
		}
		if m.caret <= 0 {
			if m.cursor > 0 {
				m.cursor--
				if c := m.cursorItem(); c != nil {
					m.caret = len([]rune(c.name))
				}
			}
			return m, nil
		}
		runes := []rune(cur.name)
		m.caret = prevWordBoundary(runes, m.caret)
		if sp := spanContaining(anchorSpans(runes), m.caret); sp != nil {
			m.caret = sp.start // a chip is atomic
		}
		return m, nil
	case "alt+right":
		// zoom into the cursor node — leaves too: the view starts empty
		// and typing adds the first child
		if cur := m.cursorItem(); cur != nil {
			// a mirror carries no children in memory; zoom into its source so the
			// original's children render — see mirrorContext, "zoom"
			if cur.mirrorOf != "" {
				src, ok := m.tree.byUUID[m.tree.sourceUUID(cur)]
				if !ok {
					return m, nil
				}
				cur = src
			}
			m.viewStack = append(m.viewStack, cur)
			m.cursor = 0
			m.caret = 0
			m.refreshRows()
		}
		return m, nil
	case "alt+left", "alt+backspace":
		// zoom back out
		if len(m.viewStack) > 1 {
			zoomed := m.viewRoot()
			m.viewStack = m.viewStack[:len(m.viewStack)-1]
			m.refreshRows()
			m.cursor = m.rowIndexOf(zoomed)
			m.caret = 0
		} else if base := m.viewStack[0]; base.uuid != "" && base.uuid != database.RootUUID {
			// at the loaded root: walk up to its parent in the forest, reloading the
			// tree there and focusing the node we came from
			if n, err := database.GetNode(m.db, base.uuid); err == nil && n.ParentUUID != "" {
				m.reopenAt(n.ParentUUID, base.uuid)
			}
		}
		return m, nil
	case "alt+g":
		// on a link chip, alt+g follows it (a node jumps, a URL opens in the
		// browser); off a chip it opens the /goto finder to jump to any node
		if cur := m.cursorItem(); cur != nil {
			if c, ok := m.linkChipAtCaret(cur); ok {
				return m.followLink(c)
			}
		}
		if !m.tempActive {
			m.openFinder(actGoto)
		}
		return m, nil
	case "alt+e":
		// toggle a type's inline expanded view (json/agent): alt+e focuses it,
		// alt+e again collapses. Else fall back to an action-only expand (voice play).
		if cur := m.cursorItem(); cur != nil {
			if v := nodeViewOf(cur); v != nil {
				if m.focused {
					v.Leave(m, cur)
					m.focused = false
				} else if v.Enter(m, cur) {
					m.focused = true
					m.focusScroll = 0
				}
			} else if c, ok := m.cmdChipAtCaret(cur); ok {
				m.openCmdView(c) // ⌥e on a cmd chip shows its full run output
				return m, nil
			} else if c, ok := m.linkChipAtCaret(cur); ok {
				m.openLinkEdit(c) // ⌥e on a link chip edits its name + target
				return m, nil
			} else if e := typeOf(cur.typ).expand; e != nil {
				return m, e(m, cur)
			} else if cmd := m.openPathChipCmd(cur); cmd != nil {
				return m, cmd // ⌥e on a node with a path chip opens the file in $EDITOR
			}
		}
		return m, nil
	case "alt+r":
		// run / re-run a runnable node's own action (bash/query/worker). Never
		// auto-runs; on an agent it re-runs the turn.
		if cur := m.cursorItem(); cur != nil {
			if c, ok := m.cmdChipAtCaret(cur); ok {
				return m, m.runCmdChip(c) // an inline cmd chip runs on its own
			}
			// a node mentioning an agent re-sends its thread to the session
			if ag, ok := m.mentionedAgent(expandAnchors(cur.name, m.chips)); ok {
				return m, m.sendThread(cur, ag)
			}
			if run := typeOf(cur.typ).run; run != nil {
				return m, run(m, cur)
			}
		}
		return m, nil
	case "alt+up", "ctrl+up":
		// collapse the cursor node
		if cur := m.cursorItem(); cur != nil && len(m.tree.childItems(cur)) > 0 && !cur.collapsed {
			ctx := m.mirrorContext().ctx
			cur.collapsed = true
			m.persistCollapsed(cur)
			m.refreshRows()
			m.cursor = m.findRow(cur, ctx)
		}
		return m, nil
	case "alt+down", "ctrl+down":
		// expand the cursor node
		if cur := m.cursorItem(); cur != nil && len(m.tree.childItems(cur)) > 0 && cur.collapsed {
			ctx := m.mirrorContext().ctx
			cur.collapsed = false
			m.persistCollapsed(cur)
			m.refreshRows()
			m.cursor = m.findRow(cur, ctx)
		}
		return m, nil
	case "up":
		starts := m.selectedVisualRows()
		line := caretVisualLine(starts, m.caret)
		if line > 0 {
			// walk up one visual line of the wrapped node first
			goal := m.caretColumn(starts, line)
			m.caret = m.caretAtColumn(starts, line-1, goal)
		} else if m.atTopOfTempList() {
			// at the top of the worker list: go back up into the main outline
			m.exitTemp()
		} else if m.cursor > 0 {
			// from the first visual line, cross to the previous node and land
			// on its last visual line, keeping the horizontal column
			goal := m.caretColumn(starts, 0)
			m.cursor--
			prev := m.selectedVisualRows()
			m.caret = m.caretAtColumn(prev, len(prev)-1, goal)
			m.clampCaret()
		}
		return m, nil
	case "down":
		starts := m.selectedVisualRows()
		line := caretVisualLine(starts, m.caret)
		if line < len(starts)-1 {
			// walk down one visual line of the wrapped node first
			goal := m.caretColumn(starts, line)
			m.caret = m.caretAtColumn(starts, line+1, goal)
		} else if m.cursor < len(m.rows)-1 {
			// from the last visual line, cross to the next node and land on its
			// first visual line, keeping the horizontal column
			goal := m.caretColumn(starts, line)
			m.cursor++
			m.caret = m.caretAtColumn(m.selectedVisualRows(), 0, goal)
			m.clampCaret()
		} else if !m.tempActive {
			// past the last node of the main outline: drop into the Temporary Domain
			m.enterTemp()
		}
		return m, nil
	case "left":
		if m.caret > 0 {
			m.caret--
			// a chip anchor is atomic: if the step landed inside one, jump to its start
			if cur := m.cursorItem(); cur != nil {
				if sp := spanContaining(anchorSpans([]rune(cur.name)), m.caret); sp != nil {
					m.caret = sp.start
				}
			}
		} else if m.cursor > 0 {
			// at the start of a node, cross to the previous node and land at its end
			m.cursor--
			if c := m.cursorItem(); c != nil {
				m.caret = len([]rune(c.name))
			}
		}
		return m, nil
	case "right":
		cur := m.cursorItem()
		if cur != nil && m.caret < len([]rune(cur.name)) {
			m.caret++
			// a chip anchor is atomic: if the step landed inside one, jump past it
			if sp := spanContaining(anchorSpans([]rune(cur.name)), m.caret); sp != nil {
				m.caret = sp.end
			}
		} else if cur != nil && m.cursor < len(m.rows)-1 {
			// at the end of a node, cross to the next node and land at its start
			m.cursor++
			m.caret = 0
		}
		return m, nil
	case "home":
		// move to the first position of the current visual line, not the start
		// of the whole node: a wrapped node has several visual lines
		starts := m.selectedVisualRows()
		line := caretVisualLine(starts, m.caret)
		m.caret = starts[line]
		return m, nil
	case "end":
		// move to the last position of the current visual line, not the end of
		// the whole node: a wrapped node has several visual lines. On the final
		// visual line this is the node end.
		cur := m.cursorItem()
		if cur == nil {
			return m, nil
		}
		runes := []rune(cur.name)
		starts := m.selectedVisualRows()
		line := caretVisualLine(starts, m.caret)
		if line+1 >= len(starts) {
			m.caret = len(runes)
			return m, nil
		}
		// stop before the next line's start; a space consumed by the wrap break
		// lands the caret just before it, mirroring the on-break-space render.
		end := starts[line+1]
		if end > 0 && end <= len(runes) && runes[end-1] == ' ' {
			end--
		}
		m.caret = end
		return m, nil
	case "backspace":
		cur := m.cursorItem()
		// a mirror reference is edited at its original — see mirrorContext
		if cur == nil || cur.mirrorOf != "" {
			return m, nil
		}
		if !typeOf(cur.typ).inlineEditable || cur.readonly {
			return m, nil // e.g. json — edited only in the alt+e editor; or a locked node
		}
		if runes := []rune(cur.name); m.boundCaret(len(runes)) > 0 {
			// backspace at a chip anchor's end deletes the whole chip (anchor + record)
			if sp := spanEndingAt(anchorSpans(runes), m.caret); sp != nil {
				m.deleteChipID(sp.id)
				cur.name = string(runes[:sp.start]) + string(runes[sp.end:])
				m.caret = sp.start
				m.unsaved = true
				return m, nil
			}
			cur.name = string(runes[:m.caret-1]) + string(runes[m.caret:])
			m.caret--
			m.unsaved = true
			return m, nil
		}
		// backspace on an empty non-bullet node demotes its type to a plain bullet
		// first (e.g. Bash → bullet → delete), so a special node isn't blown away in
		// one keypress — the next backspace then merges/removes the bullet.
		if cur.name == "" && cur.mirrorOf == "" && typeOf(cur.typ).key != database.TypeBullets {
			cur.typ = database.TypeBullets
			m.unsaved = true
			return m, nil
		}
		// caret at the start: merge this node into the one above. Its text appends
		// to the previous node and its children move under that node.
		if m.cursor > 0 {
			prev := m.rows[m.cursor-1].it
			if prev.mirrorOf != "" {
				return m, nil // can't merge into a mirror reference
			}
			// merging up into a blank placeholder line: the absorbed node is really
			// the content, so carry its style/type/collapsed across — otherwise
			// backspacing a red, collapsed node into an empty line above it would
			// silently drop its colour and re-expand its children.
			if prev.name == "" && prev.style == "" && len(prev.children) == 0 {
				prev.style = cur.style
				prev.typ = cur.typ
				prev.completedAt = cur.completedAt
				prev.collapsed = cur.collapsed
				if m.tree.db != nil {
					_ = database.SetCollapsed(m.tree.db, prev.uuid, cur.collapsed)
				}
			}
			mergeAt := len([]rune(prev.name))
			prev.name += cur.name
			for _, c := range cur.children {
				c.parent = prev
			}
			prev.children = append(prev.children, cur.children...)
			cur.children = nil
			m.tree.remove(cur)
			m.unsaved = true
			m.refreshRows()
			m.cursor = m.rowIndexOf(prev)
			m.clampCursor()
			m.caret = mergeAt
			return m, nil
		}
		// the first node and empty: just remove it
		if cur.name == "" && len(cur.children) == 0 {
			m.tree.remove(cur)
			m.unsaved = true
			m.ensureViewNonEmpty()
			m.refreshRows()
		}
		return m, nil
	}

	// printable input (space arrives as KeySpace, not KeyRunes)
	if k.Type == tea.KeySpace && !k.Alt {
		k.Type = tea.KeyRunes
		k.Runes = []rune{' '}
	}
	if k.Type == tea.KeyRunes && len(k.Runes) > 0 && !k.Alt {
		cur := m.cursorItem()
		if cur == nil {
			// empty view: create the first node
			it, err := m.tree.insertFirstChild(m.viewRoot())
			if err != nil {
				m.err = err
				return m.quit()
			}
			m.refreshRows()
			m.cursor = m.rowIndexOf(it)
			m.caret = 0
			cur = it
		}

		// "/" opens the slash menu anywhere in the row. On editable rows it
		// is typed into the text and stripped when a command runs or the menu
		// is cancelled, so esc restores the name to what it was before.
		if string(k.Runes) == "/" && !k.Paste {
			m.mode = modeSlash
			m.slashQuery = ""
			m.slashSel = 0
			m.slashInline = cur.mirrorOf == "" && !cur.readonly
			if m.slashInline {
				runes := []rune(cur.name)
				m.boundCaret(len(runes))
				cur.name = string(runes[:m.caret]) + "/" + string(runes[m.caret:])
				m.slashStart = m.caret
				m.caret++
				m.unsaved = true
			}
			return m, nil
		}

		// ">" opens the file picker to splice a path chip at the caret — the chip
		// renders as "›name", so ">" is its natural trigger. Skipped where ">" is
		// real syntax (bash redirects, code, queries), and after a "-" so the "->"
		// log gesture can form instead of grabbing the picker.
		if string(k.Runes) == ">" && !k.Paste && cur.mirrorOf == "" && !cur.readonly &&
			pathChipTrigger(cur.typ) && !runeBeforeCaretIs(cur, m.caret, '-') {
			return m, m.openFilePicker(cur)
		}

		// "[[" opens the link picker: the second "[" drops the first and opens the
		// finder where you pick a node or type/paste a URL. Same type guard as the
		// path chip ("[" stays literal in code/bash/query/quote/json).
		if string(k.Runes) == "[" && !k.Paste && cur.mirrorOf == "" && !cur.readonly &&
			pathChipTrigger(cur.typ) && runeBeforeCaretIs(cur, m.caret, '[') {
			runes := []rune(cur.name)
			m.boundCaret(len(runes))
			cur.name = string(runes[:m.caret-1]) + string(runes[m.caret:])
			m.caret--
			m.unsaved = true
			m.openFinder(actLinkInsert)
			return m, nil
		}

		// "#" opens the tag completer at a word boundary; ":" opens the query-command
		// completer in a query node. Both stay literal mid-word so "C#"/"a:b" type
		// normally; tags skip bash/code where "#" is a comment.
		if string(k.Runes) == "#" && !k.Paste && cur.mirrorOf == "" && !cur.readonly &&
			tagPickerTrigger(cur.typ) && atWordStart(cur, m.caret) {
			return m.openCompleter(cur, complTag, "#")
		}
		if string(k.Runes) == ":" && !k.Paste && cur.mirrorOf == "" && !cur.readonly &&
			cur.typ == database.TypeQuery && atWordStart(cur, m.caret) {
			return m.openCompleter(cur, complQueryCmd, ":")
		}
		// "@" opens the agent picker at a word boundary — the mention stays
		// plain text; Enter on the node later sends the thread (see agent.go)
		if string(k.Runes) == "@" && !k.Paste && cur.mirrorOf == "" && !cur.readonly &&
			len(m.agents) > 0 && tagPickerTrigger(cur.typ) && atWordStart(cur, m.caret) {
			return m.openCompleter(cur, complAgent, "@")
		}

		if cur.mirrorOf != "" {
			return m, nil // a mirror reference is edited at its original — see mirrorContext
		}
		if !typeOf(cur.typ).inlineEditable || cur.readonly {
			return m, nil // e.g. json — edited only in the alt+e editor; or a locked node (slash above still works)
		}

		text := string(k.Runes)
		if k.Paste {
			if lines := pasteLines(text); len(lines) > 1 {
				return m.pasteFanOut(cur, lines)
			} else if len(lines) == 1 {
				text = lines[0]
			} else {
				text = ""
			}
		}

		// guard against a caret left stale by a cursor move (e.g. a worker run
		// reseeding the compose) — slicing runes[:m.caret] would otherwise panic
		m.boundCaret(len([]rune(cur.name)))

		// typing a space commits a sign prefix ("$ "→bash, "-> "/"→ "→log) into the
		// node's type, or a #tag / date token before it into a chip.
		if text == " " && !k.Paste {
			if m.convertBySign(cur) {
				return m, nil // the sign became the type; the space is consumed
			}
			if m.bashCmdBeforeCaret(cur) {
				return m, nil // a "$cmd" + double space committed a cmd chip
			}
			m.chipifyBeforeCaret(cur)
		}

		runes := []rune(cur.name)
		m.boundCaret(len(runes)) // chipify may have changed the name/caret
		ins := []rune(text)
		cur.name = string(runes[:m.caret]) + string(ins) + string(runes[m.caret:])
		m.caret += len(ins)
		m.unsaved = true
		m.maybeLinkToMirror(cur)
		return m, nil
	}

	return m, nil
}

// pathChipTrigger reports whether ">" should open the file picker on this type.
// Text-ish nodes get it; types where ">" is real syntax (bash redirect, code,
// query, quote, json) keep ">" literal.
func pathChipTrigger(typ string) bool {
	switch typ {
	case database.TypeBash, database.TypeCode, database.TypeQuery, database.TypeQuote, database.TypeJSON:
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
	case database.TypeBash, database.TypeCode:
		return false
	}
	return typeOf(typ).inlineEditable
}

// convertBySign turns a sign typed at the very start of a node into that node's
// type — the keyboard-only counterpart to /type. It fires on the space after the
// sign (so the whole pre-caret text IS the sign), converts, and strips the sign
// (a bash node renders its own "$ " prefix; a log node its own "→"), leaving the
// caret at the start of the remaining text. Works from any type, so it doubles as
// the reverse conversion (type "$ " on a log node to make it bash, and vice versa).
func (m *Model) convertBySign(cur *item) bool {
	if cur == nil || cur.mirrorOf != "" || cur.readonly {
		return false
	}
	runes := []rune(cur.name)
	if m.caret > len(runes) {
		return false
	}
	var newType string
	switch string(runes[:m.caret]) {
	case "$":
		newType = database.TypeBash
	case "->", "→":
		newType = database.TypeLog
	default:
		return false
	}
	if cur.typ == newType {
		return false // already that type — let the space type normally
	}
	m.pushUndo("")
	cur.typ = newType
	cur.name = string(runes[m.caret:]) // drop the sign; the type renders its own
	m.caret = 0
	m.unsaved = true
	return true
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
	seen := map[string]bool{}
	for n.MirrorOf != "" && !seen[n.UUID] {
		seen[n.UUID] = true
		orig, err := database.GetNode(m.db, n.MirrorOf)
		if err != nil {
			break
		}
		n = orig
	}
	return n
}

// copyToClipboard puts s on the system clipboard via OSC 52, written to
// stderr so it bypasses the bubbletea renderer owning stdout.
func copyToClipboard(s string) {
	seq := osc52.New(s)
	if os.Getenv("TMUX") != "" {
		seq = seq.Tmux()
	}
	_, _ = seq.WriteTo(os.Stderr)
}

// deleteNode removes the node and its subtree from the tree.
func (m *Model) deleteNode(it *item) {
	// drop each removed node's persisted run-output cache so it doesn't outlive it
	var dropRunOut func(x *item)
	dropRunOut = func(x *item) {
		m.deleteRunOut(x.uuid)
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
		if cur := m.cursorItem(); cur != nil {
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
// edit against a caret left stale by a cursor move (e.g. a worker run reseeding
// the compose line), which would otherwise panic slicing runes[:m.caret].
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

func (m *Model) filteredSlash() []slashCommand {
	if m.slashQuery == "" {
		return slashCommands
	}
	var ret []slashCommand
	for _, c := range slashCommands {
		if strings.Contains(c.name, strings.ToLower(m.slashQuery)) {
			ret = append(ret, c)
		}
	}
	return ret
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
	end := m.slashStart + 1 + len([]rune(m.slashQuery))
	if end > len(runes) {
		end = len(runes)
	}
	cur.name = string(runes[:m.slashStart]) + string(runes[end:])
	m.caret = m.slashStart
}

func (m *Model) handleSlashKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	cur := m.cursorItem()

	switch k.String() {
	case "esc":
		// escape just closes the menu and LEAVES the typed "/query" as literal text,
		// so you can write "/pa" without the menu swallowing it. (The slash menu only
		// strips its text when a command actually runs.)
		m.mode = modeOutline
		return m, nil
	case "up":
		if m.slashSel > 0 {
			m.slashSel--
		}
		return m, nil
	case "down":
		if m.slashSel < len(m.filteredSlash())-1 {
			m.slashSel++
		}
		return m, nil
	case "backspace":
		if qr := []rune(m.slashQuery); len(qr) > 0 {
			m.slashQuery = string(qr[:len(qr)-1])
			m.slashSel = 0
			if m.slashInline && cur != nil && m.caret > 0 {
				runes := []rune(cur.name)
				cur.name = string(runes[:m.caret-1]) + string(runes[m.caret:])
				m.caret--
			}
		} else {
			if m.slashInline && cur != nil {
				m.stripSlashText()
			}
			m.mode = modeOutline
		}
		return m, nil
	case "enter":
		cmds := m.filteredSlash()
		if m.slashSel < len(cmds) {
			m.stripSlashText()
			return m.runSlash(cmds[m.slashSel].name)
		}
		m.mode = modeOutline
		return m, nil
	}

	if k.Type == tea.KeySpace && !k.Alt {
		k.Type, k.Runes = tea.KeyRunes, []rune{' '}
	}
	if k.Type == tea.KeyRunes && !k.Alt {
		m.slashQuery += string(k.Runes)
		m.slashSel = 0
		if m.slashInline && cur != nil {
			runes := []rune(cur.name)
			ins := []rune(string(k.Runes))
			cur.name = string(runes[:m.caret]) + string(ins) + string(runes[m.caret:])
			m.caret += len(ins)
		}
		// nothing matches anymore: it was ordinary text, keep it as typed
		if len(m.filteredSlash()) == 0 {
			m.mode = modeOutline
		}
	}
	return m, nil
}

// filteredTypes returns the node-type keys matching the picker's search query,
// in registry order: built-ins first, then installed artifacts. The query
// fuzzy-matches label and key (subsequence, case-insensitive), so twenty
// forgotten artifacts never bury Todo.
func (m *Model) filteredTypes() []string {
	q := strings.ToLower(m.typeQuery)
	var ret []string
	for _, t := range typeOrder() {
		if typeOf(t).tempOnly && !m.tempActive {
			continue // temp-only types (worker) are not offered in the notebook
		}
		if q != "" && !fuzzyMatch(strings.ToLower(typeLabel(t)), q) && !fuzzyMatch(t, q) {
			continue
		}
		ret = append(ret, t)
	}
	return ret
}

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

func (m *Model) handleTypeKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "esc":
		m.mode = modeOutline
		return m, nil
	case "up":
		if m.typeSel > 0 {
			m.typeSel--
		}
		return m, nil
	case "down":
		if m.typeSel < len(m.filteredTypes())-1 {
			m.typeSel++
		}
		return m, nil
	case "backspace":
		if qr := []rune(m.typeQuery); len(qr) > 0 {
			m.typeQuery = string(qr[:len(qr)-1])
			m.typeSel = 0
		} else {
			m.mode = modeOutline
		}
		return m, nil
	case "enter":
		filt := m.filteredTypes()
		cur := m.cursorItem()
		if cur != nil && m.typeSel < len(filt) {
			m.pushUndo("")
			cur.typ = filt[m.typeSel]
			m.unsaved = true
		}
		m.mode = modeOutline
		return m, nil
	}
	// any other rune extends the search query
	if k.Type == tea.KeySpace && !k.Alt {
		k.Type, k.Runes = tea.KeyRunes, []rune{' '}
	}
	if k.Type == tea.KeyRunes && !k.Alt {
		m.typeQuery += string(k.Runes)
		m.typeSel = 0
	}
	return m, nil
}

func (m *Model) handleStyleKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "esc":
		m.mode = modeOutline
		return m, nil
	case "up":
		if m.styleSel > 0 {
			m.styleSel--
		}
		return m, nil
	case "down":
		if m.styleSel < len(stylePickerItems)-1 {
			m.styleSel++
		}
		return m, nil
	case "enter":
		cur := m.cursorItem()
		if cur != nil {
			m.pushUndo("")
			it := stylePickerItems[m.styleSel]
			if it.kind == "toggle" {
				cur.style = styleToggle(cur.style, it.value)
			} else {
				cur.style = styleSetColor(cur.style, it.value)
			}
			m.unsaved = true
		}
		m.mode = modeOutline
		return m, nil
	}
	return m, nil
}

func (m *Model) handleThemeKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "esc":
		m.mode = modeOutline
		return m, nil
	case "up":
		if m.themeSel > 0 {
			m.themeSel--
		}
		return m, nil
	case "down":
		if m.themeSel < len(themes)-1 {
			m.themeSel++
		}
		return m, nil
	case "enter":
		if m.themeSel >= 0 && m.themeSel < len(themes) {
			applyTheme(themes[m.themeSel])
			m.saveTheme(themes[m.themeSel].name)
			m.flash = "theme · " + activeThemeName
		}
		m.mode = modeOutline
		return m, nil
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
	case "/artifacts":
		m.mode = modeArtifacts
		m.artSel = 0
	case "/type":
		// open the picker; pre-select the type already in effect, if any
		m.mode = modeType
		m.typeSel = 0
		m.typeQuery = ""
		for i, t := range typeOrder() {
			if t == cur.typ {
				m.typeSel = i
				break
			}
		}
	case "/style":
		// open the picker; pre-select the first active toggle, then the
		// active color, otherwise default to the first item.
		m.mode = modeStyle
		m.styleSel = 0
		for i, it := range stylePickerItems {
			if it.kind == "toggle" && styleHas(cur.style, it.value) {
				m.styleSel = i
				break
			}
		}
		if m.styleSel == 0 {
			c := styleColor(cur.style)
			for i, it := range stylePickerItems {
				if it.kind == "color" && it.value == c {
					m.styleSel = i
					break
				}
			}
		}
	case "/theme":
		// open the palette picker; pre-select the theme already in effect
		m.mode = modeTheme
		m.themeSel = 0
		for i, t := range themes {
			if t.name == activeThemeName {
				m.themeSel = i
				break
			}
		}
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
		if cur.completedAt > 0 {
			cur.completedAt = 0
		} else {
			cur.completedAt = time.Now().Unix()
		}
		m.unsaved = true
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
	case "/file":
		// fuzzy-pick a file with fzf, then splice a path chip in at the caret
		return m, m.openFilePicker(cur)
	case "/note":
		// a mirror is the same node everywhere: edit the original's note
		cur = m.tree.resolve(cur)
		m.mode = modeNote
		m.notePrev = cur.note
		m.caret = len([]rune(cur.note))
	case "/bring":
		// pick any node (incl. an Agent Domain agent) and move it here
		m.openFinder(actBringHere)
	case "/mirror":
		m.openFinder(actMirrorHere)
	case "/link":
		// splice an inline link chip at the caret (same as the [[ trigger)
		m.openFinder(actLinkInsert)
	case "/move":
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
	case "backspace":
		runes := []rune(cur.note)
		if m.caret > 0 && m.caret <= len(runes) {
			cur.note = string(runes[:m.caret-1]) + string(runes[m.caret:])
			m.caret--
		}
		return m, nil
	case "left":
		if m.caret > 0 {
			m.caret--
		}
		return m, nil
	case "right":
		if m.caret < len([]rune(cur.note)) {
			m.caret++
		}
		return m, nil
	}
	if k.Type == tea.KeySpace && !k.Alt {
		k.Type, k.Runes = tea.KeyRunes, []rune{' '}
	}
	if k.Type == tea.KeyRunes && !k.Alt {
		runes := []rune(cur.note)
		ins := []rune(sanitizeName(string(k.Runes)))
		cur.note = string(runes[:m.caret]) + string(ins) + string(runes[m.caret:])
		m.caret += len(ins)
	}
	return m, nil
}

func (m *Model) openFinder(act finderAction) {
	m.mode = modeFinder
	m.finderAct = act
	m.finderQuery = ""
	m.finderSel = 0
	m.finderHits = nil
	m.refreshFinder()
}

func (m *Model) refreshFinder() {
	// an empty query matches everything, recent first: the picker starts
	// full and narrows as you type
	var hits []database.Node
	var err error
	if strings.TrimSpace(m.finderQuery) == "" {
		hits, err = database.RecentNodes(m.db, 100)
	} else {
		hits, err = database.SearchNodes(m.db, m.finderQuery, true)
	}
	if err != nil {
		m.finderHits = nil
		return
	}
	// the node being acted on is never a valid target
	cur := m.cursorItem()
	var filtered []database.Node
	for _, h := range hits {
		if cur != nil && h.UUID == cur.uuid {
			continue
		}
		// /goto is a jump target list: a node with no name and no mirror is empty
		// noise, so leave it out
		if m.finderAct == actGoto && h.Name == "" && h.MirrorOf == "" {
			continue
		}
		filtered = append(filtered, h)
	}
	// /bring can also pull a node out of the (ephemeral, DB-less) Agent Domain, so
	// surface its agents alongside the saved nodes — most recent first.
	if m.finderAct == actBringHere {
		filtered = append(m.tempFinderHits(cur), filtered...)
	}
	m.finderHits = filtered
	if m.finderSel >= len(m.finderHits) {
		m.finderSel = 0
	}
}

// tempFinderHits returns the Agent Domain's named nodes as finder candidates,
// synthesized as database.Node so they sit in the same picker list as saved nodes.
// The always-empty compose line and the cursor node are skipped.
func (m *Model) tempFinderHits(cur *item) []database.Node {
	if m.tempTree == nil || m.tempTree == m.tree {
		return nil // no domain, or we're already inside it
	}
	q := strings.ToLower(strings.TrimSpace(m.finderQuery))
	var hits []database.Node
	for _, it := range m.tempTree.root.children {
		name := strings.TrimSpace(it.name)
		if name == "" || (cur != nil && it.uuid == cur.uuid) {
			continue
		}
		if q != "" && !strings.Contains(strings.ToLower(name), q) {
			continue
		}
		hits = append(hits, database.Node{UUID: it.uuid, Name: it.name, Type: it.typ})
	}
	return hits
}

func (m *Model) handleFinderKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "esc":
		m.mode = modeOutline
		return m, nil
	case "up":
		if m.finderSel > 0 {
			m.finderSel--
		}
		return m, nil
	case "down":
		if m.finderSel < len(m.finderHits)-1 {
			m.finderSel++
		}
		return m, nil
	case "backspace":
		if len(m.finderQuery) > 0 {
			m.finderQuery = m.finderQuery[:len(m.finderQuery)-1]
			m.refreshFinder()
		}
		return m, nil
	case "enter":
		// [[ accepts a URL typed/pasted straight into the query — link to the
		// website instead of a node
		if m.finderAct == actLinkInsert && browser.IsURL(m.finderQuery) {
			return m.insertURLLink(m.finderQuery)
		}
		if m.finderSel < len(m.finderHits) {
			return m.runFinder(m.finderHits[m.finderSel])
		}
		m.mode = modeOutline
		return m, nil
	}
	if k.Type == tea.KeySpace && !k.Alt {
		k.Type, k.Runes = tea.KeyRunes, []rune{' '}
	}
	if k.Type == tea.KeyRunes && !k.Alt {
		m.finderQuery += string(k.Runes)
		m.refreshFinder()
	}
	return m, nil
}

func (m *Model) runFinder(target database.Node) (tea.Model, tea.Cmd) {
	m.mode = modeOutline
	cur := m.cursorItem()
	if cur == nil {
		return m, nil
	}

	switch m.finderAct {
	case actMirrorHere:
		m.pushUndo("")
		target = m.resolveSourceNode(target)
		if cur.name == "" && cur.mirrorOf == "" && len(cur.children) == 0 {
			// the empty node where "/" was typed becomes the mirror
			cur.mirrorOf = target.UUID
		} else {
			it, err := m.tree.insertSiblingAfter(cur)
			if err != nil {
				m.err = err
				return m.quit()
			}
			it.mirrorOf = target.UUID
			m.refreshRows()
			m.cursor = m.rowIndexOf(it)
		}
		if _, inTree := m.tree.byUUID[target.UUID]; !inTree {
			m.tree.externalNames[target.UUID] = target.Name
		}
		m.unsaved = true
	case actMoveTo:
		m.pushUndo("")
		if targetItem, inTree := m.tree.byUUID[target.UUID]; inTree {
			if m.tree.reparent(cur, targetItem) {
				m.unsaved = true
				m.refreshRows()
				m.cursor = m.rowIndexOf(cur)
			}
		} else {
			// moving out of the open subtree: persist everything, then move in db
			if err := m.moveToDB(cur, target); err != nil {
				m.err = err
				return m.quit()
			}
		}
	case actGoto:
		// save, then reopen on the target
		if _, err := m.saveAll(); err != nil {
			m.err = err
			return m.quit()
		}
		t, err := loadTree(m.db, target.UUID)
		if err != nil {
			m.err = err
			return m.quit()
		}
		m.tree = t
		m.viewStack = []*item{t.root}
		m.undoStack = nil
		m.refreshAncestors()
		m.cursor = 0
		m.caret = 0
		m.unsaved = false
	case actLinkInsert:
		// insert an inline link chip pointing at the picked node (the original,
		// never a mirror), its name defaulting to the node's name. Resolve the
		// target's chip anchors to display text first: a node whose title carries
		// a chip (e.g. a #tag) stores a raw "￼id￼" anchor in its name, and that
		// must never become a link label — it leaks the chip id and corrupts the
		// editor's anchor invariant (see createLabeledChip's sentinel guard).
		dst := m.resolveSourceNode(target)
		label := displayAnchors(dst.Name, m.chips)
		m.insertLinkChip(nodeLinkURI(dst.UUID), label)
		m.flash = "linked → " + clipStr(label, 24)
	case actBringHere:
		// move the picked node (and its subtree) to the cursor location.
		m.pushUndo("")
		if src, ok := m.tempTree.byUUID[target.UUID]; ok && m.tempTree != m.tree {
			m.bringFromTemp(src, cur) // pull an agent out of the Agent Domain
		} else if it, inTree := m.tree.byUUID[target.UUID]; inTree {
			m.bringWithin(it, cur) // already in the open subtree
		} else if err := m.bringFromDB(target, cur); err != nil {
			m.err = err
			return m.quit()
		}
	}

	m.refreshRows()
	return m, nil
}

func (m *Model) moveToDB(cur *item, target database.Node) error {
	if _, err := m.saveAll(); err != nil {
		return err
	}
	m.unsaved = false

	rank, err := database.NextRank(m.db, target.UUID)
	if err != nil {
		return err
	}
	if _, err := m.db.Exec("UPDATE nodes SET parent_uuid = ?, rank = ? WHERE uuid = ?",
		target.UUID, rank, cur.uuid); err != nil {
		return errors.Wrap(err, "moving node")
	}

	// detach from the in-memory tree without tombstoning
	if idx := indexOf(cur); idx >= 0 {
		cur.parent.children = append(cur.parent.children[:idx], cur.parent.children[idx+1:]...)
	}
	m.refreshRows()
	return nil
}

// placeBrought splices an already-detached subtree in as a sibling right after cur,
// registers it (and its descendants) in the current tree's index, and moves the
// cursor onto it. Used by /bring once the source has been unhooked from its origin.
func (m *Model) placeBrought(it, cur *item) {
	parent := cur.parent
	it.parent = parent
	idx := indexOf(cur)
	parent.children = append(parent.children, nil)
	copy(parent.children[idx+2:], parent.children[idx+1:])
	parent.children[idx+1] = it

	var reg func(x *item)
	reg = func(x *item) {
		m.tree.byUUID[x.uuid] = x
		for _, c := range x.children {
			reg(c)
		}
	}
	reg(it)

	m.unsaved = true
	m.refreshRows()
	m.cursor = m.rowIndexOf(it)
	m.flash = "brought here"
}

// bringFromTemp migrates an agent (and its subtree) out of the ephemeral Agent
// Domain into the main tree at the cursor. Any live process keeps running — the run
// machinery is keyed by uuid, not by which tree owns the node. The Agent Domain
// keeps its compose line afterward.
func (m *Model) bringFromTemp(src, cur *item) {
	if idx := indexOf(src); idx >= 0 {
		src.parent.children = append(src.parent.children[:idx], src.parent.children[idx+1:]...)
	}
	var migrate func(x *item)
	migrate = func(x *item) {
		delete(m.tempTree.byUUID, x.uuid)
		if s, ok := m.tempTree.snapshots[x.uuid]; ok {
			delete(m.tempTree.snapshots, x.uuid)
			m.tree.snapshots[x.uuid] = s
		}
		for _, c := range x.children {
			migrate(c)
		}
	}
	migrate(src)
	m.placeBrought(src, cur)
}

// bringWithin relocates a node already loaded in the open subtree to sit right after
// cur. Bringing a node into its own subtree is a no-op.
func (m *Model) bringWithin(it, cur *item) {
	for p := cur; p != nil; p = p.parent {
		if p == it {
			m.flash = "can't bring a node into itself"
			return
		}
	}
	if idx := indexOf(it); idx >= 0 {
		it.parent.children = append(it.parent.children[:idx], it.parent.children[idx+1:]...)
	}
	parent := cur.parent
	it.parent = parent
	idx := indexOf(cur)
	parent.children = append(parent.children, nil)
	copy(parent.children[idx+2:], parent.children[idx+1:])
	parent.children[idx+1] = it
	m.unsaved = true
	m.refreshRows()
	m.cursor = m.rowIndexOf(it)
	m.flash = "brought here"
}

// bringFromDB moves a node that lives elsewhere in the database under the cursor's
// parent, then reloads the open view so the brought subtree appears. Like moveToDB
// but in the opposite direction (target → here rather than here → target).
func (m *Model) bringFromDB(target database.Node, cur *item) error {
	if _, err := m.saveAll(); err != nil {
		return err
	}
	m.unsaved = false

	parentUUID := cur.parent.uuid
	rank, err := database.NextRank(m.db, parentUUID)
	if err != nil {
		return err
	}
	if _, err := m.db.Exec("UPDATE nodes SET parent_uuid = ?, rank = ? WHERE uuid = ?",
		parentUUID, rank, target.UUID); err != nil {
		return errors.Wrap(err, "bringing node")
	}

	root := m.viewRoot()
	t, err := loadTree(m.db, root.uuid)
	if err != nil {
		return err
	}
	m.tree = t
	m.viewStack = []*item{t.root}
	m.refreshAncestors()
	m.refreshRows()
	if it, ok := t.byUUID[target.UUID]; ok {
		m.cursor = m.rowIndexOf(it)
	}
	m.clampCursor()
	m.flash = "brought here"
	return nil
}

func (m *Model) quit() (tea.Model, tea.Cmd) {
	// stop any live agent processes (kept alive across turns for steering)
	for _, cancel := range m.runCancel {
		cancel()
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
			}
		}
	}
	m.quitting = true
	return m, tea.Quit
}

// View implements tea.Model.
func (m *Model) View() string {
	width := m.width
	if width <= 0 {
		width = 80
	}
	maxLine := width - 1 // never touch the last column: deferred-wrap desync

	if m.quitting {
		if m.err != nil {
			return ""
		}
		// the final frame is what the terminal scrollback keeps: the whole
		// outline, fully expanded, styled exactly like the live editor. The
		// trailing newline matters: the renderer erases the last line of the
		// final frame on shutdown, so give it an empty one to eat.
		return strings.Join(m.finalView(maxLine), "\n") + "\n"
	}

	var lines []string

	if m.mode == modeFinder {
		lines = m.viewFinder(maxLine)
	} else if m.mode == modeLinkEdit {
		lines = m.viewLinkEdit(maxLine)
	} else if m.mode == modeCmdView {
		lines = m.viewCmdView(maxLine)
	} else {
		lines = m.viewOutline(maxLine)
	}

	// The inline renderer (no alt screen) can only move the cursor back over the
	// lines of the previous frame — it cannot reach into scrollback. A frame
	// taller than the terminal therefore strands its top lines: on the next
	// flush the renderer clears only what it last rendered, leaving the overflow
	// behind, which is what doubles the outline across a shrink-then-grow resize.
	// Cap every frame at the window height so each node renders exactly once.
	if m.height > 0 && len(lines) > m.height {
		lines = lines[:m.height]
	}

	// Erase the line before drawing it, not after. The inline renderer rewrites
	// lines in place without clearing, so a frame that grows after a shrink would
	// leave the previous narrower line's trailing cells behind the new one — the
	// 40-col and 60-col renders overlaid. cClearEOL erases from the cursor to the
	// end of the line; the renderer leaves the cursor at column 0 before painting
	// each row, so prefixing clears the whole row first. It must lead the line: a
	// full-width row is truncated to the terminal width by the renderer, and that
	// truncation drops any escape bytes past the cut — a trailing cClearEOL would
	// be silently discarded on exactly the wide rows that need clearing.
	for i, l := range lines {
		lines[i] = cClearEOL + l + cReset
	}

	return strings.Join(lines, "\n")
}

// finalView renders the complete tree with glyphs and connectors but no
// cursor, caret or bottom bar. Long rows wrap.
func (m *Model) finalView(maxLine int) []string {
	var lines []string
	allRows := m.tree.allRows()
	for i, r := range allRows {
		below := i+1 < len(allRows) && allRows[i+1].depth > r.depth
		if r.it.typ == database.TypeDivider {
			lines = append(lines, dividerLine(r, maxLine, false))
			lines = append(lines, m.noteBandLines(r, maxLine, below, -1)...)
			continue
		}
		glyph, glyphColor := glyphFor(r.it)
		if r.mirrored {
			glyph, glyphColor = glyphMirror, cDim
		}
		name := m.tree.displayName(r.it)
		body := renderBody(r.it, name, -1, false, m.chips)
		if rm := typeOf(r.it.typ).renderM; rm != nil {
			body = rm(m, r.it)
		}
		line := " " + cDim + connector(r) + glyphColor + glyph + cReset + " " + body + m.typeSuffix(r.it)
		lines = append(lines, wrapLine(line, maxLine, continuationPrefix(r, below))...)
		lines = append(lines, m.noteBandLines(r, maxLine, below, -1)...)
	}
	return lines
}

func (m *Model) viewOutline(maxLine int) []string {
	var lines []string

	rows := m.rows
	if len(rows) == 0 {
		lines = append(lines, cDim+" empty - type to add a node"+cReset)
	}

	// render every row to its wrapped lines first: the viewport then works
	// in screen lines, so wrapped rows never push the cursor off screen
	groups := make([][]string, len(rows))
	bands := make([][]string, len(rows))
	for i, r := range rows {
		it := r.it
		selected := i == m.cursor

		// a divider is a full-width rule with no glyph/body; it still hangs a note
		if it.typ == database.TypeDivider {
			below := i+1 < len(rows) && rows[i+1].depth > r.depth
			groups[i] = []string{dividerLine(r, maxLine, selected)} // single line, never wrapped
			noteCaret := -1
			if selected && m.mode == modeNote {
				noteCaret = m.caret
			}
			bands[i] = m.noteBandLines(r, maxLine, below, noteCaret)
			continue
		}

		glyph, glyphColor := glyphFor(it)
		if r.mirrored {
			glyph, glyphColor = glyphMirror, cDim
		}
		if m.tempActive && !r.mirrored {
			glyph = glyphDotted // every Temporary Domain node shows a dashed icon
		}
		if selected {
			glyphColor = cRed
		}
		name := m.tree.displayName(it)

		caret := -1
		if selected && m.mode != modeNote && it.mirrorOf == "" {
			caret = m.caret
		}
		body := renderBody(it, name, caret, selected, m.chips)
		if rm := typeOf(it.typ).renderM; rm != nil {
			body = rm(m, it) // Model-aware override (voice waveform)
		}

		line := " " + cDim + connector(r) + glyphColor + glyph + cReset + " " + body + m.typeSuffix(it)

		below := i+1 < len(rows) && rows[i+1].depth > r.depth
		groups[i] = wrapLine(line, maxLine, continuationPrefix(r, below))
		// the note hangs beneath the node as a tinted band; on the selected row in
		// note mode that same band becomes the editing surface (block cursor)
		noteCaret := -1
		if selected && m.mode == modeNote {
			noteCaret = m.caret
		}
		bands[i] = m.noteBandLines(r, maxLine, below, noteCaret)
		// workers render on a single line (status/usage/activity in the suffix); the
		// transcript lives in the agent UI (alt+e). Other runnable nodes (bash/query)
		// hang their ephemeral output beneath them.
		// the focused bash node shows its full scrollable viewer (the nodeView band
		// below) instead of this capped inline band, so don't render both
		focusedView := m.focused && i == m.cursor && nodeViewOf(it) != nil
		if !focusedView {
			bands[i] = append(bands[i], m.runBandLines(r, below, maxLine)...)
		}
	}

	// The Temporary Domain panel is always visible during normal editing — only
	// modal overlays (slash menu, pickers, prompts) take the full body. Layout:
	// main notes on top, the status bar acting as the divider, then the
	// always-visible Temporary Domain panel below it. Below ~3 body rows there is
	// no room for that stack, so fall back to the plain outline.
	rowBudget := m.rowBudget()
	// A focused inline view takes the whole body (like a picker) — the temp split
	// is suppressed so a tall view (agent transcript) isn't crammed into the panel.
	showTemp := (m.mode == modeOutline || m.mode == modeNote) && rowBudget >= 3 && !m.focused
	tempBudget, mainBudget := 0, rowBudget
	if showTemp {
		m.ensureTempTree() // always-visible panel must exist before we render it
		tempBudget = m.tempPanelBudget(rowBudget)
		mainBudget = rowBudget - tempBudget
		if mainBudget < 1 {
			mainBudget = 1
			tempBudget = rowBudget - 1
		}
	}
	focusedBudget := mainBudget
	if showTemp && m.tempActive {
		focusedBudget = tempBudget
	}

	// The focused node's inline expanded view renders as a band beneath it,
	// self-windowed to the focused budget so the node header stays pinned above
	// while a tall view (e.g. a long agent transcript) scrolls within its window.
	if m.focused && m.cursor >= 0 && m.cursor < len(rows) {
		cur := rows[m.cursor].it
		if v := nodeViewOf(cur); v != nil {
			r := rows[m.cursor]
			below := m.cursor+1 < len(rows) && rows[m.cursor+1].depth > r.depth
			winH := focusedBudget - len(groups[m.cursor]) - 1
			if winH < 1 {
				winH = 1
			}
			if total := v.Lines(m, cur, maxLine); m.focusScroll > total-winH {
				m.focusScroll = total - winH
			}
			if m.focusScroll < 0 {
				m.focusScroll = 0
			}
			bands[m.cursor] = append(bands[m.cursor],
				v.Bands(m, cur, continuationPrefix(r, below), maxLine, m.focusScroll, winH, true)...)
		}
	}

	const pickerMaxRows = 8 // most option rows any picker shows at once before scrolling

	maxRows := focusedBudget
	// Pickers (slash menu, /type, /style) are modal overlays drawn above the status
	// bar. Each reserves a small, FIXED-height scrolling window by shrinking the body
	// budget, so the picker never takes over the screen — the outline stays visible
	// and the list scrolls to keep the selection in view. typePickerRows includes the
	// /type search header.
	pickerItems, typePickerRows := 0, 0
	switch m.mode {
	case modeSlash:
		pickerItems = len(m.filteredSlash())
	case modeStyle:
		pickerItems = len(stylePickerItems)
	case modeType:
		pickerItems = len(m.filteredTypes())
		typePickerRows = 1 // the "type:" search header
	case modeArtifacts:
		pickerItems = len(loadedArtifacts)
		if pickerItems == 0 {
			pickerItems = 1 // the "none installed" hint row
		}
		typePickerRows = 1 // the header row
	case modeArtifactSrc:
		pickerItems = artifactSrcRows
		typePickerRows = 1 // the header row
	case modeTheme:
		pickerItems = len(themes)
	case modeComplete:
		pickerItems = len(m.complItems())
	}
	pickerRows := 0
	if pickerItems > 0 || typePickerRows > 0 {
		win := pickerItems
		if win > pickerMaxRows {
			win = pickerMaxRows
		}
		pickerRows = win + typePickerRows
		if pickerRows > rowBudget-1 { // always leave at least one body row
			pickerRows = rowBudget - 1
		}
		if pickerRows < 1 {
			pickerRows = 1
		}
		maxRows = rowBudget - pickerRows
		if maxRows < 1 {
			maxRows = 1
		}
	}
	cursorStart, cursorEnd := 0, 0
	var flat []string
	// the zoomed-in (view-root) node has no row of its own, so surface its note
	// as a band at the top of the view — the same band a row would hang below it.
	flat = append(flat, m.noteBandLines(row{it: m.viewRoot(), depth: 0}, maxLine, false, -1)...)
	for i := range groups {
		if i == m.cursor {
			cursorStart = len(flat)
			// scroll to keep the node itself in view, not the tail of its band —
			// except while editing the note, where the band is what needs to show
			cursorEnd = len(flat) + len(groups[i]) - 1
			// while editing the note, or while a focused inline view hangs beneath
			// the node, the band is what must stay on screen
			if m.mode == modeNote || m.focused {
				cursorEnd += len(bands[i])
			}
		}
		flat = append(flat, groups[i]...)
		flat = append(flat, bands[i]...)
	}
	start := 0
	if m.scrolling {
		// manual scroll (pgup/pgdown): pin the window at scrollTop, clamped to the
		// content, independent of where the cursor is.
		start = m.scrollTop
		if maxStart := len(flat) - maxRows; start > maxStart {
			start = maxStart
		}
		if start < 0 {
			start = 0
		}
		m.scrollTop = start
	} else {
		// follow the cursor: keep its row (and band, while editing) on screen
		if cursorEnd >= maxRows {
			start = cursorEnd - maxRows + 1
		}
		if cursorStart < start {
			start = cursorStart
		}
	}
	m.viewTop, m.viewRows = start, maxRows // cache for pgup/pgdown stepping
	end := start + maxRows
	if end > len(flat) {
		end = len(flat)
	}
	lines = append(lines, flat[start:end]...)

	// The delete confirm sits above the status line, not below it: the inline
	// renderer leaves a shrinking frame's old last line in place, so if the
	// confirm prompt were the final line, canceling it (one line shorter) would
	// strand the status bar blank until the next keypress repainted. Keeping the
	// bottomBar as every frame's last line means ESC-cancel restores it at once.
	if m.mode == modeConfirm {
		if cur := m.cursorItem(); cur != nil {
			// Build suffix-first: the count and keybind hints must never be clipped,
			// so reserve their width plus the fixed " delete " prefix and quotes,
			// then elide the middle of the name to fit whatever room is left.
			prefix := " " + cRed + "delete " + cReset
			suffix := cDim + fmt.Sprintf(" - %s - enter delete - esc keep", nodeNoun(subtreeSize(cur))) + cReset
			room := maxLine - visibleWidth(prefix) - visibleWidth(suffix) - 2 // 2 for the quotes
			name := elideMiddle(m.tree.displayName(cur), room)
			line := prefix + cYellow + fmt.Sprintf("%q", name) + cReset + suffix
			lines = append(lines, clip(line, maxLine))
		}
	}

	// The slash menu lists its commands above the status line, same as the
	// confirm prompt and for the same reason: the inline renderer skips
	// repainting an unchanged last line, so if the bottomBar were the final line
	// with the menu below it, dismissing the menu (Backspace on an empty query)
	// would shrink the frame without moving the bar's row — the renderer would
	// skip the bar and then erase-below it, blanking the status bar for a frame.
	// Listing the menu above the bar shifts the bar's row when the menu vanishes,
	// which forces the repaint. The menu is trimmed to the rows left under the
	// body so body + menu + bar still fits the window height.
	if m.mode == modeSlash {
		cmds := m.filteredSlash()
		win := pickerMaxRows
		s2 := scrollStart(m.slashSel, len(cmds), win)
		e2 := s2 + win
		if e2 > len(cmds) {
			e2 = len(cmds)
		}
		for i := s2; i < e2; i++ {
			c := cmds[i]
			mark := "  "
			if i == m.slashSel {
				mark = cAccent + "▸ " + cReset
			}
			line := " " + mark + cFG + fmt.Sprintf("%-14s", c.name) + cDim + " " + c.desc + cReset
			lines = append(lines, clip(line, maxLine))
		}
	}

	// the /type picker: a search header plus a scrolling, bounded option list
	if m.mode == modeType {
		filt := m.filteredTypes()
		if m.typeSel >= len(filt) {
			m.typeSel = len(filt) - 1
		}
		if m.typeSel < 0 {
			m.typeSel = 0
		}
		query := m.typeQuery
		if query == "" {
			query = cDim + "type to search" + cReset
		} else {
			query = cFG + query + cReset
		}
		lines = append(lines, clip(" "+cDim+"type: "+cReset+query, maxLine))

		win := pickerMaxRows
		s2 := scrollStart(m.typeSel, len(filt), win)
		e2 := s2 + win
		if e2 > len(filt) {
			e2 = len(filt)
		}
		for i := s2; i < e2; i++ {
			mark := "  "
			if i == m.typeSel {
				mark = cAccent + "▸ " + cReset
			}
			lines = append(lines, clip(" "+mark+cFG+typeLabel(filt[i])+cReset, maxLine))
		}
	}

	// the /artifacts management view and its source pager
	if m.mode == modeArtifacts {
		lines = append(lines, m.artifactListLines(maxLine)...)
	}
	if m.mode == modeArtifactSrc {
		lines = append(lines, m.artifactSrcLines(maxLine)...)
	}

	// the /style picker lists text style toggles and color swatches above the status line
	if m.mode == modeStyle {
		cur := m.cursorItem()
		win := pickerMaxRows
		s2 := scrollStart(m.styleSel, len(stylePickerItems), win)
		e2 := s2 + win
		if e2 > len(stylePickerItems) {
			e2 = len(stylePickerItems)
		}
		for i := s2; i < e2; i++ {
			it := stylePickerItems[i]
			mark := "  "
			if i == m.styleSel {
				mark = cAccent + "▸ " + cReset
			}
			if it.kind == "toggle" {
				active := ""
				if cur != nil && styleHas(cur.style, it.value) {
					active = cDim + " (on)" + cReset
				}
				line := " " + mark + cFG + stylePickerLabels[it.value] + active + cReset
				lines = append(lines, clip(line, maxLine))
			} else {
				swatch := styleColorCode[it.value] + "●" + cReset
				line := " " + mark + swatch + " " + styleColorCode[it.value] + stylePickerLabels[it.value] + cReset
				lines = append(lines, clip(line, maxLine))
			}
		}
	}

	// the /theme picker: one row per theme, each previewing its own palette as a
	// strip of swatches so the colors are visible before you commit.
	if m.mode == modeTheme {
		win := pickerMaxRows
		s2 := scrollStart(m.themeSel, len(themes), win)
		e2 := s2 + win
		if e2 > len(themes) {
			e2 = len(themes)
		}
		for i := s2; i < e2; i++ {
			t := themes[i]
			mark := "  "
			if i == m.themeSel {
				mark = cAccent + "▸ " + cReset
			}
			active := ""
			if t.name == activeThemeName {
				active = cDim + " (on)" + cReset
			}
			swatches := t.accent + "●" + t.red + "●" + t.yellow + "●" +
				t.green + "●" + t.cyan + "●" + t.purple + "●" + cReset
			line := " " + mark + cFG + fmt.Sprintf("%-9s", t.name) + active + "  " + swatches
			lines = append(lines, clip(line, maxLine))
		}
	}

	// the inline completer popup ("#" tags, ":" query commands): a scrolling list
	// of matches, each label plus an optional dim hint.
	if m.mode == modeComplete {
		items := m.complItems()
		if m.compl.sel >= len(items) {
			m.compl.sel = len(items) - 1
		}
		if m.compl.sel < 0 {
			m.compl.sel = 0
		}
		win := pickerMaxRows
		s2 := scrollStart(m.compl.sel, len(items), win)
		e2 := s2 + win
		if e2 > len(items) {
			e2 = len(items)
		}
		for i := s2; i < e2; i++ {
			mark := "  "
			if i == m.compl.sel {
				mark = cAccent + "▸ " + cReset
			}
			line := " " + mark + cFG + items[i].label + cReset
			if items[i].desc != "" {
				line += cDim + "  " + items[i].desc + cReset
			}
			lines = append(lines, clip(line, maxLine))
		}
	}

	// Assemble the body: main notes, then the status bar (which doubles as the
	// divider), then the Temporary Domain panel below it. `lines` here is the
	// focused region's body (no modal menus are open in showTemp modes). The frame
	// is padded to a constant height so the inline renderer never strands stale
	// lines despite the status bar sitting mid-frame.
	if showTemp {
		focused := lines
		if len(focused) > focusedBudget {
			focused = focused[:focusedBudget]
		}
		var mainLines, tempLines []string
		if m.tempActive {
			// guard a malformed stash: a nil tree or empty view-stack must degrade to a
			// blank region, never panic on the slice index.
			var mainRoot *item
			if n := len(m.mainStash.viewStack); n > 0 {
				mainRoot = m.mainStash.viewStack[n-1]
			}
			mainLines = m.readonlyRegionLines(m.mainStash.tree, mainRoot, m.mainStash.cursor, mainBudget, maxLine, false, false)
			tempLines = focused // live, focused temp
		} else {
			mainLines = focused // live, focused main
			// read-only temp panel: hide the empty compose line (only shows once focused)
			tempLines = m.readonlyRegionLines(m.tempTree, m.tempTree.root, 0, tempBudget, maxLine, true, true)
		}
		body := mainLines
		body = append(body, m.bottomBar(maxLine)) // the status bar is the divider
		body = append(body, tempLines...)
		total := rowBudget + 1 // main + status + temp, fixed for a stable frame
		for len(body) < total {
			body = append(body, "")
		}
		if len(body) > total {
			body = body[:total]
		}
		return body
	}

	lines = append(lines, m.bottomBar(maxLine))

	return lines
}

// WARNING (invariant): the bottom/status bar is the LAST rendered line of every
// frame — tooling and the inline renderer treat the final line as the status line.
// Always append it last (see viewOutline); never emit content below it.
func (m *Model) bottomBar(maxLine int) string {
	total := len(m.rows)
	pos := m.cursor + 1
	if len(m.rows) == 0 {
		pos = 0
	}
	state := ""
	if m.unsaved {
		state = " · unsaved"
	}
	if len(m.agentBusy) > 0 {
		state += " · " + cRed + "✦ agent thinking…" + cDim
	}
	if m.flash != "" {
		state += " · " + m.flash
	}
	// offer the date conversion while a non-canonical time phrase sits under the
	// cursor; an already-canonical date needs no conversion and is chipped as-is
	if m.mode == modeOutline {
		if cur := m.cursorItem(); cur != nil && cur.mirrorOf == "" {
			if d := detectDate(cur.name, m.caret, time.Now()); d != nil && d.phrase != d.canonical() {
				// the date picker hint reads white against the dim status bar, then
				// hands the color back so the rest of the bar stays muted
				state += fmt.Sprintf(" · "+cFG+"ctrl+t %q → %s"+cDim, d.phrase, d.canonical())
			}
		}
	}
	// breadcrumb: the forest path down to the current view root
	parts := append([]string(nil), m.ancestors...)
	for _, v := range m.viewStack {
		name := displayAnchors(m.tree.displayName(v), m.chips) // resolve chip anchors
		if name == "" {
			name = "untitled"
		}
		parts = append(parts, name)
	}
	// the bottom space is the Temp region — relabel its root in the breadcrumb
	if m.tempActive && len(parts) > 0 {
		parts[0] = "Temp"
	}
	title := strings.Join(parts, " › ")
	if title == "" {
		title = "untitled"
	}
	bar := fmt.Sprintf(" %s · %d/%d", title, pos, total)
	bar += state
	return clip(cDim+bar+cReset, maxLine)
}

// finderRowName resolves the name shown for a finder row. A mirror node
// carries an empty name in the database, so follow its mirror_of chain to
// the source node and show that name, suffixed to mark it a mirror. resolve
// looks up a node by uuid; a missing source falls back to a placeholder.
func finderRowName(n database.Node, resolve func(string) (database.Node, bool)) string {
	if n.MirrorOf == "" {
		return n.Name
	}
	seen := map[string]bool{n.UUID: true}
	cur := n.MirrorOf
	for {
		src, ok := resolve(cur)
		if !ok {
			return "(missing) - mirror"
		}
		if src.MirrorOf == "" || seen[cur] {
			return src.Name + " - mirror"
		}
		seen[cur] = true
		cur = src.MirrorOf
	}
}

func (m *Model) viewFinder(maxLine int) []string {
	var lines []string

	labels := map[finderAction]string{
		actMirrorHere: "/mirror",
		actMoveTo:     "/move",
		actGoto:       "/goto",
		actBringHere:  "/bring",
		actLinkInsert: "[[ link",
	}
	hints := map[finderAction]string{
		actMirrorHere: "Enter mirror at cursor",
		actMoveTo:     "Enter move this node there",
		actGoto:       "Enter open node",
		actBringHere:  "Enter bring this node here",
		actLinkInsert: "Enter link to node, or type a URL",
	}

	query := cDim + " " + labels[m.finderAct] + " " + cFG + withCaret(m.finderQuery, len([]rune(m.finderQuery))) + cReset
	lines = append(lines, clip(query, maxLine))

	// [[ doubles as a URL field: when the query is a URL, Enter links to the site
	if m.finderAct == actLinkInsert && browser.IsURL(m.finderQuery) {
		lines = append(lines, clip(cAccent+" ↵ "+cReset+cDim+"link to "+cFG+browser.Normalize(m.finderQuery)+cReset, maxLine))
	}

	maxResults := m.height - 4
	if maxResults < 3 {
		maxResults = 8
	}
	shown := m.finderHits
	overflow := 0
	if len(shown) > maxResults {
		overflow = len(shown) - maxResults
		shown = shown[:maxResults]
	}

	for i, h := range shown {
		mark := "   "
		if i == m.finderSel {
			mark = cAccent + " ▸ " + cReset
		}
		count, err := database.CountSubtree(m.db, h.UUID)
		if err != nil {
			count = 1
		}
		name := displayAnchors(finderRowName(h, func(uuid string) (database.Node, bool) {
			n, err := database.GetNode(m.db, uuid)
			return n, err == nil
		}), m.chips)
		// carry the node's own /color and /bold-/italic-/underline into the
		// picker so a styled node reads the same here as in the outline
		base := cFG
		if c := styleBaseColor(h.Style); c != "" {
			base = c
		}
		label := base + styleAttrs(h.Style) + fmt.Sprintf("%-28s", name) + cReset
		line := mark + label + cDim + fmt.Sprintf(" %d nodes", count) + cReset
		lines = append(lines, clip(line, maxLine))
	}
	if overflow > 0 {
		lines = append(lines, clip(cDim+fmt.Sprintf("   … %d more", overflow)+cReset, maxLine))
	}
	if len(shown) == 0 {
		lines = append(lines, cDim+"   no matches"+cReset)
	}

	lines = append(lines, "")
	lines = append(lines, clip(cDim+" "+hints[m.finderAct]+" - esc back to outline"+cReset, maxLine))
	lines = append(lines, m.bottomBar(maxLine))

	return lines
}

// Run opens the inline node editor on the given node.
func Run(ctx context.DnoteCtx, nodeUUID string) error {
	loadArtifacts(ctx.DB) // runtime node types must exist before the first render

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

	m := &Model{
		db:        ctx.DB,
		ctx:       ctx,
		tree:      t,
		chips:     chips,
		tempTree:  tempTree, // the Temp root subtree, persisted alongside Root
		viewStack: []*item{t.root},
		agents:    tag.LoadAgents(ctx.Paths.Config),
	}
	m.loadTheme() // apply the persisted color theme before the first render
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
	name := fm.tree.displayName(fm.tree.root)
	// muted gray throughout, only the node name in yellow
	fmt.Printf("%s→ saved %s%q%s - %s - %s written%s\n",
		cDim, cYellow, name, cDim,
		nodeNoun(total), nodeNoun(fm.saved.written), cReset)

	return nil
}

func nodeNoun(n int) string {
	if n == 1 {
		return "1 node"
	}
	return fmt.Sprintf("%d nodes", n)
}
