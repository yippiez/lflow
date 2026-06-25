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
	// a log line: → glyph, a muted "(time)" chip, the label (colored by /color),
	// then a muted "· description" tail. Rendered in renderBody (TypeLog cases).
	{key: database.TypeLog, label: "Log", glyph: logGlyph, inlineEditable: true},
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
	{
		key: database.TypeVoice, label: "Voice", inlineEditable: false,
		renderM: func(m *Model, it *item) string { return m.voiceRender(it) },
		run:     runVoice,
		expand:  playVoice,
	},
	// an artifact node embeds a web page (.html, or .md rendered to html) in the
	// DB; ▣ glyph, the name is editable inline, alt+e opens it in the browser.
	{
		key: database.TypeArtifact, label: "Artifact", glyph: artifactGlyph,
		inlineEditable: true,
		expand:         openArtifactNode,
	},
}

var byType = func() map[string]nodeType {
	m := make(map[string]nodeType, len(nodeTypes))
	for _, nt := range nodeTypes {
		m[nt.key] = nt
	}
	return m
}()

// typeOf returns the descriptor for a type key; unknown keys fall back to bullets.
func typeOf(key string) nodeType {
	if nt, ok := byType[key]; ok {
		return nt
	}
	return byType[database.TypeBullets]
}

// typeOrder / typeLabels drive the /type picker, derived from the registry so
// there is a single source of truth.
var typeOrder = func() []string {
	out := make([]string, len(nodeTypes))
	for i, nt := range nodeTypes {
		out[i] = nt.key
	}
	return out
}()

var typeLabels = func() map[string]string {
	m := make(map[string]string, len(nodeTypes))
	for _, nt := range nodeTypes {
		m[nt.key] = nt.label
	}
	return m
}()

// logGlyph is the → arrow, tinted by the node's /color (muted gray by default) so
// the arrow and label share a color while the description stays muted.
func logGlyph(it *item) (string, string) {
	col := cDim
	if c := styleBaseColor(it.style); c != "" {
		col = c
	}
	return "→", col
}

func todoGlyph(it *item) (string, string) {
	if it.completedAt > 0 {
		return glyphTodoDone, cDim
	}
	return glyphTodo, cDim
}

func headingGlyph(digit string) func(it *item) (string, string) {
	return func(it *item) (string, string) { return digit, cBold + cYellow }
}
