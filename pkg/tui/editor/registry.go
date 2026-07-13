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
// Cross-cutting concerns stay where they belong: the collapsed ● glyph is
// handled in glyphFor (it applies to every type); the
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
	autoFocus      bool                               // resting the cursor here auto-enters its view (thin caret, type directly) — no alt+e; see reconcileAutoFocus
	blockFaces     bool                               // alt+e toggles the Render (prose) face ⇄ the BlockCode (code) face instead of entering an editor (nlpcompute); see toggleBlockFace
	tempOnly       bool                               // only offered/allowed in the Temporary Domain
	internal       bool                               // never offered in /type: only the app creates nodes of this type
	searchHidden   bool                               // never surfaced by finders (/goto, /mirror, /move, [[) or live queries, unless ":type:" names it explicitly
	disableChips   bool                               // structured chip gestures (#, >, [[, @, dates, $) remain literal in this type
	expand         func(m *Model, it *item) tea.Cmd   // alt+e action (action-only types, e.g. voice play, file → $EDITOR)
	run            func(m *Model, it *item) tea.Cmd   // alt+r action; nil → none
	view           nodeView                           // alt+e inline expanded view; nil → none
	// flashActions lets a type declare its own flash (alt+s) actions — each a verb,
	// a chip color, and a handler — so flash surfaces named, colored actions with no
	// switch in flash.go. nil → the actions are inferred from run/view/expand (a
	// generic "run"/"expand"). See flashActionsFor. jump and fold stay universal.
	flashActions func(m *Model, it *item) []flashAction

	// cliDeps lists the CLI binaries the type shells out to (NodeCLIDeps).
	// Availability is judged by the daemon (execution side); a missing dep
	// greys the type in /type and alt+r errors "Missing dependency: <bin>".
	cliDeps []string

	// bands hangs extra band lines beneath the node in the outline flow (like the
	// note / run-output bands), e.g. the image thumbnail. Called from both render
	// paths (viewRenderRows and finalView) so a banded type declares it once here
	// instead of a type check in each. nil → no extra bands.
	bands func(m *Model, r row, below bool, maxLine int) []string
	// blockCode makes the node render AS a borderless code block that REPLACES its
	// row (no glyph/body line — see viewRenderRows / blockGroupLines): it returns
	// the code, the caret rune index when the node is the focused editing target
	// (else -1), and ok=false to fall back to the normal row. nil → normal row.
	blockCode func(m *Model, it *item, focused bool) (code string, caret int, ok bool)
	// continueOnEnter makes Enter from this type open another node of the same type
	// — the todo-list continuation, where a fresh sibling stays a todo.
	continueOnEnter bool

	// granular look hooks: an editable type keeps caret editing while these
	// decide its look (glyph, prefix, muted tail) — see the log type.
	prefix    func(it *item) string // styled prefix before the body, e.g. the log time chip
	baseColor func(it *item) string // body foreground SGR; "" keeps the default
	muteFrom  func(name string) int // rune index the muted tail starts at; -1 = none
	// spanColor tints individual runes of an EDITABLE node's body without taking
	// over the whole render (unlike `render`): it returns index→SGR-color for the
	// runes to recolor, applied through the same per-rune path as magic keywords,
	// so the caret and selection still work. The Math type paints its operator
	// glyphs yellow this way. nil → no per-rune tint. (index is a rune index.)
	spanColor func(it *item, runes []rune) map[int]string
	// bodyTail appends already-styled text after the node's body on the same row
	// (before the ★ mark) — the Math type's dim linear preview of its subtree.
	// Called for the resting/selected row alike. nil → nothing. "" → nothing.
	bodyTail func(it *item) string

	// toContext gives the type its own XML element in the agent context (see
	// buildThread → tag.renderThread), so a typed node reads coherently to an
	// agent instead of flattening to a bare <node> line: the element name, its
	// attributes, and an optional multi-line body replacing the one-line name.
	// Children still nest inside the element, and the role tags (asked/answer/
	// parent) still win the element name so threading survives. nil → <node>.
	toContext func(it *item) contextXML
	// toContextM is the Model-aware sibling (mirrors render/renderM) for types
	// whose context body lives outside the item — e.g. a document loaded from
	// a node blob. When set it wins over toContext.
	toContextM func(m *Model, it *item) contextXML
}

// contextXML is what a toContext hook returns — the pieces of the node's XML
// element in the agent context. Zero values keep the defaults.
type contextXML struct {
	tag   string // element name; "" keeps "node"
	attrs string // inside the opening tag, e.g. `done="true"`
	body  string // multi-line element content replacing the name line
}

// xmlTag is the trivial toContext hook — the element name alone carries the
// type (<code>…</code>, <quote>…</quote>).
func xmlTag(name string) func(*item) contextXML {
	return func(*item) contextXML { return contextXML{tag: name} }
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
	{key: database.TypeTodo, label: "Todo", glyph: todoGlyph, inlineEditable: true, continueOnEnter: true,
		toContext: todoToContext},
	// a divider has no body text — viewOutline/finalView render it as a full-width
	// rule (see dividerLine), hiding the glyph. It is otherwise a normal node: it
	// nests, moves, takes a /note, and is removed with ctrl+d.
	{key: database.TypeDivider, label: "Divider", inlineEditable: false, toContext: xmlTag("divider")},
	{key: database.TypeH1, label: "Heading 1", glyph: headingGlyph("1"), inlineEditable: true, toContext: xmlTag("h1")},
	{key: database.TypeH2, label: "Heading 2", glyph: headingGlyph("2"), inlineEditable: true, toContext: xmlTag("h2")},
	{key: database.TypeH3, label: "Heading 3", glyph: headingGlyph("3"), inlineEditable: true, toContext: xmlTag("h3")},
	// the Code node is a multi-line block; resting the cursor on it auto-focuses
	// its block editor (autoFocus — a thin caret shows, type directly, no alt+e),
	// so it is not inlineEditable. The borderless gray block REPLACES the node's
	// row (blockCode), and its multi-line body IS it.name (see code.go).
	{
		key: database.TypeCode, label: "Code", inlineEditable: false, autoFocus: true,
		render:    codeInlineRender, // compact fallback (the temp panel, unknown surfaces)
		view:      codeView{},
		blockCode: codeBlockCode,
		toContext: codeToContext,
	},
	{key: database.TypeQuote, label: "Quote", inlineEditable: true, toContext: xmlTag("quote")},
	// a timestamped journal line (see log.go); was the log.js NodeMod before
	// the extension system was removed — nodes typed under the mod light up
	// unchanged, the key is the same free string.
	{
		key: database.TypeLog, label: "Log", inlineEditable: true,
		glyph:     logGlyph,
		prefix:    logPrefix,
		baseColor: func(it *item) string { return cDim }, // /color overrides (render.go)
		muteFrom:  logMuteFrom,
		toContext: logToContext,
	},
	{
		key: database.TypeJSON, label: "JSON", inlineEditable: false,
		render:    func(it *item, name string) string { return renderJSONPreview(name) },
		view:      jsonView{},
		toContext: jsonToContext,
	},
	// there is deliberately NO bash node type: inline runnable shell is the cmd
	// chip ("$cmd" + double space, see cmdchip.go) — legacy "bash"-typed nodes
	// fall back to bullets like any unknown type, text intact.
	{
		key: database.TypeQuery, label: "Query", inlineEditable: true, disableChips: true,
		prefix:    queryPrefix,
		run:       runQuery,
		toContext: xmlTag("query"),
	},
	// a Workflowy mirror root (see wf.go): paste a workflowy link, alt+r pulls
	// the subtree in as readonly children, each one refreshable itself.
	{
		key: database.TypeWF, label: "Workflowy", glyph: wfGlyph, inlineEditable: true,
		run:       runWF,
		toContext: xmlTag("workflowy"),
	},
	// an agent reply (see agent.go): red ✦, body red, plain text + chips.
	// Typed attachments (code, image, bash-as-cmd, json, …) hang as locked
	// children — spoken via {{attach:…}} tokens, not conversation bullets.
	// Internal — the /type picker never offers it, only the agent creates one —
	// and born locked so a reply can't change under the thread; /lock unlocks
	// it for reshaping like any other node. Search-hidden: replies are answers
	// in a thread, not navigation targets, so no finder or query lists them.
	{
		key: database.TypeAgent, label: "Agent", glyph: agentGlyph, inlineEditable: true,
		internal:     true,
		searchHidden: true,
		baseColor:    func(it *item) string { return cRed },
	},
	{
		key: database.TypeVoice, label: "Voice", inlineEditable: false,
		renderM:      func(m *Model, it *item) string { return m.voiceRender(it) },
		run:          runVoice,
		expand:       playVoice,
		flashActions: voiceFlashActions, // name them: "record" (toggle) and "play"
		cliDeps:      []string{"ffmpeg"},
		toContext:    xmlTag("voice"),
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
		bands:        func(m *Model, r row, below bool, maxLine int) []string { return m.imageBandLines(r, below, maxLine) },
		toContext:    xmlTag("image"), // pixels never travel — the caption is the context
	},
	// a math expression composed as an outline (see math.go): the node's text is
	// an operator (colored yellow) with operands as children, or an atom leaf.
	// Stays inline-editable; the operator row carries a dim linear preview of its
	// whole subtree, and children fan out as the AST beneath it.
	{
		key: database.TypeMath, label: "Math", inlineEditable: true,
		spanColor: mathSpanColor,
		bodyTail:  mathBodyTail,
		toContext: mathToContext,
	},
	// The pluggable node types — nlpcompute — live in editor/nodes (one Go file
	// per node) and register themselves via RegisterNodePlugin at init; see
	// nodeplugin.go.
}

// byType fills in init() — a var initializer would cycle: nodeTypes references
// runQuery, which reaches typeOf/byType through the query type filter.
var byType map[string]nodeType

func init() {
	byType = make(map[string]nodeType, len(nodeTypes))
	for _, nt := range nodeTypes {
		byType[nt.key] = nt
	}
}

// typeOf returns the descriptor for a type key; unknown keys fall back to
// bullets, which is what keeps a node of a retired type (e.g. the removed
// NodeMod system's) rendering instead of crashing.
func typeOf(key string) nodeType {
	if nt, ok := byType[key]; ok {
		return nt
	}
	return byType[database.TypeBullets]
}

// typeOrder drives the /type picker: the registry in its declared order.
func typeOrder() []string {
	out := make([]string, 0, len(nodeTypes))
	for _, nt := range nodeTypes {
		out = append(out, nt.key)
	}
	return out
}

// typeLabel is the picker label for a type key.
func typeLabel(key string) string {
	return typeOf(key).label
}

func todoGlyph(it *item) (string, string) {
	if it.completedAt > 0 {
		return glyphTodoDone, cDim
	}
	return glyphTodo, cDim
}

// todoToContext carries the checkbox state — the one thing a todo's text
// alone can't tell an agent.
func todoToContext(it *item) contextXML {
	done := "false"
	if it.completedAt > 0 {
		done = "true"
	}
	return contextXML{tag: "todo", attrs: `done="` + done + `"`}
}

func headingGlyph(digit string) func(it *item) (string, string) {
	return func(it *item) (string, string) { return digit, cBold + cYellow }
}
