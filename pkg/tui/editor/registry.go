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
	disableChips   bool                               // structured chip gestures (#, >, [[, dates, $) remain literal in this type
	expand         func(m *Model, it *item) tea.Cmd   // alt+e action (action-only types, e.g. voice play, file → $EDITOR)
	run            func(m *Model, it *item) tea.Cmd   // alt+r action; nil → none
	view           nodeView                           // alt+e inline expanded view; nil → none
	// openHost is the alt+o action: hand the node's content to the HOST — the
	// desktop's own viewer/app, outside the terminal entirely (image → the
	// system image previewer). The terminal keeps drawing inline unicode; this
	// is the escape hatch for pixels a text cell cannot carry. nil → none.
	openHost func(m *Model, it *item) tea.Cmd
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
	// onType runs right after /type has set this type on a node — the hook a type
	// uses to land on its own face instead of leaving the picker on a row that
	// looks unchanged (the Table folds to its grid face). nil → nothing.
	onType func(m *Model, it *item)

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
	// (before the ★ mark) — the Math type's dim linear preview of its subtree, the
	// Bash type's composed command line. The chip store comes along so a tail can
	// resolve anchors (a path chip in a command) instead of leaking sentinels.
	// Called for the resting/selected row alike. nil → nothing. "" → nothing.
	bodyTail func(it *item, chips map[string]database.Chip) string
	// runInTail says this type's run lives in the row's bodyTail — the streaming
	// headline after its "→" — and so claims NO output band beneath the row. The
	// Bash type sets it to read exactly like the cmd chip it is the tree form of:
	// the row streams, alt+e opens the terminal, nothing hangs in between.
	runInTail bool

	// toContext reserves a structured XML representation for consumers that
	// need typed outline context. nil means the generic node representation.
	toContext func(it *item) contextXML
	// toContextM is the Model-aware sibling (mirrors render/renderM) for types
	// whose context body lives outside the item — e.g. a document loaded from
	// a node blob. When set it wins over toContext.
	toContextM func(m *Model, it *item) contextXML
}

// contextXML is what a toContext hook returns — the pieces of the node's XML
// element in structured context. Zero values keep the defaults.
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
	// a divider renders as a full-width rule (see dividerLine), hiding the glyph;
	// an optional text runs on the midpoint of the rule, edited like any node. It
	// is otherwise a normal node: it nests, moves, takes a /note, and is removed
	// with ctrl+d.
	{key: database.TypeDivider, label: "Divider", inlineEditable: true, toContext: xmlTag("divider")},
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
	// a shell command composed AS an outline (see bashnode.go): a "$" row whose
	// children are its parts — a join operator (| && || ;) or a wrapper ($() ())
	// composes them, anything else heads them — so a long pipeline is written as
	// a tree. alt+r runs THIS node's subtree, alt+e opens the output. The inline
	// cmd chip ("$$", cmdchip.go) remains the one-liner surface.
	{
		key: database.TypeBash, label: "Bash", inlineEditable: true,
		glyph:        bashGlyph,
		spanColor:    bashSpanColor,
		bodyTail:     bashBodyTail,
		run:          runBashNode,
		view:         runOutView{},
		flashActions: bashFlashActions,
		cliDeps:      []string{"bash"},
		runInTail:    true,
	},
	{
		key: database.TypeQuery, label: "Query", inlineEditable: true, disableChips: true,
		prefix:    queryPrefix,
		spanColor: querySpanColor, // "semantic phrase" marks tint yellow
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
		// preview, alt+o hands the PNG to the desktop's own image viewer. The
		// pixels are a PNG blob in node_blobs keyed by node uuid — so the outline
		// stays one portable SQLite file; the name holds an optional caption.
		key: database.TypeImage, label: "Image", inlineEditable: false,
		renderM:      func(m *Model, it *item) string { return m.imageRender(it) },
		run:          runImagePaste,
		view:         imageView{},   // alt+e: scrollable half-block render
		openHost:     imageOpenHost, // alt+o: the host's image viewer
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
		spanColor:    mathSpanColor,
		bodyTail:     mathBodyTail,
		run:          runMathLatex, // alt+r: export this subtree's LaTeX to the run band
		flashActions: mathFlashActions,
		toContext:    mathToContext,
	},
	// a table is an ordinary subtree READ as a grid (see table.go): columns are
	// the children, rows are their children, and a cell's children are the
	// outline inside it. The face IS the fold state — a folded table draws its
	// grid, an open one is the plain outline — so ctrl+space / alt+↑↓ toggle
	// table ⇄ nodes, and alt+e opens the grid editor.
	{
		key: database.TypeTable, label: "Table", inlineEditable: true,
		glyph:        tableGlyph,
		bands:        func(m *Model, r row, below bool, maxLine int) []string { return m.tableBandLines(r, below, maxLine) },
		view:         tableView{},
		flashActions: tableFlashActions,
		onType:       tableOnType,
		toContextM:   tableToContext,
	},
	// a molecule is an ordinary subtree READ as a structure: one atom per node,
	// the bond to the parent as a prefix (=O), children the atoms bonded to it —
	// so the outline IS the molecular graph, the way the Math node is the AST.
	// alt+e draws it. A notation string mentioned inline is a ⌬ chip instead.
	{
		key: database.TypeMol, label: "Molecule", inlineEditable: true,
		glyph:     moleculeGlyph,
		spanColor: molSpanColor,
		bodyTail:  molTreeBodyTail,
		view:      moleculeView{},
		// building a molecule as an outline is atom-after-atom, so Enter keeps the
		// type going (the todo-list continuation) — otherwise every second atom
		// would land as a bullet and need retyping.
		continueOnEnter: true,
	},
	// a Line is ordinary dialogue text with a character attached (see line.go):
	// the character renders as a colored "[NAME] " prefix, never baked into the
	// stored name. alt+e (and landing here fresh via /type) opens the character
	// picker — pick one already used in the outline or write a new one; alt+c
	// there recolors the highlighted character, and that color follows it to
	// every Line node that names it.
	{
		key: database.TypeLine, label: "Line", inlineEditable: true,
		prefix:    linePrefix,
		expand:    func(m *Model, it *item) tea.Cmd { m.openCharacterPicker(it); return nil },
		toContext: lineToContext,
	},
	// An agentic coding SESSION is not a node type: it is an inline chip only
	// (see agent.go), so there is no entry here.
	// Thinking is a plain marker node: always the muted-gray thinking glyph,
	// no other behavior.
	{key: database.TypeThinking, label: "Thinking", glyph: thinkingGlyph, inlineEditable: true},
	// The pluggable node types — nlpcompute — live in editor/nodes (one Go file
	// per node) and register themselves via RegisterNodePlugin at init; see
	// nodeplugin.go.
}

func thinkingGlyph(it *item) (string, string) { return glyphThinking, cDim }

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
// alone cannot express.
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
