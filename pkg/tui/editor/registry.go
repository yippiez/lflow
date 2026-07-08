package editor

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/lflow/lflow/pkg/tui/database"
)

// nodeType is the per-type descriptor — the single place a node type's editor
// behavior is declared. Adding a type = one entry here (plus optional render /
// glyph / expand funcs), instead of editing the scattered switches in glyphFor,
// renderBody, the /type picker, and the inline-edit guards.
//
// Cross-cutting concerns stay where they belong: the mirror ◆ glyph and the
// collapsed ● glyph are handled in glyphFor (they apply to every type); the
// legacy display attributes (heading bold, quote bar, code background) stay in
// renderBody. A rich type that wants full control of its inline body sets
// `render` (like json); a type that is read-only inline sets InlineEditable=false;
// a type with an alt+e panel sets `expand`.
type nodeType struct {
	key, label     string
	sign           string                             // inline prefix sign, e.g. "$ "; "" = none
	glyph          func(it *item) (string, string)    // per-type glyph+color; nil → default ○/●
	render         func(it *item, name string) string // stateless inline-body override; nil → default
	renderM        func(m *Model, it *item) string    // Model-aware inline-body override (voice waveform)
	inlineEditable bool                               // false → typing/backspace/enter is a no-op
	tempOnly       bool                               // only offered/allowed in the Temporary Domain
	expand         func(m *Model, it *item) tea.Cmd   // alt+e action (action-only types, e.g. voice play, file → $EDITOR)
	run            func(m *Model, it *item) tea.Cmd   // alt+r action; nil → none
	view           nodeView                           // alt+e inline expanded view; nil → none
	// flashActions lets a type declare its own flash (alt+s) actions — each a verb,
	// a chip color, and a handler — so flash surfaces named, colored actions with no
	// switch in flash.go. nil → the actions are inferred from run/view/expand (a
	// generic "run"/"expand"). See flashActionsFor. jump and fold stay universal.
	flashActions func(m *Model, it *item) []flashAction

	// hooks below exist for the artifact bridge (see artifact.go): granular
	// enough that an editable type like log keeps caret editing while a JS
	// program decides its look. Built-ins may use them too.
	prefix    func(it *item) string // styled prefix before the body, e.g. the log time chip
	baseColor func(it *item) string // body foreground SGR; "" keeps the default
	muteFrom  func(name string) int // rune index the muted tail starts at; -1 = none
}

// nodeView is a node type's INLINE expanded view: alt+e focuses it, its lines
// render as bands beneath the node (in the outline flow — never a separate
// screen), and while focused it captures keys. It is stateless — it receives
// (m, it) every call and keeps per-node state in m's data stores keyed by
// it.uuid — so one value serves every node of the type. This is what lets a new
// rich type plug in via one registry entry instead of a central mode.
type nodeView interface {
	// Enter focuses the view (alt+e); seed any edit buffer here. false declines
	// (nothing to show), leaving focus off.
	Enter(m *Model, it *item) bool
	// Lines reports the total band-line count at this width, so the caller can
	// clamp the scroll offset. Pure.
	Lines(m *Model, it *item, width int) int
	// Bands renders the view as band lines beneath the node, self-windowed to
	// [scroll, scroll+winH). rail is the tree-rail prefix; focused draws carets.
	Bands(m *Model, it *item, rail string, width, scroll, winH int, focused bool) []string
	// Key handles a key while focused; handled=false falls through (esc/ctrl+c
	// are handled centrally).
	Key(m *Model, it *item, k tea.KeyMsg) (tea.Cmd, bool)
	// Leave is called on esc/defocus — flush the edit buffer to persisted state.
	Leave(m *Model, it *item)
}

// nodeViewOf returns the inline view for an item's type, or nil.
func nodeViewOf(it *item) nodeView {
	if it == nil {
		return nil
	}
	return typeOf(it.typ).view
}

// nodeTypes is the ordered registry; the /type picker shows it in this order.
//
// WARNING (invariant): everything is a node with a free-string type — a NEW node
// type is a new entry here (plus its descriptor), NOT a DB migration. nodes.type
// is free text and typeOf() falls back to bullets for unknown keys.
var nodeTypes = []nodeType{
	{key: database.TypeBullets, label: "Bullet", inlineEditable: true},
	{key: database.TypeTodo, label: "Todo", glyph: todoGlyph, inlineEditable: true},
	// a divider has no body text — viewOutline/finalView render it as a full-width
	// rule (see dividerLine), hiding the glyph. It is otherwise a normal node: it
	// nests, moves, takes a /note, and is removed with ctrl+d.
	{key: database.TypeDivider, label: "Divider", inlineEditable: false},
	// log is NOT here: it moved to the artifact model (the seeded "log" row in
	// the artifacts table renders the → glyph, muted time chip and · tail).
	{key: database.TypeH1, label: "Heading 1", glyph: headingGlyph("1"), inlineEditable: true},
	{key: database.TypeH2, label: "Heading 2", glyph: headingGlyph("2"), inlineEditable: true},
	{key: database.TypeH3, label: "Heading 3", glyph: headingGlyph("3"), inlineEditable: true},
	{key: database.TypeCode, label: "Code", inlineEditable: true},
	{key: database.TypeQuote, label: "Quote", inlineEditable: true},
	{
		key: database.TypeJSON, label: "JSON", inlineEditable: false,
		render: func(it *item, name string) string { return renderJSONPreview(name) },
		view:   jsonView{},
	},
	{
		key: database.TypeBash, label: "Bash", sign: "$ ", inlineEditable: true,
		run:  runBash,
		view: bashView{}, // alt+e: scrollable, color-preserving output viewer
	},
	{
		key: database.TypeQuery, label: "Query", sign: "⌕ ", inlineEditable: true,
		run: runQuery,
	},
	// a Workflowy mirror root (see wf.go): paste a workflowy link, alt+r pulls
	// the subtree in as readonly children, each one refreshable itself.
	{
		key: database.TypeWF, label: "Workflowy", glyph: wfGlyph, inlineEditable: true,
		run: runWF,
	},
	// an agent reply (see agent.go): red ✦, body red, plain text + chips only,
	// read-only inline — the agent's message is a record, not an editable note.
	{
		key: database.TypeAgent, label: "Agent", glyph: agentGlyph, inlineEditable: false,
		baseColor: func(it *item) string { return cRed },
	},
	{
		key: database.TypeVoice, label: "Voice", inlineEditable: false,
		renderM:      func(m *Model, it *item) string { return m.voiceRender(it) },
		run:          runVoice,
		expand:       playVoice,
		flashActions: voiceFlashActions, // name them: "record" (toggle) and "play"
	},
	{
		// an image: alt+r pastes from the host clipboard, alt+e opens the half-block
		// preview. The pixels live as a local PNG (~/.local/share/lflow/images/
		// <uuid>.png), never in the DB/sync; the name holds an optional caption.
		key: database.TypeImage, label: "Image", inlineEditable: false,
		renderM:      func(m *Model, it *item) string { return m.imageRender(it) },
		run:          runImagePaste,
		view:         imageView{}, // alt+e: scrollable half-block render
		flashActions: imageFlashActions,
	},
}

var byType = func() map[string]nodeType {
	m := make(map[string]nodeType, len(nodeTypes))
	for _, nt := range nodeTypes {
		m[nt.key] = nt
	}
	return m
}()

// typeOf returns the descriptor for a type key — compiled-in first, then
// runtime artifacts; unknown keys fall back to bullets, which is what keeps a
// node whose artifact was disabled or deleted rendering instead of crashing.
func typeOf(key string) nodeType {
	if nt, ok := byType[key]; ok {
		return nt
	}
	if nt, ok := artifactByKey[key]; ok {
		return nt
	}
	return byType[database.TypeBullets]
}

// typeOrder drives the /type picker: built-ins in their fixed order, then
// installed artifacts in load order. Recomputed per call because artifacts
// hot-load at runtime (an agent install shows up immediately).
func typeOrder() []string {
	out := make([]string, 0, len(nodeTypes)+len(artifactTypes))
	for _, nt := range nodeTypes {
		out = append(out, nt.key)
	}
	for _, nt := range artifactTypes {
		out = append(out, nt.key)
	}
	return out
}

// typeLabel is the picker label for a type key.
func typeLabel(key string) string {
	return typeOf(key).label
}

// WARNING (invariant): an artifact type is indistinguishable from a built-in
// everywhere a type is READ (glyphs, rendering, storage). The /type picker is
// the one management surface: artifact rows there carry the space/ctrl+d
// enable/disable/uninstall chords and disabled artifacts list muted — there is
// no separate /artifacts view or CLI.

func todoGlyph(it *item) (string, string) {
	if it.completedAt > 0 {
		return glyphTodoDone, cDim
	}
	return glyphTodo, cDim
}

func headingGlyph(digit string) func(it *item) (string, string) {
	return func(it *item) (string, string) { return digit, cBold + cYellow }
}
