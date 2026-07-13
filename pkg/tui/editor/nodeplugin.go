package editor

import (
	"context"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lflow/lflow/pkg/tui/database"
	"github.com/lflow/lflow/pkg/tui/tag"
)

// The node plugin host. The editor owns the GENERIC machinery — registry,
// pickers, bands, dep gating, agent transport — and each pluggable node type
// lives in its own file under editor/nodes, registered at init through
// RegisterNodePlugin. Plugins see the editor only through NodeHost and a
// NodeRef — no editor internals leak, so a node file reads standalone.
//
// Everything in this file wears the Node prefix: it is the node-facing API.

// NodeRef is a plugin's handle on one outline node — an interface, so node
// tests can fake it without an editor Model.
type NodeRef interface {
	// UUID returns the node's identity.
	UUID() string
	// Type returns the node's type key.
	Type() string
	// Text returns the display text with chips expanded (an @Pi chip reads
	// "@Pi", a path chip its full path); a mirror reads its source's name.
	Text() string
	// SetText replaces the node's plain text and marks it for the next flush.
	SetText(s string)
	// PathChip returns the node's first path-chip value, or "".
	PathChip() string
	// Parent returns the node's parent, when it has a real one.
	Parent() (NodeRef, bool)
	// Siblings returns the node's parent's children (the node included).
	Siblings() []NodeRef
	// Children returns the node's children.
	Children() []NodeRef
	// Is reports whether two refs name the same node.
	Is(o NodeRef) bool
}

// nodeRef is the editor's NodeRef.
type nodeRef struct {
	m  *Model
	it *item
}

func (n nodeRef) UUID() string { return n.it.uuid }
func (n nodeRef) Type() string { return n.it.typ }

func (n nodeRef) Text() string {
	return expandAnchors(n.m.tree.displayName(n.it), n.m.chips)
}

func (n nodeRef) SetText(s string) {
	n.it.name = s
	n.m.unsaved = true
}

func (n nodeRef) PathChip() string {
	for _, sp := range anchorSpans([]rune(n.it.name)) {
		if c, ok := n.m.chips[sp.id]; ok && c.Kind == chipKindPath {
			return c.Value
		}
	}
	return ""
}

func (n nodeRef) Parent() (NodeRef, bool) {
	if p := n.it.parent; p != nil && p.uuid != "" {
		return nodeRef{m: n.m, it: p}, true
	}
	return nil, false
}

func (n nodeRef) Siblings() []NodeRef {
	if n.it.parent == nil {
		return nil
	}
	out := make([]NodeRef, 0, len(n.it.parent.children))
	for _, c := range n.it.parent.children {
		out = append(out, nodeRef{m: n.m, it: c})
	}
	return out
}

func (n nodeRef) Children() []NodeRef {
	out := make([]NodeRef, 0, len(n.it.children))
	for _, c := range n.it.children {
		out = append(out, nodeRef{m: n.m, it: c})
	}
	return out
}

func (n nodeRef) Is(o NodeRef) bool {
	or, ok := o.(nodeRef)
	return ok && n.it == or.it
}

// NodeHost is the editor surface a plugin may touch.
type NodeHost interface {
	// NodeStore is the ephemeral per-node state bag (never persisted).
	NodeStore(uuid string) map[string]any
	// NodeDB is the live database handle (nil in the ephemeral temp tree).
	NodeDB() *database.DB
	// NodeFlash shows a transient message in the bar.
	NodeFlash(msg string)
	// NodeDepOK reports a CLI binary's availability (NodeCLIDeps; judged by
	// the daemon — the execution side).
	NodeDepOK(bin string) bool
	// NodeDefaultAgent names the first configured agent (the compute default).
	NodeDefaultAgent() string
	// NodeAgentGate returns the missing CLI backend of an agent, if any.
	NodeAgentGate(name string) (string, bool)
	// NodeComputeTurn runs one raw agent turn (system+prompt as-is) — on the
	// daemon when connected, locally otherwise. Cancel ctx to stop it.
	NodeComputeTurn(ctx context.Context, agentName, system, prompt, cwd string) (<-chan tag.Event, error)
}

// NodePlugin declares one pluggable node type — the exported mirror of the
// registry's nodeType, hooks phrased in NodeHost/NodeRef.
type NodePlugin struct {
	Key, Label, Sign string
	InlineEditable   bool
	// AutoFocus makes resting the cursor on this node's block face auto-enter its
	// view for editing (thin caret, type directly, no alt+e) — like the Code node
	// (see reconcileAutoFocus). The View's Enter gates it (return false to stay
	// inline), so a two-faced node focuses only on its code face.
	AutoFocus bool
	// BlockFaces makes alt+e TOGGLE this node between its Render (prose) face and
	// its BlockCode (code) face instead of entering an editor — editing the code
	// face is handled by AutoFocus. The face lives in the node store under the key
	// NodeBlockFace reads. Pair with AutoFocus + BlockCode.
	BlockFaces bool
	CLIDeps    []string

	Glyph     func() (string, string) // static glyph + SGR (per-node glyphs stay core for now)
	BaseColor func() string           // body SGR; nil/"" default
	Render    func(h NodeHost, n NodeRef) string // inline body override
	Run       func(h NodeHost, n NodeRef) tea.Cmd          // alt+r
	View      NodePluginView                               // alt+e inline expanded view
	// BlockCode makes the node render AS a borderless code block that REPLACES its
	// row (no glyph/body line): it returns the code, the caret rune index when the
	// node is the focused editing target (else -1), and ok=false to render the
	// normal Render row instead. nil → always the normal row.
	BlockCode func(h NodeHost, n NodeRef, focused bool) (code string, caret int, ok bool)
	// Preview renders always-on band lines beneath the unfocused node (the
	// image-thumbnail slot); focused reports the expanded view being open.
	Preview   func(h NodeHost, n NodeRef, rail string, maxLine int, focused bool) []string
	ToContext func(h NodeHost, n NodeRef) (tag, attrs, body string)
	// OnRemove fires when a node of this type leaves the tree — cancel any
	// in-flight work keyed on it.
	OnRemove func(h NodeHost, uuid string)
}

// NodePluginView is the plugin flavor of nodeView (see registry.go): the
// alt+e inline expanded editor, stateless, per-node state in NodeStore.
type NodePluginView interface {
	Enter(h NodeHost, n NodeRef) bool
	Lines(h NodeHost, n NodeRef, width int) int
	Bands(h NodeHost, n NodeRef, rail string, width, scroll, winH int, focused bool) []string
	Key(h NodeHost, n NodeRef, k tea.KeyMsg) (tea.Cmd, bool)
	Leave(h NodeHost, n NodeRef)
}

// NodePluginMsg lets a plugin's async tea.Cmd flow back into it: the editor's
// Update routes any message implementing it straight to the plugin.
type NodePluginMsg interface {
	HandleNodePlugin(h NodeHost) tea.Cmd
}

// nodePluginRemovals collects registered OnRemove hooks (see stopAgentsUnder).
var nodePluginRemovals []func(h NodeHost, uuid string)

// RegisterNodePlugin adds a plugin type to the registry — the plugin package
// calls this from init(); the /type picker, dep gating, alt+r/alt+e and agent
// context all pick it up like a built-in.
func RegisterNodePlugin(p NodePlugin) {
	nt := nodeType{
		key:            p.Key,
		label:          p.Label,
		sign:           p.Sign,
		inlineEditable: p.InlineEditable,
		autoFocus:      p.AutoFocus,
		blockFaces:     p.BlockFaces,
		cliDeps:        p.CLIDeps,
	}
	if p.Glyph != nil {
		g := p.Glyph
		nt.glyph = func(*item) (string, string) { return g() }
	}
	if p.BaseColor != nil {
		bc := p.BaseColor
		nt.baseColor = func(*item) string { return bc() }
	}
	if p.Render != nil {
		r := p.Render
		nt.renderM = func(m *Model, it *item) string { return r(m, nodeRef{m: m, it: it}) }
	}
	if p.Run != nil {
		run := p.Run
		nt.run = func(m *Model, it *item) tea.Cmd { return run(m, nodeRef{m: m, it: it}) }
	}
	if p.View != nil {
		nt.view = nodePluginViewAdapter{v: p.View}
	}
	if p.BlockCode != nil {
		bc := p.BlockCode
		nt.blockCode = func(m *Model, it *item, focused bool) (string, int, bool) {
			return bc(m, nodeRef{m: m, it: it}, focused)
		}
	}
	if p.Preview != nil {
		pv := p.Preview
		nt.bands = func(m *Model, r row, below bool, maxLine int) []string {
			focused := m.focused && m.cursorItem() == r.it
			return pv(m, nodeRef{m: m, it: r.it}, continuationPrefix(r, below), maxLine, focused)
		}
	}
	if p.ToContext != nil {
		tc := p.ToContext
		nt.toContextM = func(m *Model, it *item) contextXML {
			t, a, b := tc(m, nodeRef{m: m, it: it})
			return contextXML{tag: t, attrs: a, body: b}
		}
	}
	if p.OnRemove != nil {
		nodePluginRemovals = append(nodePluginRemovals, p.OnRemove)
	}
	nodeTypes = append(nodeTypes, nt)
	if byType != nil { // registration after init(): keep the index live
		byType[nt.key] = nt
	}
}

// nodePluginViewAdapter bridges NodePluginView onto the internal nodeView.
type nodePluginViewAdapter struct{ v NodePluginView }

func (a nodePluginViewAdapter) Enter(m *Model, it *item) bool {
	return a.v.Enter(m, nodeRef{m: m, it: it})
}
func (a nodePluginViewAdapter) Lines(m *Model, it *item, width int) int {
	return a.v.Lines(m, nodeRef{m: m, it: it}, width)
}
func (a nodePluginViewAdapter) Bands(m *Model, it *item, rail string, width, scroll, winH int, focused bool) []string {
	return a.v.Bands(m, nodeRef{m: m, it: it}, rail, width, scroll, winH, focused)
}
func (a nodePluginViewAdapter) Key(m *Model, it *item, k tea.KeyMsg) (tea.Cmd, bool) {
	return a.v.Key(m, nodeRef{m: m, it: it}, k)
}
func (a nodePluginViewAdapter) Leave(m *Model, it *item) {
	a.v.Leave(m, nodeRef{m: m, it: it})
}

// ── NodeHost implementation on the Model ────────────────────────────────────

func (m *Model) NodeStore(uuid string) map[string]any { return m.nodeStore(uuid) }
func (m *Model) NodeDB() *database.DB                 { return m.db }
func (m *Model) NodeFlash(msg string)                 { m.flash = msg }
func (m *Model) NodeDepOK(bin string) bool            { return m.depOK(bin) }

func (m *Model) NodeDefaultAgent() string {
	for _, a := range m.agents {
		return a.Name
	}
	return "Pi"
}

func (m *Model) NodeAgentGate(name string) (string, bool) {
	if a, ok := m.agentByName(name); ok {
		return m.agentDepMissing(a)
	}
	return "", false
}

// NodeComputeTurn runs a raw generation turn — daemon-side when connected
// (the client is only a client), local CLI otherwise; mock and websocket
// agents keep their thread transport with the prompt wrapped as one.
func (m *Model) NodeComputeTurn(ctx context.Context, agentName, system, prompt, cwd string) (<-chan tag.Event, error) {
	ag := tag.Agent{Name: agentName}
	if a, ok := m.agentByName(agentName); ok {
		ag = a
	}
	if m.live != nil && !ag.Mock && ag.URL == "" {
		wch, err := m.live.AgentPrompt(ctx, agentName, system, prompt, cwd, tag.SkillDir())
		if err != nil {
			return nil, err
		}
		out := make(chan tag.Event, 16)
		go func() {
			defer close(out)
			for ev := range wch {
				out <- tag.Event{Op: ev.Op, Text: ev.Text, Tool: ev.Tool, Placement: ev.Placement}
			}
		}()
		return out, nil
	}
	cl, err := m.tagClientFor(ag)
	if err != nil {
		return nil, err
	}
	if c, ok := cl.(*tag.CLIClient); ok {
		c.Cwd = cwd
		return c.SendPrompt(ctx, agentName, system, prompt)
	}
	// mock/websocket transports only speak threads — wrap the prompt as one,
	// mention included so a discretionary agent knows it is addressed
	return cl.Send(ctx, agentName, []tag.ThreadNode{{Name: "@" + agentName + " " + prompt, Role: "user", Asked: true}})
}

// ── plugin-facing helpers (the render toolkit) ──────────────────────────────

// NodeBlockFace reads the alt+e block/prose face toggle for a BlockFaces node
// from the node store (core's alt+e handler writes it): "nlp" = show the Render
// row, anything else ("" default, "code") = show the BlockCode block.
func NodeBlockFace(h NodeHost, uuid string) string {
	s, _ := h.NodeStore(uuid)["blockFace"].(string)
	return s
}

// NodeClip trims a styled line to a display width (ANSI aware).
func NodeClip(s string, width int) string { return clip(s, width) }

// NodeWindowBands clamps a band list to [scroll, scroll+winH).
func NodeWindowBands(content []string, scroll, winH int) []string {
	if scroll > len(content) {
		scroll = len(content)
	}
	end := scroll + winH
	if end > len(content) {
		end = len(content)
	}
	return content[scroll:end]
}

// NodePalette is the live theme's SGR codes — read it per render, the values
// change when the theme does.
type NodePalette struct {
	Reset, FG, Dim, Accent, Red, Green, Yellow, Cyan string
}

// NodeTheme returns the live palette.
func NodeTheme() NodePalette {
	return NodePalette{
		Reset: cReset, FG: cFG, Dim: cDim, Accent: cAccent,
		Red: cRed, Green: cGreen, Yellow: cYellow, Cyan: cCyan,
	}
}

// NodeColor maps a /style color name (red, orange, …) to its themed SGR.
func NodeColor(name string) string { return styleColorCode[name] }

// NodeFirstNonEmpty returns a unless it is empty.
func NodeFirstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// NodeVisibleWidth measures a styled string's display width (ANSI aware).
func NodeVisibleWidth(s string) int { return visibleWidth(s) }

// The multi-line caret helpers behind a plugin's code face (see CodeBlockBands):
// NodeCaretVMove walks the caret up/down a line keeping its column;
// NodeCaretLineCol / NodeCaretAt convert between a caret index and line/column
// (home = col 0, end = a huge col).
func NodeCaretVMove(s string, caret, dir int) int      { return jsonCaretLineMove(s, caret, dir) }
func NodeCaretLineCol(s string, caret int) (int, int)  { return jsonCaretLC(s, caret) }
func NodeCaretAt(s string, line, col int) int          { return jsonLCCaret(s, line, col) }

// NodeFuzzyMatch reports whether needle subsequence-matches hay.
func NodeFuzzyMatch(hay, needle string) bool { return fuzzyMatch(hay, needle) }
