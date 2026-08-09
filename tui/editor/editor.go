// Package editor implements the full-screen (alt-screen) outline editor:
// black background, muted gray ○/●/□ glyphs and connectors plus 1/2/3
// heading digits, the selected row marked by its glyph turning red, a block
// cursor that inverts the cell beneath it, a minimal dim bottom bar, a
// type-to-filter slash menu above the bar, and a full-panel fuzzy finder for
// /mirror:to /mirror:from /move:to /move:here /goto /backlinks. On quit the
// alt screen is popped and the styled outline is printed once to the normal
// screen, ahead of the "→ saved" summary.
package editor

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/lflow/lflow/tui/daemon/client"
	"github.com/lflow/lflow/tui/daemon/wire"
	"github.com/lflow/lflow/tui/database"
	"github.com/lflow/lflow/tui/database/style"
	"github.com/lflow/lflow/tui/integrations"
	"github.com/lflow/lflow/tui/multiplexer"
	"github.com/lflow/lflow/tui/runtime"
	"github.com/mattn/go-runewidth"
	"github.com/pkg/errors"
)

type mode int

const (
	modeOutline mode = iota
	modeSlash
	modeFinder
	modeNote
	modeConfirm        // inline delete confirmation for nodes with children
	modeType           // the /type picker: choose one of the node types
	modeStyle          // the /style picker: toggle bold, italic, underline, strikethrough, color
	modeTheme          // the /theme picker: choose a color palette
	modeSettings       // the /settings picker: global preferences (theme, image preview, …)
	modeComplete       // the inline completer: "#" tags, ":" query commands
	modeFlash          // flash jump/act: every visible row's actions get a typed label (see flash.go)
	modeTagColor       // the alt+e tag color picker: assign a pill color to a tag
	modeInsert         // the /insert picker: choose a kind (cmd, date, icon, link, path, tag) to splice at the caret
	modeAgentPick      // /agent: start a coding session here, or attach one from a CLI's own store
	modeAgentColor     // ⌥c on a session chip: its color
	modeCite           // the Zotero picker: "@@" splices a citation chip, /type and /insert → Zotero mirror an entry
	modeCharacterPick  // the alt+e character picker on a Line node: pick or write a character
	modeCharacterColor // the character picker's recolor key: assign a pill color to a character
	modeSuggest        // alt+v review: settle the proposals pending on the cursor node (see suggest.go)
	modeShortcuts      // /shortcuts: full-page, scrollable shortcut reference
)

type finderAction int

const (
	actMirrorHere finderAction = iota // /mirror:from — a mirror of the picked node lands at the cursor
	actMirrorFrom                     // /mirror:to — a mirror of the cursor node lands under the picked node
	actMoveTo                         // /move:to — the cursor node moves under the picked node
	actGoto
	actBringHere  // /move:here — the picked node moves to the cursor
	actLinkInsert // [[ — insert an inline link chip at the caret (node or URL)
	actBacklinks  // /backlinks — nodes that mirror or [[-link to the cursor node
	actQueryScope // :in: — select the query's subtree boundary
)

type slashCommand struct {
	name string
	desc string
}

var slashCommands = []slashCommand{
	{"/backlinks", "Show nodes that mirror or link to this one"},
	{"/complete", "Toggle done (alt+enter)"},
	{"/duplicate", "Duplicate this node and its subtree next to it"},
	{"/goto", "Jump the editor to another node"},
	{"/goto:suggestion", "Jump to the next node with a pending suggestion"},
	{"/hide:complete", "Hide or show completed nodes"},
	{"/insert", "Insert a chip — or a Zotero entry — at caret"},
	{"/link", "Copy this node's lflow link (a selection copies all of them)"},
	{"/lock", "Lock or unlock this node as read-only"},
	{"/mirror:from", "Mirror another node here"},
	{"/mirror:to", "Mirror this node into another node"},
	{"/mirror:workflowy", "Mirror a Workflowy subtree here (paste a link, then alt+r)"},
	{"/move:here", "Move another node here"},
	{"/move:to", "Move this node under another node"},
	{"/note", "Edit this node's note"},
	{"/priority:down", "Incoming nodes land at the bottom"},
	{"/priority:up", "Incoming nodes land on top"},
	{"/reborn", "Reset this node's creation date to now"},
	{"/settings", "Editor preferences: theme, image preview, Zotero"},
	{"/shortcuts", "Open the full keyboard shortcut reference"},
	{"/star", "Star this node — ranks first in pickers and search hits"},
	{"/style", "Style this node — or just the text selected with shift+←/→"},
	{"/type", "Set this node's type"},
	{"/undo", "Undo the last action"},
	{"/redo", "Redo the last undo"},
}

// stylePickerItem groups the text-attribute toggles and the color choices
// into a single /style picker list.
type stylePickerItem struct {
	kind  string // "toggle" or "color"
	value string
}

// The picker is BUILT from the shared vocabulary rather than repeating it, so a
// color added in tui/style appears here — and in the tag, agent and line
// color pickers, which all read the same list — without a second edit that can
// be forgotten.
var stylePickerItems = buildStylePickerItems()

var stylePickerLabels = buildStylePickerLabels()

func buildStylePickerItems() []stylePickerItem {
	out := make([]stylePickerItem, 0, len(style.Attrs)+len(style.Colors))
	for _, a := range style.Attrs {
		out = append(out, stylePickerItem{"toggle", a})
	}
	for _, c := range style.Colors {
		out = append(out, stylePickerItem{"color", c})
	}
	return out
}

// styleLabelWords names the tokens whose label is not just their capitalized
// selves: an attribute the user knows by another word, and the "light" colors,
// which read as two words.
var styleLabelWords = map[string]string{
	"strike":      "Strikethrough",
	"lightgreen":  "Light green",
	"lightpurple": "Light purple",
}

func buildStylePickerLabels() map[string]string {
	out := map[string]string{}
	for _, sp := range buildStylePickerItems() {
		if label, ok := styleLabelWords[sp.value]; ok {
			out[sp.value] = label
			continue
		}
		out[sp.value] = strings.ToUpper(sp.value[:1]) + sp.value[1:]
	}
	return out
}

// Model is the bubbletea model for the editor.
type Model struct {
	db    *database.DB
	ctx   runtime.Ctx // for config and node context
	tree  *tree
	chips map[string]database.Chip // inline chip records, keyed by id (see chip.go)
	// backlinkCounts is every node's reference count (mirrors + [[ link chips
	// pointing at it) across the WHOLE outline, one bulk DB read per refreshRows
	// (see database.BacklinkCounts) — the "n backlinks" row suffix.
	backlinkCounts map[string]int

	viewStack []*item // zoom stack; last is the current view root
	cursor    int     // index into visibleRows
	caret     int     // rune index in the edited field
	rows      []row   // cached visible rows
	// unroll is the per-mirror cycle budget (uuid → levels): how many times a
	// mirror of an ancestor may re-enter its target, one level per expand
	// press. Ephemeral view state — never persisted or synced.
	unroll map[string]int

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
	// /move:to, /move:here, /goto, /backlinks, "[[" link); it owns the query,
	// selection, and results (see picker_finder.go).
	finder bodyFinder

	notePrev string // note backup for esc in note mode
	// noteRich stays true while a chip/style picker opened from /note is on top.
	// Those pickers normally edit cur.name; this flag redirects them to cur.note
	// and returns to note editing when the nested picker closes.
	noteRich bool

	// alt+e link-chip editor: an inline band under the row (linkEditView), keyed
	// through focusChip like the bash chip's output band
	linkEditID     string // chip id being edited
	linkEditName   string // working copy of the link's display name
	linkEditTarget string // working copy of the link's target (URL or lflow://node/<uuid>)
	linkEditField  int    // 0 = name field, 1 = target field
	linkEditCaret  int    // caret inside the active field — same movement keys as the outline

	// alt+i chip-field editor: the command inside a $ (bash) chip, or the
	// tag+attributes inside a <> (element) chip — one field, shared by both
	// kinds. An inline band under the row (chipEditView), keyed through
	// focusChip/focusChipEditing like every other alt+e/alt+i editor.
	chipEditID    string // chip id being edited
	chipEditValue string // working copy of the field
	chipEditCaret int    // caret inside the field

	// the focused chip (alt+e/alt+i): its inline view renders as a band beneath
	// the node — the same surface a focused bash node uses — keyed by this chip
	// id. A bash chip's alt+e band is its run output (bashChipView) UNLESS
	// focusChipEditing is set, in which case alt+i put it in chipEditView
	// instead; a link or session chip's band is always its editor
	// (linkEditView / agentEditView). activeView dispatches on chip kind (and,
	// for a bash chip, focusChipEditing).
	focusChip        string
	focusChipEditing bool

	// the agentic coding session pickers (/agent, /agents). agentStore is the
	// sessions discovered in the CLIs' own stores when the start/attach picker
	// opened.
	// Both are snapshots — a picker never re-walks a store while it is being
	// typed in.
	agentStore []agentStoreSession
	// The scan that fills agentStore while the picker is already open: the
	// channel its batches arrive on (identity tells a reopened picker's scan from
	// the previous one), which sessions have already been merged, and how far
	// along it is.
	agentScanCh chan tea.Msg
	agentSeen   map[string]bool
	agentFill   pickerFill
	// agentColorChip is the chip ⌥c is picking a color for.
	agentColorChip string

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
	// settingEdit is the live caret-editable buffer for a TEXT setting (see
	// settingDef.text) while its row is selected in the /settings picker.
	settingEdit textField

	compl complState

	// Shared RUN machinery — the generic spawn/stream/cancel infrastructure the
	// runnable node types use (bash, query, voice). Ephemeral, in-memory only,
	// keyed by node uuid (or cmd-chip id — same string keyspace). Run output is
	// NEVER in the DB or synced. One runState per id so every lifecycle event
	// (cancel/finish/delete) drops all of it atomically — see run/ensureRun.
	runs map[string]*runState

	// muxm is the PTY engine: every bash run's session lives here, keyed by
	// the same uuid keyspace as runs. Lazily created (see mux); sessions die
	// with the editor.
	muxm *multiplexer.Manager

	// Temporary Domain — a scratch outline region (a second root, 7-day retention)
	tempActive bool
	tempTree   *tree
	mainStash  tempStash
	// tempScroll is the read-only temp panel's window offset (mouse wheel over the
	// panel); tempTop/tempHeight bound the panel's screen region for that hit-test.
	// A focused temp list scrolls through the normal body window instead.
	tempScroll int
	tempTop    int
	tempHeight int

	// inline expanded view: when focused, the cursor node's nodeView captures keys
	// and renders bands beneath it (replaces the per-feature full-screen modes).
	focused     bool // is the cursor node's inline view capturing input
	focusScroll int  // first visible line of the focused view's self-window
	// focusFollow pins a run's expanded band to its tail as output arrives — a
	// terminal following its own output. Scrolling up drops it, end/G takes it
	// back (see runViewKey).
	focusFollow bool
	// auto-focus (Code node): the cursor resting on an autoFocus type enters its
	// view without alt+e. autoFocused tracks that node (nil = manual/no focus, so
	// reconcileAutoFocus leaves alt+e-focused json/bash untouched); autoFocusHold
	// is a node the user esc'd out of — reconcile won't re-grab it until the
	// cursor moves off. See reconcileAutoFocus.
	autoFocused   *item
	autoFocusHold *item

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

	// deps is the daemon-probed CLI availability map (NodeCLIDeps, see
	// deps.go): bin → available. nil = never probed → everything counts
	// available and failures surface at run time.
	deps map[string]bool

	// Workflowy mirror (see wf.go and tui/integrations): node uuid → workflowy id for
	// every pulled node, busy flags per pull root, and the API client (lazy;
	// tests inject one pointed at a mock server)
	wfMap    map[string]string
	wfBusy   map[string]bool
	wfClient *integrations.WorkflowyClient

	// Zotero library (see zotero.go and tui/integrations): the local library read
	// once, lazily, on first cite; zoteroErr remembers why a read failed so the
	// picker can say so instead of retrying a missing install on every keystroke.
	zoteroLib *integrations.Library
	zoteroErr string
	// zoteroFill tracks the read while it is in flight, so the picker can open
	// ahead of it and show a spinner; zoteroWaiting is the mirrors that asked for
	// their entry before there was a library to read it from.
	zoteroFill    pickerFill
	zoteroWaiting []string
	// The Zotero item mirror (see zoteroitem.go): node uuid → the Zotero object
	// that node stands for, busy flags per mirror root, and where each mirrored
	// attachment's file lives on THIS machine (session-only — a path is
	// machine-specific and the next pull recomputes it).
	zoteroBusy  map[string]bool
	zoteroPaths map[string]string
	// citeAct is what the open cite picker will do with the entry you pick.
	citeAct citeAction

	// Ancestor path strings used to sort :breadcrumb: query hits before their
	// nested result tree is reconciled; cleared whenever a query re-runs.
	qCrumbs map[string]string
	// queryLoad streams one persisted query's candidates in small batches. It is
	// ephemeral and cancelable: a newer alt+r supersedes the previous scan.
	queryLoad       *queryLoad
	queryGeneration int

	tagColorWord string // the tag word the alt+e color picker is assigning

	characterPickUUID  string // the Line node uuid the character picker is assigning to
	characterColorName string // the character name the nested recolor picker is assigning

	// edit suggestions (see suggest.go): the pending review queue grouped by the
	// node each proposal is about, refreshed whenever the feed reports a
	// suggestions write. suggestUUID/suggestSel are the review cursor while
	// modeSuggest is up. A proposal has changed nothing until it is approved.
	suggests map[string][]database.Suggestion
	// suggestOrder is the queue's targets in filing order, deduplicated — what
	// alt+v walks. The map above cannot answer "next", only "here".
	suggestOrder []string
	suggestUUID  string
	suggestSel   int

	// hideCompleted is the /hide:complete toggle: when true, completed nodes (and their
	// subtrees) drop out of the visible outline. Session-only — not persisted.
	hideCompleted bool

	// multi-select (see multisel.go): shift+up/down grows a row range from the
	// anchor; structural ops act on the selection roots
	selOn     bool
	selAnchor int

	// horizontal selection (see textsel.go): shift+left/right grows a run of the
	// cursor node's text from the anchor caret; /style paints exactly that run
	textSelOn     bool
	textSelAnchor int

	// /undo and /redo: snapshots of the tree taken before each action; redo
	// stacks the states undo popped, until a new edit clears it
	undoStack []undoState
	redoStack []undoState
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

	escPending bool
	escAt      time.Time // when the pending esc landed — split alt-chord window

	// the ⌥e session editor: an inline band under the row (agentEditView)
	agentEditID    string
	agentEditName  string
	agentEditCaret int
	agentEditField int // 0 = name, 1 = color
	agentEditColor int // index into agentColorOptions
	unsaved        bool
	quitting       bool
	animTicking    bool   // the magic-keyword animation tick is currently scheduled
	flash          string // one-shot status for the bottom bar, cleared on keypress
	flashErr       bool   // the flash is an error — rendered red (see errorFlash)
	err            error

	saved struct {
		written int
	}

	// onSave, when set, runs after every successful saveAll — the file-open
	// session serializes the tree back to its source file there. Its error
	// surfaces like a save error (the edits stay in the DB and retry on the
	// next save).
	onSave func() error
	// allowedTypes, when set, restricts the /type picker to the node types
	// the session's file format accepts. nil = unrestricted.
	allowedTypes map[string]bool
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
	// a save that tombstones nodes also settles the proposals about them, so the
	// queue this Model is holding is stale afterwards — note it before save
	// clears the pending deletions
	tombstoned := (main != nil && len(main.deleted) > 0) || (temp != nil && len(temp.deleted) > 0)

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
	if tombstoned {
		m.loadSuggests()
	}
	if m.onSave != nil {
		if err := m.onSave(); err != nil {
			return w, err
		}
	}
	return w, nil
}

// reopenAt saves, reloads the tree rooted at rootUUID, and focuses focusUUID. It
// is how alt+left walks up past the loaded root into the rest of the forest.
func (m *Model) reopenAt(rootUUID, focusUUID string) {
	if _, err := m.saveAll(); err != nil {
		m.errorFlash("save: " + err.Error())
		return
	}
	m.unsaved = false
	t, err := loadTree(m.db, rootUUID)
	if err != nil {
		m.errorFlash(err.Error())
		return
	}
	m.tree = t
	m.viewStack = []*item{t.root}
	m.undoStack = nil // a reload is a fresh editing context
	m.redoStack = nil
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
	chips   map[string]database.Chip
	// sidecars is each chip's node_output row, raw JSON keyed by chip id. Chips
	// and their sidecars are deleted together, so they have to be restored
	// together: the record alone brings back a session chip with no session.
	sidecars map[string]string
	external []*item  // grafted subtree roots, cloned like root
	view     []string // viewStack uuids
	cursor   int
	caret    int
}

// pushUndo snapshots the tree before a mutating action. The mark coalesces a run
// of same-kind edits — typing a word is one undo step, not one per character —
// by skipping the snapshot when the mark matches the previous one. Any new
// snapshot invalidates the redo stack: the edit branches off the undone state.
func (m *Model) pushUndo(mark string) {
	if mark != "" && mark == m.undoMark {
		return
	}
	m.undoMark = mark
	m.undoStack = append(m.undoStack, m.captureUndoState())
	m.redoStack = nil
	const maxUndo = 100
	if len(m.undoStack) > maxUndo {
		m.undoStack = m.undoStack[len(m.undoStack)-maxUndo:]
	}
}

// captureUndoState snapshots the live tree and cursor exactly as it is right
// now — the state a later undo (or redo) restores.
func (m *Model) captureUndoState() undoState {
	st := undoState{
		root:     cloneItem(m.tree.root, nil),
		deleted:  append([]string(nil), m.tree.deleted...),
		chips:    make(map[string]database.Chip, len(m.chips)),
		sidecars: m.chipSidecars(),
		external: make([]*item, len(m.tree.external)),
		view:     make([]string, len(m.viewStack)),
		cursor:   m.cursor,
		caret:    m.caret,
	}
	for id, c := range m.chips {
		st.chips[id] = c
	}
	for i, ex := range m.tree.external {
		st.external[i] = cloneItem(ex, nil)
	}
	for i, v := range m.viewStack {
		st.view[i] = v.uuid
	}
	return st
}

// snapshotForKey pushes an undo snapshot before a mutating outline key. A run of
// typed characters on one node coalesces into a single undo step; each
// structural action is its own step.
func (m *Model) snapshotForKey(key string, k tea.KeyMsg) {
	cur := m.cursorItem()
	// undo/redo chords are commands, never edits — they must not snapshot (a
	// snapshot would also clear the redo stack the redo chord is about to read)
	if key == "ctrl+z" || key == "alt+z" || key == "ctrl+y" || key == "ctrl+shift+z" {
		return
	}
	// a paste is its own undo step, never merged with the typing around it:
	// type "ab", paste "cd" — one undo removes just the paste, the next the
	// typed text. (Pastes that become chips push their own snapshot inside.)
	if k.Paste {
		m.pushUndo("")
		return
	}
	if (m.selOn || m.textSelOn) && (key == "y" || key == "x") {
		return // clipboard.go snapshots the cut; copy does not mutate
	}
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

// undo restores the most recent snapshot, first parking the current state on
// the redo stack so the same key shape can bring it back.
func (m *Model) undo() {
	if len(m.undoStack) == 0 {
		m.flash = "nothing to undo"
		return
	}
	st := m.undoStack[len(m.undoStack)-1]
	m.undoStack = m.undoStack[:len(m.undoStack)-1]
	m.redoStack = append(m.redoStack, m.captureUndoState())
	m.undoMark = "" // the next edit starts a fresh snapshot
	m.restoreUndoState(st)
}

// redo replays the state undo parked, in the reverse direction: the current
// state goes back on the undo stack and the parked snapshot comes back.
func (m *Model) redo() {
	if len(m.redoStack) == 0 {
		m.flash = "nothing to redo"
		return
	}
	st := m.redoStack[len(m.redoStack)-1]
	m.redoStack = m.redoStack[:len(m.redoStack)-1]
	m.undoStack = append(m.undoStack, m.captureUndoState())
	m.undoMark = "" // the next edit starts a fresh snapshot
	m.restoreUndoState(st)
}

// restoreUndoState swaps the snapshot's tree, chips, sidecars and view in —
// shared by undo and redo, which only differ in which stack feeds it.
func (m *Model) restoreUndoState(st undoState) {
	m.tree.root = cloneItem(st.root, nil) // clone so the stacked entry stays pristine
	m.tree.external = make([]*item, len(st.external))
	for i, ex := range st.external {
		m.tree.external[i] = cloneItem(ex, nil)
	}
	m.tree.rebuildByUUID()

	// Chip records are part of the editable text snapshot: restoring an anchor
	// without its record renders as @?. Reconcile both the in-memory cache and the
	// chips table before the restored tree can render or save.
	currentChips := m.chips
	chipDB := m.chipDB()
	touched := make(map[string]bool, len(currentChips))
	m.chips = make(map[string]database.Chip, len(st.chips))
	for id, c := range st.chips {
		m.chips[id] = c
		if chipDB != nil {
			_ = database.UpsertChip(chipDB, c)
		}
	}
	for id := range currentChips {
		touched[id] = true
		if _, keep := st.chips[id]; !keep && chipDB != nil {
			_ = database.DeleteChip(chipDB, id)
		}
	}
	// and the local rows hanging off those chips — a restored session chip has to
	// come back knowing which session it is, under the name and color it wore
	m.restoreChipSidecars(st.sidecars, touched)

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
	m.rows = m.tree.visibleRows(m.viewRoot(), m.hideCompleted, m.unroll)
	m.clampCursor()
	// publish the look of every visible coding session (color + status tail) for
	// the Model-less render path — see agentLooks
	m.refreshAgentLooks()
	m.refreshBacklinkCounts()
}

// refreshBacklinkCounts re-reads the whole outline's reference counts for the
// "n backlinks" row suffix. One bulk query per outline change (refreshRows runs
// on edits, not per frame); a failed read leaves the counts as they were, so a
// transient DB error never takes the suffix away. Mirrors resolve to their
// source's count — the row shows the node it actually represents.
func (m *Model) refreshBacklinkCounts() {
	if m.db == nil {
		return
	}
	counts, err := database.BacklinkCounts(m.db)
	if err != nil {
		return
	}
	m.backlinkCounts = counts
}

// backlinkCount is the number of nodes elsewhere in the outline referencing
// it.uuid — a mirror or [[ link chip. Mirrors answer for their source.
func (m *Model) backlinkCount(it *item) int {
	uuid := it.uuid
	if it.mirrorOf != "" {
		uuid = m.tree.sourceUUID(it)
	}
	return m.backlinkCounts[uuid]
}

// expandStep opens the cursor node one visible level: uncollapse it, and when
// the row is a mirror re-entering an ancestor, raise its unroll budget so
// exactly this level shows — cycles nest one step per press, never forever.
// The cursor index is kept: new rows appear below it, and findRow cannot tell
// repeated occurrences of the same mirror apart.
func (m *Model) expandStep() {
	cur := m.cursorItem()
	if cur == nil || len(m.tree.childItems(cur)) == 0 || m.cursor >= len(m.rows) {
		return
	}
	r := m.rows[m.cursor]
	changed := false
	if cur.collapsed {
		cur.collapsed = false
		m.persistCollapsed(cur)
		changed = true
	}
	if r.cycleDepth > 0 && m.unroll[cur.uuid] < r.cycleDepth {
		if m.unroll == nil {
			m.unroll = map[string]int{}
		}
		m.unroll[cur.uuid] = r.cycleDepth
		changed = true
	}
	if changed {
		m.refreshRows()
	}
}

// collapseStep folds the cursor node one visible level. On an expanded cycle
// repetition it lowers the unroll budget back to just above this level; the
// first level folds the collapsed flag itself and rewinds the whole unroll, so
// the next expand starts at one level again.
func (m *Model) collapseStep() {
	cur := m.cursorItem()
	if cur == nil || cur.collapsed || len(m.tree.childItems(cur)) == 0 || m.cursor >= len(m.rows) {
		return
	}
	r := m.rows[m.cursor]
	if r.cycleDepth > 1 && !r.cycled {
		m.unroll[cur.uuid] = r.cycleDepth - 1
		m.refreshRows() // rows shrink below the cursor row; its index holds
		return
	}
	delete(m.unroll, cur.uuid)
	ctx := m.cursorCtx()
	cur.collapsed = true
	m.persistCollapsed(cur)
	m.refreshRows()
	m.cursor = m.findRow(cur, ctx)
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

// reconcileAutoFocus keeps an autoFocus block node (the Code node) in edit focus
// while the cursor rests on it: entering seeds the block buffer and thin caret,
// and moving off flushes it back to the node. It runs after every key (and once
// at open) so navigating onto a code block drops you straight into typing — no
// alt+e. Manual alt+e focus (json/bash: autoFocused == nil) is left untouched,
// and a node the user esc'd out of (autoFocusHold) is not re-grabbed until the
// cursor leaves it.
func (m *Model) reconcileAutoFocus() {
	if m.mode != modeOutline {
		return
	}
	cur := m.cursorItem()
	if m.autoFocusHold != nil && m.autoFocusHold != cur {
		m.autoFocusHold = nil // moved away — the esc hold is spent
	}
	// the cursor left an auto-focused block: flush and drop focus
	if m.autoFocused != nil && m.autoFocused != cur {
		if v := nodeViewOf(m.autoFocused); v != nil {
			v.leave(m, m.autoFocused)
		}
		m.focused = false
		m.autoFocused = nil
	}
	// the cursor rests on an eligible auto-focus node: enter its view
	if cur != nil && !m.focused && cur != m.autoFocusHold && !m.selOn &&
		cur.mirrorOf == "" && !cur.readonly && typeOf(cur.typ).autoFocus {
		if v := nodeViewOf(cur); v != nil && v.enter(m, cur) {
			m.focused = true
			m.autoFocused = cur
			m.focusScroll = 0
		}
	}
}

// toggleBlockFace flips a blockFaces node (nlpcompute) between its code block and
// its prose row (alt+e). Showing code → flush any focused edit and drop to the
// prose face (inline-editable text). Showing prose → switch to the code face and
// clear the esc-hold so reconcileAutoFocus drops the cursor straight into it.
func (m *Model) toggleBlockFace(cur *item) {
	bc := typeOf(cur.typ).blockCode
	if bc == nil {
		return
	}
	d := m.nodeStore(cur.uuid)
	face, _ := d["blockFace"].(string)
	_, _, codeShown := bc(m, cur, false)
	if face != "nlp" && codeShown {
		if m.focused && m.autoFocused == cur {
			if v := nodeViewOf(cur); v != nil {
				v.leave(m, cur)
			}
			m.focused = false
			m.autoFocused = nil
		}
		d["blockFace"] = "nlp"
		return
	}
	d["blockFace"] = "code"
	m.autoFocusHold = nil // let reconcileAutoFocus enter the code face now
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
	if it == nil || m.db == nil {
		return // no database behind this tree (the ephemeral temp tree, tests)
	}
	if err := database.SetCollapsed(m.db, it.uuid, it.collapsed); err != nil {
		m.err = err
	}
}

// Init implements tea.Model.
func (m *Model) Init() tea.Cmd {
	cmd := m.startAnim(nil)
	// mouse capture follows the preference: only the wheel mode grabs the
	// mouse (native selection then needs shift); select mode leaves the mouse
	// to the terminal so drag-select and copy-on-select just work.
	if m.setting("mouse") == "wheel" {
		cmd = tea.Batch(cmd, tea.EnableMouseCellMotion)
	}
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
		// The alt screen repaints in full every frame, so a resize needs no
		// special-case clear — just record the new size.
		m.width = msg.Width
		m.height = msg.Height
		return m, nil
	case tea.MouseMsg:
		m.handleMouse(msg)
		return m, nil
	case tea.KeyMsg:
		_, cmd := m.handleKey(msg)
		// track the cursor onto/off an auto-focus block (Code): enter it for
		// direct typing, flush it on leave — so the next render shows the caret
		m.reconcileAutoFocus()
		// live sync: arm the debounced flush for fresh edits, and fold in any
		// external events that queued while a modal surface was open
		if sc := m.scheduleSync(); sc != nil {
			cmd = tea.Batch(cmd, sc)
		}
		m.drainLive()
		// a keyword may have just been typed (or scrolled into view) — kick the
		// animation tick if it isn't already running.
		return m, m.startAnim(cmd)
	case ncDoneMsg:
		// an nlpcompute generation landing — keep the animation tick alive so a
		// node's shine indicator keeps sliding while the result is folded in
		return m, m.startAnim(m.handleNCDone(msg))
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
	case queryLoadMsg:
		return m, m.startAnim(m.handleQueryLoad(msg))
	case queryReadyMsg:
		return m, m.handleQueryReady(msg)
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
		// a bash chip's inline → tail tracks the newest line as it arrives, so a
		// long-running command streams in place instead of staying blank until it
		// exits (setBashChipPreview is a no-op for a node uuid). startAnim keeps the
		// shimmer sliding for the rest of the run.
		m.setBashChipPreview(msg.uuid)
		return m, m.startAnim(waitBashCmd(r.ch))
	case bashBytesMsg:
		r := m.run(msg.uuid)
		// r.scr is set with r.cancel (startShellRun) and only cleared once the
		// run is over, so a live run always has its screen
		if r == nil || r.cancel == nil {
			return m, nil // canceled — stop streaming
		}
		// raw PTY bytes go to the run's terminal screen, which interprets them the
		// way a terminal would (overwrites, clears, cursor moves) — see term.go
		r.scr.Write(msg.data)
		m.setBashChipPreview(msg.uuid)
		return m, m.startAnim(waitBashCmd(r.ch))
	case bashDoneMsg:
		m.finishRun(msg.uuid) // cache the finished band so it survives a restart
		return m, nil
	case wfDoneMsg:
		m.handleWFDone(msg)
		return m, nil
	case zoteroPullMsg:
		m.handleZoteroPull(msg)
		return m, nil
	case zoteroLoadedMsg:
		return m, m.handleZoteroLoaded(msg)
	case agentScanMsg:
		return m, m.handleAgentScan(msg)
	case voiceDoneMsg:
		m.setVoiceWave(msg.uuid, msg.env, msg.dur)
		return m, nil
	case webDoneMsg:
		m.handleWebDone(msg)
		return m, nil
	case imagePastedMsg:
		m.setImagePasting(msg.uuid, false)
		switch {
		case msg.err != nil:
			m.errorFlash("image: " + msg.err.Error())
		case m.db == nil:
			m.errorFlash("image: no database")
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
	case imageOpenedMsg:
		if msg.err != nil {
			m.errorFlash("image: " + msg.err.Error())
		} else {
			m.flash = "opened in " + msg.via
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
//     mirror at once. TEXT edits go the same way, through editTarget: typing on a
//     mirror row types into the source, because the text on that row IS the
//     source's text.
//   - NAVIGATION stays local. After an edit the cursor is restored by (item, ctx)
//     via findRow, so it stays in the mirror view the user is working in rather
//     than jumping to the original copy.
//
// Per-key behaviour, all expressed through the fields below:
//
//	enter      · editable is false on a mirror reference, so Enter does not split
//	             its text; it opens an empty sibling. Splitting would tear one
//	             node's words across two PLACES — the prefix staying at the source
//	             and the suffix landing beside the mirror — which is the one edit
//	             "same node everywhere" cannot express. Locked nodes (readonly)
//	             also skip the split — newline only. Otherwise it splits at the
//	             caret. The cursor is restored into ctx.
//	tab        · indenting under a mirror attaches the child to the mirror's
//	             source; indentInto names that mirror so the cursor follows into
//	             its view instead of snapping back to the original.
//	shift+tab  · outdent is bounded by localRoot (the mirror's source when the
//	             cursor is inside a mirror). A through-child deeper than the
//	             source outdents within the source and the cursor stays in ctx;
//	             a direct child of the source escapes — lands as the next sibling
//	             of the mirror — so both the original and the mirror update.
//	reorder    · alt+shift+up/down move the real node among its siblings; the
//	             cursor is restored into ctx.
//	collapse   · fold/unfold counts the resolved children and restores into ctx.
//	ctrl+t     · the date pill converts through editTarget, so it works on a
//	             mirror and lands on the source.
//	zoom       · alt/ctrl+right into a mirror enters its source node so the
//	             original's children render rather than the empty reference.
//	delete     · removing a node drops it from the original AND every mirror at
//	             once, so the row set can shrink by more than one — clampCursor
//	             reclamps after any manual cursor nudge.
type mirrorContext struct {
	ctx *item // the mirror the cursor sits under, nil at the original location
	// editable governs the one edit that is about PLACEMENT rather than text:
	// whether Enter splits this row. False on a mirror reference. Text edits ask
	// editTarget instead, and a mirror answers yes to those.
	editable   bool
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

// editTarget is the node a CONTENT edit acts on from the row under the cursor —
// typing, backspace, word delete, a chip, a style — or nil when this row's text
// is not yours to change.
//
// A mirror is the same node seen twice, so editing one through the other is the
// only behaviour that matches what the row says: the text you are looking at IS
// the source's text, and a keystroke lands on the source while the cursor stays
// where you are working. Everything else about the mirror — where it sits, what
// it indents under, what Enter creates next to it — belongs to the placement,
// not the source; only the words travel.
//
// Nil is for rows that are FIXED rather than merely elsewhere: a materialized
// query result (the query owns it and rewrites it), a Zotero mirror (Zotero owns
// it), a locked node, and a type with no inline text at all (json, code — those
// have their own editor behind ⌥e). Those rows still take a cursor and still
// draw a caret; the keystroke simply does nothing.
func (m *Model) editTarget() *item {
	return m.editTargetOf(m.cursorItem())
}

// caretText is the text the caret moves through on a row: what that row DRAWS,
// which for a mirror is the source's name and not the mirror's own empty one.
// Measuring against the empty string is what used to pin the caret at column 0
// on every mirrored row — the caret could not move along words it could see.
func (m *Model) caretText(it *item) string {
	if it == nil {
		return ""
	}
	return m.tree.displayName(it)
}

func (m *Model) editTargetOf(cur *item) *item {
	if cur == nil || cur.queryGenerated() || zoteroMirrored(cur) {
		return nil
	}
	src := m.tree.resolve(cur)
	if src == nil || src.readonly || !typeOf(src.typ).inlineEditable {
		return nil
	}
	return src
}

// setNodeType is the ONE way a node's type changes: /type, the backspace demote
// of an empty special node, a plugin's own retype. It exists so the cleanup that
// has to happen on every one of them happens on every one of them — today that
// is the run band, which belonged to the type being left behind.
func (m *Model) setNodeType(it *item, typ string) {
	if it == nil || it.typ == typ {
		return
	}
	// a command still running under the old type is stopped: its output would
	// otherwise land in a band the node no longer has
	if r := m.run(it.uuid); r != nil && r.cancel != nil {
		r.cancel()
		m.finishRun(it.uuid)
	}
	m.dropRunOut(it.uuid)
	it.typ = typ
	m.unsaved = true
}

// linkChipTrigger reports whether "[[" should open the link picker on this type.
// It has no cancel-to-literal path, so it stays off where "[" is real syntax
// (bash test brackets, code, query, quote, json).
func linkChipTrigger(typ string) bool {
	if typeOf(typ).disableChips {
		return false
	}
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
	if typeOf(typ).disableChips {
		return false
	}
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
// node below it. LEADING SPACES ARE DEPTH — a line indented past the one above
// it lands as its child — so an indented list, or a subtree copied out with
// alt+y (see clipboard.go), comes back as a tree instead of rows whose names
// start with spaces. Lines are already sanitized by pasteLines; a line that
// sanitized to empty (only C0/DEL bytes) creates no node so the paste never
// leaves a ghost empty-named node between two real lines.
func (m *Model) pasteFanOut(cur *item, lines []string) (tea.Model, tea.Cmd) {
	runes := []rune(cur.name)
	m.boundCaret(len(runes))
	cur.name = string(runes[:m.caret]) + strings.TrimLeft(lines[0], " ") + string(runes[m.caret:])

	// one entry per open indent level, holding the last node landed at it; the
	// base is the pasted-into row itself, so an unindented paste is all siblings
	type pasteLevel struct {
		indent int
		it     *item
	}
	stack := []pasteLevel{{it: cur}}
	last := cur
	for _, l := range lines[1:] {
		text := strings.TrimLeft(l, " ")
		if text == "" {
			continue
		}
		indent := len(l) - len(text)
		for len(stack) > 1 && indent < stack[len(stack)-1].indent {
			stack = stack[:len(stack)-1]
		}
		top := stack[len(stack)-1]
		var it *item
		var err error
		if indent > top.indent {
			// first child at a new level; later lines at this level are its siblings
			if it, err = m.tree.insertFirstChild(top.it); err == nil {
				stack = append(stack, pasteLevel{indent: indent, it: it})
			}
		} else {
			if it, err = m.tree.insertSiblingAfter(top.it); err == nil {
				stack[len(stack)-1].it = it
			}
		}
		if err != nil {
			// a locked parent takes no children: keep what landed, say why
			m.errorFlash(err.Error())
			break
		}
		it.name = text
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
		m.errorFlash("a node cannot mirror itself")
		return
	}
	target, err := database.GetNode(m.db, uuid)
	if err != nil {
		m.errorFlash("link points at no node")
		return
	}

	target = m.resolveSourceNode(target)
	it.name = ""
	it.mirrorOf = target.UUID
	m.caret = 0
	if !m.tree.graftExternal(target.UUID) {
		m.tree.externalNames[target.UUID] = target.Name // ungraftable: name stub
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
	if it == nil || it.structureLocked {
		m.errorFlash("node structure is locked")
		return
	}
	// cancel plugin work still running inside this subtree
	m.removeNodeStateUnder(it)
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
	out     []outLine           // line producers' output (query, math's LaTeX export)
	scr     *multiplexer.Screen // a SHELL run's terminal screen; nil = line band
	cancel  func()              // cancels the running command; nil once finished
	ch      chan tea.Msg        // stream channel for a running command
	loaded  bool                // band hydrated from node_output (see runout.go)
	dropped int                 // lines dropped off the band's head (see maxRunLines)
	pwd     string              // cwd captured when the band was run
	cmd     string              // the command that was run (see runTails — a changed cmd invalidates its tail)
	started time.Time           // launch instant; drives the elapsed clock while running
}

// lines is the band's content, whatever produced it: a shell run renders its
// terminal screen (scrollback then the live grid — what a terminal would show),
// a line producer returns what it appended. Every read path — the inline band,
// the expanded view, the chip's → tail, persistence — goes through here, so the
// two producers stay interchangeable.
func (r *runState) lines() []outLine {
	if r == nil {
		return nil
	}
	if r.scr != nil {
		rows := r.scr.Lines()
		out := make([]outLine, len(rows))
		for i, t := range rows {
			out[i] = outLine{text: t}
		}
		return out
	}
	return r.out
}

// dropCount is how many lines fell off the band's head — the screen counts its
// own scrollback drops.
func (r *runState) dropCount() int {
	if r == nil {
		return 0
	}
	if r.scr != nil {
		return r.scr.Dropped()
	}
	return r.dropped
}

// elapsed reports how long a live run has been going, or 0 when it never started
// (a rehydrated band, a synthesised run in a test).
func (r *runState) elapsed() time.Duration {
	if r == nil || r.started.IsZero() {
		return 0
	}
	return time.Since(r.started)
}

// run returns the existing run state for an id, or nil if none — nil-safe for
// read/comma-ok sites (an absent id reads as "no output, not running").
func (m *Model) run(id string) *runState { return m.runs[id] }

// mux returns the multiplexer, lazily created (mirrors ensureRun) so every
// direct-constructed test Model gets one on first touch.
func (m *Model) mux() *multiplexer.Manager {
	if m.muxm == nil {
		m.muxm = multiplexer.NewManager(maxRunLines)
	}
	return m.muxm
}

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
		runes := []rune(m.caretText(cur))
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

// deleteWordBoundary returns the index a word delete removes back to: a caret
// right after whitespace removes only that whitespace (the word survives for
// the next press), otherwise the whole previous word goes.
func deleteWordBoundary(runes []rune, caret int) int {
	i := caret
	if i > 0 && runes[i-1] == ' ' {
		for i > 0 && runes[i-1] == ' ' {
			i--
		}
		return i
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
		if typeOf(t).tempOnly && !m.tempActive {
			continue // temp-only types are not offered outside the Temporary Domain
		}
		if m.allowedTypes != nil && !m.allowedTypes[t] {
			continue // a file session offers only the types its format accepts
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
// left/right (or space) cycle an option's value with a live apply + DB persist,
// esc/enter close. A TEXT setting (settingDef.text — e.g. searxng.url) edits in
// place instead: typing and the caret keys write its buffer, enter saves it and
// closes, esc closes without saving.
func (m *Model) handleSettingsKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	d, text := m.selectedSetting()
	switch k.String() {
	case "esc":
		m.mode = modeOutline
		return m, nil
	case "enter":
		if text {
			m.setSetting(d.key, strings.TrimSpace(m.settingEdit.value))
		}
		m.mode = modeOutline
		return m, nil
	case "up":
		if m.settingsSel > 0 {
			m.settingsSel--
			m.initSettingEdit()
		}
	case "down":
		if m.settingsSel < len(settingDefs)-1 {
			m.settingsSel++
			m.initSettingEdit()
		}
	case "alt+c":
		// a FIXED row's value is detected, not chosen — the one thing it is for
		// is scripts and paths, so copy it to the clipboard
		if d.fixed && m.settingsSel >= 0 && m.settingsSel < len(settingDefs) {
			val := m.setting(d.key)
			if val == "" {
				m.errorFlash("zotero · no library found to copy")
				return m, nil
			}
			via, _ := clipWrite(val)
			if via == "" {
				m.errorFlash("no clipboard (need wl-copy/xclip/pbcopy, or a terminal that takes OSC 52)")
			} else {
				m.flash = "copied · " + val
			}
		}
	case "left", "right", " ", "space", "h", "l":
		if text {
			m.settingEdit.handleKey(k)
			return m, nil
		}
		if m.settingsSel >= 0 && m.settingsSel < len(settingDefs) && !d.fixed {
			dir := 1
			if s := k.String(); s == "left" || s == "h" {
				dir = -1
			}
			m.setSetting(d.key, cycleSetting(d, m.setting(d.key), dir))
			// mouse capture toggles live with the preference
			if d.key == "mouse" {
				if m.setting("mouse") == "wheel" {
					return m, tea.EnableMouseCellMotion
				}
				return m, tea.DisableMouse
			}
		}
	default:
		if text {
			m.settingEdit.handleKey(k)
		}
	}
	return m, nil
}

// selectedSetting returns the currently selected setting and whether it is a
// free-text one. A nil-ish selection falls back to the first entry.
func (m *Model) selectedSetting() (settingDef, bool) {
	if m.settingsSel < 0 || m.settingsSel >= len(settingDefs) {
		m.settingsSel = 0
	}
	d := settingDefs[m.settingsSel]
	return d, d.text
}

// initSettingEdit loads the selected TEXT setting's current value into the
// editing buffer with the caret at the end, so typing appends. Called whenever
// the selection moves; option settings ignore it.
func (m *Model) initSettingEdit() {
	d, text := m.selectedSetting()
	if !text {
		return
	}
	m.settingEdit = textField{value: m.setting(d.key), caret: len([]rune(m.setting(d.key)))}
}

func (m *Model) runSlash(name string) (tea.Model, tea.Cmd) {
	m.mode = modeOutline
	cur := m.cursorItem()
	if cur == nil {
		return m, nil
	}

	switch name {
	case "/type":
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
		m.initSettingEdit()
	case "/lock":
		// Toggle only LOCK_READ_WRITE. The independent structural lock bit remains
		// intact, so generated query rows can be content+structure locked or only
		// structure locked without /lock collapsing the union.
		if zoteroMirrored(cur) {
			// unlocking would promise an edit that the next alt+r silently discards
			m.flash = "zotero · a mirrored entry is read-only · alt+r refreshes it"
			return m, nil
		}
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
	case "/insert":
		// open the kind picker (chips + icon); searchable so "ic" reaches icon
		m.mode = modeInsert
		m.list.open(m, insertSource{}, true)
	case "/goto:suggestion":
		// walk to the next node carrying a proposal and open review on it
		m.gotoSuggestion()
	case "/shortcuts":
		m.mode = modeShortcuts
		m.focusScroll = 0
	case "/priority:up", "/priority:down":
		// where incoming and moved-in nodes land among this node's children:
		// top (up) or bottom (down). A mirror sets its original.
		// Like /star: toggled in place, immediate write, no edited_on churn.
		cur = m.tree.resolve(cur)
		if name == "/priority:up" {
			cur.priority = database.PriorityUp
			m.flash = "priority up · incoming nodes land on top"
		} else {
			cur.priority = database.PriorityDown
			m.flash = "priority down · incoming nodes land at the bottom"
		}
		if m.db != nil {
			_ = database.SetPriority(m.db, cur.uuid, cur.priority)
		}
	case "/reborn":
		// reset the node's creation date to now (a mirror resets its original).
		// Like /star: writes in place, no edited_on churn.
		cur = m.tree.resolve(cur)
		cur.addedOn = time.Now().UnixNano()
		if m.db != nil {
			_ = database.SetAddedOn(m.db, cur.uuid, cur.addedOn)
		}
		m.flash = "reborn · created_at reset to now"
	case "/duplicate":
		// Deep-copy every selected root (subtrees ride along), or just the cursor
		// node when no row selection is live. The whole operation is one undo step.
		targets := m.selectionRoots()
		if len(targets) == 0 {
			targets = []*item{cur}
		}
		for _, target := range targets {
			if target.structureLocked {
				m.errorFlash(errStructureLocked.Error())
				return m, nil
			}
			if target.parent == nil {
				m.errorFlash("cannot duplicate the root node")
				return m, nil
			}
		}
		m.pushUndo("")
		ctx := m.cursorCtx()
		type duplicateGroup struct {
			parent *item
			after  *item
			clones []*item
		}
		var groups []*duplicateGroup
		byParent := map[*item]*duplicateGroup{}
		var last *item
		for _, target := range targets {
			clone, err := m.tree.cloneSubtree(target)
			if err != nil {
				m.errorFlash(err.Error())
				return m, nil
			}
			clone.parent = target.parent
			group := byParent[target.parent]
			if group == nil {
				group = &duplicateGroup{parent: target.parent}
				byParent[target.parent] = group
				groups = append(groups, group)
			}
			group.after = target
			group.clones = append(group.clones, clone)
			last = clone
		}
		// A selected block duplicates as another block after the selection — not
		// as A,A′,B,B′. If selected roots span parents, each sibling group lands
		// after the last selected root in that parent while retaining row order.
		for _, group := range groups {
			at := indexOf(group.after) + 1
			group.parent.children = append(group.parent.children, make([]*item, len(group.clones))...)
			copy(group.parent.children[at+len(group.clones):], group.parent.children[at:])
			copy(group.parent.children[at:], group.clones)
		}
		m.clearSel()
		m.unsaved = true
		m.refreshRows()
		m.cursor = m.findRow(last, ctx)
		m.flash = fmt.Sprintf("duplicated %s", nodeNoun(len(targets)))
	case "/note":
		// a mirror is the same node everywhere: edit the original's note
		cur = m.tree.resolve(cur)
		if zoteroMirrored(cur) {
			m.flash = "zotero · a mirrored entry is read-only · alt+r refreshes it"
			return m, nil
		}
		m.mode = modeNote
		m.notePrev = cur.note
		m.caret = len([]rune(cur.note))
	case "/move:here":
		// pick any node (incl. a Temporary Domain node) and move it here
		m.openFinder(actBringHere)
	case "/mirror:to":
		// send THIS node's mirror in as a child of the picked node
		m.openFinder(actMirrorFrom)
	case "/mirror:from":
		// bring the picked node's mirror here (replaces an empty node, else lands below)
		m.openFinder(actMirrorHere)
	case "/link":
		// copy the lflow://node/<uuid> link of the cursor node — or of every
		// selected root when a row selection is live — to the clipboard. The
		// pasted link reads as plain text and converts to a link chip on ctrl+t
		// (see detectURLNear); /insert → Link splices the chip in directly.
		targets := m.selectionRoots()
		if len(targets) == 0 {
			if cur := m.cursorItem(); cur != nil {
				targets = []*item{cur}
			}
		}
		if len(targets) == 0 {
			return m, nil
		}
		var linkText strings.Builder
		for _, t := range targets {
			linkText.WriteString(nodeLinkURI(m.tree.sourceUUID(t)))
			linkText.WriteByte('\n')
		}
		_, ok := clipWrite(strings.TrimSuffix(linkText.String(), "\n"))
		if !ok {
			m.errorFlash("no clipboard (need wl-copy/xclip/pbcopy, or a terminal that takes OSC 52)")
			return m, nil
		}
		m.clearSel()
		noun := "link"
		if len(targets) > 1 {
			noun = "links"
		}
		m.flash = fmt.Sprintf("copied %d %s to clipboard", len(targets), noun)
	case "/mirror:workflowy":
		// the Workflowy sibling: this node becomes the pull handle, and alt+r
		// fetches the subtree once it holds a link (see wf.go)
		if cur.mirrorOf != "" || cur.readonly {
			m.flash = "node is not editable"
			return m, nil
		}
		m.pushUndo("")
		cur.typ = database.TypeWF
		m.unsaved = true
		if _, ok := m.wfIDFor(cur); ok {
			return m, runWF(m, cur)
		}
		m.flash = "workflowy · paste a link into this node, then alt+r"
	case "/move:to":
		m.openFinder(actMoveTo)
	case "/goto":
		m.openFinder(actGoto)
	case "/backlinks":
		// list every node that mirrors or [[-links to this one; pick → jump.
		// Flush first: /backlinks queries the DB directly, and a link/mirror
		// created earlier in this session may still be sitting unflushed in
		// memory (auto-sync is debounced ~1s) — without this the query would
		// show no matches for a link that plainly exists in the outline.
		if _, err := m.saveAll(); err != nil {
			m.errorFlash("save: " + err.Error())
			return m, nil
		}
		m.openFinder(actBacklinks)
	case "/undo":
		m.undo()
	case "/redo":
		m.redo()
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
	key := k.String()
	switch key {
	case "esc":
		cur.note = m.notePrev
		m.clearTextSel()
		m.noteRich = false
		m.mode = modeOutline
		m.caret = len([]rune(cur.name))
		return m, nil
	case "enter":
		// Notes use the same structured text contract as names. Commit textual
		// tag/date/link forms to chip records before leaving the field; cmd,
		// agent, Zotero and other anchors already present pass through unchanged.
		if m.db != nil {
			if note, err := database.ChipifyName(m.db, cur.note); err == nil {
				cur.note = note
				if chips, loadErr := database.LoadChips(m.db); loadErr == nil {
					m.chips = chips
				}
			} else {
				m.errorFlash("note: " + err.Error())
				return m, nil
			}
		}
		m.clearTextSel()
		m.noteRich = false
		m.mode = modeOutline
		m.unsaved = true
		m.caret = len([]rune(cur.name))
		return m, nil
	case "shift+left":
		m.extendTextSel(-1, false)
		return m, nil
	case "shift+right":
		m.extendTextSel(1, false)
		return m, nil
	case "ctrl+shift+left", "alt+shift+left":
		m.extendTextSel(-1, true)
		return m, nil
	case "ctrl+shift+right", "alt+shift+right":
		m.extendTextSel(1, true)
		return m, nil
	case "alt+a", "alt+P":
		return m.openNoteInsert()
	case "alt+c":
		if _, _, _, ok := m.textSelection(); ok {
			m.noteRich = true
			m.mode = modeStyle
			m.list.open(m, styleSource{}, true)
		}
		return m, nil
	}

	// A selection behaves like a selection here too: typing over it replaces it,
	// backspace removes it whole. Both run before the gestures below so the run
	// is already gone by the time the typed character is placed.
	if m.textSelOn {
		if key == "backspace" {
			if m.deleteTextSelection() {
				return m, nil
			}
		} else if isTypedRune(k) {
			// a paste that IS a link names the run after it, same as node text
			if m.pasteLinkOverSelection(k) {
				return m, nil
			}
			m.deleteTextSelection()
		}
	}

	// The doubled chip gestures work in notes exactly as they do in node text.
	if k.Type == tea.KeyRunes && len(k.Runes) > 0 && !k.Alt && !k.Paste {
		typed := string(k.Runes)
		switch {
		case typed == "[" && m.noteRuneBefore('['):
			m.removeNoteRuneBeforeCaret()
			m.noteRich = true
			m.openFinder(actLinkInsert)
			return m, nil
		case typed == "$" && m.noteRuneBefore('$'):
			m.removeNoteRuneBeforeCaret()
			if anchor := m.createChip(chipKindBash, ""); anchor != "" {
				m.insertLiteralAt(cur, m.caret, anchor)
				m.flash = "empty $ chip · alt+i edits the command"
			}
			return m, nil
		case typed == "@" && m.noteRuneBefore('@'):
			m.removeNoteRuneBeforeCaret()
			m.noteRich = true
			return m.openCitePicker(citeChip)
		case typed == "#" && m.noteAtWordStart():
			m.noteRich = true
			return m.openCompleter(cur, complTag, "#")
		case typed == ":" && m.noteAtWordStart():
			m.noteRich = true
			return m.openIconPicker(cur)
		}
	}

	before := cur.note
	beforeCaret := m.caret
	if key == " " || (k.Type == tea.KeySpace && !k.Alt) {
		m.chipifyNoteBeforeCaret(cur)
		before, beforeCaret = cur.note, m.caret
	}
	// text editing is consistent everywhere: the note field honors the same
	// caret vocabulary as the link editor, via the shared textField primitive.
	f := textField{value: cur.note, caret: m.caret}
	if f.handleKey(k) {
		cur.note = f.value
		m.caret = f.caret
		delta := len([]rune(cur.note)) - len([]rune(before))
		if delta != 0 {
			at := min(beforeCaret, m.caret)
			shiftSpans(noteSpanUUID(cur), at, delta)
			m.persistSpans(noteSpanUUID(cur))
		}
		m.clearTextSel()
		m.unsaved = true
	}
	return m, nil
}

func (m *Model) quit() (tea.Model, tea.Cmd) {
	if m.muxm != nil {
		m.muxm.KillAll()
	}
	if m.queryLoad != nil {
		close(m.queryLoad.done)
		m.queryLoad = nil
	}
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
				_ = database.GCBlobs(m.ctx.DB)       // drop image blobs whose node is gone
				_ = database.GCZoteroNodes(m.ctx.DB) // drop mirror bindings whose node is gone
			}
		}
	}
	m.quitting = true
	return m, tea.Quit
}

// Run opens the inline node editor on the given node.
func Run(ctx runtime.Ctx, nodeUUID string) error {
	return RunFile(ctx, nodeUUID, FileSession{})
}

// FileSession configures a file-backed editor run.
type FileSession struct {
	// OnSave fires after every successful saveAll: ctrl+s, quit, and the
	// debounced auto-flush (~1s after typing pauses — scheduleSync arms it
	// whenever OnSave is set, live daemon or not; see livesync.go). The tree
	// persists to the scratch DB first, then serializes to disk, so a crash
	// loses at most the last second of typing, never the whole session.
	OnSave func() error
	// AllowedTypes restricts the /type picker to what the file format
	// accepts (nil = unrestricted).
	AllowedTypes map[string]bool
	// DefaultType is the type a freshly typed node gets ("" = bullets) — a
	// .py session types python statements, not bullets.
	DefaultType string
}

// RunFile is Run for a file-backed session.
func RunFile(ctx runtime.Ctx, nodeUUID string, fs FileSession) error {
	onSave, allowedTypes := fs.OnSave, fs.AllowedTypes

	t, err := loadTree(ctx.DB, nodeUUID)
	if err != nil {
		return errors.Wrap(err, "loading node tree")
	}
	t.defaultType = fs.DefaultType // "" = bullets; a file session types its format's default

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
		nodeSpans = sp // styled text runs (see spans.go)
	}
	if lc, err := database.AllLineCharacters(ctx.DB); err == nil {
		lineCharacters = lc // package var, like tagColors: the render path is Model-free
	}
	if cc, err := database.AllCharacterColors(ctx.DB); err == nil {
		characterColors = cc
	}
	if zb, err := database.AllZoteroNodes(ctx.DB); err == nil {
		zoteroBindings = zb // mirrored Zotero entries (see zoteroitem.go)
	}

	m := &Model{
		db:           ctx.DB,
		ctx:          ctx,
		tree:         t,
		chips:        chips,
		tempTree:     tempTree, // the Temp root subtree, persisted alongside Root
		viewStack:    []*item{t.root},
		wfMap:        wfMap,
		live:         ctx.Live, // daemon connection: live sync (nil in direct runs)
		onSave:       onSave,
		allowedTypes: allowedTypes,
	}
	m.hydrateBashChipPreviews() // rebuild → chrome from local node_output (chip label is never stored)
	m.hydrateRunTails()    // and the same for runnable NODES, whose rows hang a → tail
	m.hydrateAgentChips()  // same for session chips: the label is the session's live title
	m.startFeed()          // subscribe to external changes; Init retries if it failed
	m.loadSettings()       // apply persisted preferences (theme, …) before the first render
	m.loadDeps()           // NodeCLIDeps: which CLI backends the daemon can exec
	m.loadSuggests()       // proposals waiting on review (alt+v settles one)
	m.refreshAncestors()
	m.refreshRows()
	m.ensureTempTree()    // the panel is always visible, so it must always have >=1 node
	m.backfillChipsOnce() // one-time: convert legacy plain-text #tags/dates to chips
	m.refreshRows()

	// Full-screen: the editor owns the alt screen for the whole run, so a
	// mid-session resize or overflow frame never touches the user's real
	// scrollback. bubbletea restores the normal screen on exit, discarding
	// whatever was last drawn there — the styled outline below is what
	// actually reaches the normal screen, printed once after Run returns.
	// The mouse is NOT captured by default — the terminal owns drag-select and
	// copy-on-select. The "mouse: wheel" setting turns capture on (Init /
	// handleSettingsKey) so the wheel scrolls the outline instead.
	multiplexer.CaptureHostColors() // ask the real terminal its colors while we still own the tty
	p := tea.NewProgram(m, tea.WithAltScreen())
	final, err := p.Run()
	if err != nil {
		return errors.Wrap(err, "running editor")
	}

	fm, ok := final.(*Model)
	if !ok {
		fm = m
	}
	if fm.err != nil {
		return fm.err
	}

	// The alt screen is gone now; this is the first thing printed to the
	// normal screen. m.quitting is still set from the Quit path, so
	// finalView renders the same fully-expanded, quit-time-styled outline it
	// always has (tableBandLines etc. still suppress their live-only bits).
	width := fm.width
	if width <= 0 {
		width = 80
	}
	fmt.Print(strings.Join(fm.finalView(width-1), "\n") + "\n")

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
