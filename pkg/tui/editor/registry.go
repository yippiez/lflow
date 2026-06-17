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
	glyph          func(it *item) (string, string)     // per-type glyph+color; nil → default ○/●
	render         func(it *item, name string) string  // stateless inline-body override; nil → default
	renderM        func(m *Model, it *item) string     // Model-aware inline-body override (voice waveform)
	inlineEditable bool                                // false → typing/backspace/enter is a no-op
	expand         func(m *Model, it *item)           // alt+e action; nil → none
	run            func(m *Model, it *item) tea.Cmd   // alt+r action; nil → none
}

// nodeTypes is the ordered registry; the /type picker shows it in this order.
var nodeTypes = []nodeType{
	{key: database.TypeBullets, label: "Bullet", inlineEditable: true},
	{key: database.TypeTodo, label: "Todo", glyph: todoGlyph, inlineEditable: true},
	{key: database.TypeH1, label: "Heading 1", glyph: headingGlyph("1"), inlineEditable: true},
	{key: database.TypeH2, label: "Heading 2", glyph: headingGlyph("2"), inlineEditable: true},
	{key: database.TypeH3, label: "Heading 3", glyph: headingGlyph("3"), inlineEditable: true},
	{key: database.TypeCode, label: "Code", inlineEditable: true},
	{key: database.TypeQuote, label: "Quote", inlineEditable: true},
	{
		key: database.TypeJSON, label: "JSON", inlineEditable: false,
		render: func(it *item, name string) string { return renderJSONPreview(name) },
		expand: func(m *Model, it *item) { m.openJSON(it) },
	},
	{
		key: database.TypeBash, label: "Bash", sign: "$ ", inlineEditable: true,
		run: runBash,
	},
	{
		key: database.TypeQuery, label: "Query (codebase)", sign: "⌕ ", inlineEditable: true,
		run: runQuery,
	},
	{
		key: database.TypeVoice, label: "Voice note", inlineEditable: false,
		renderM: func(m *Model, it *item) string { return m.voiceRender(it) },
		run:     runVoice,
		expand:  playVoice,
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

func todoGlyph(it *item) (string, string) {
	if it.completedAt > 0 {
		return glyphTodoDone, cDim
	}
	return glyphTodo, cDim
}

func headingGlyph(digit string) func(it *item) (string, string) {
	return func(it *item) (string, string) { return digit, cBold + cYellow }
}
