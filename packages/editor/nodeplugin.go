package editor

import (
	"context"
	"sort"
	"sync"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lflow/lflow/packages/database"
	"github.com/lflow/lflow/packages/nlp"
	"github.com/lflow/lflow/packages/nodes"
)

// The node plugin host. The contract (nodes.Plugin, nodes.Host, nodes.Ref)
// lives in packages/nodes next to the types written against it; this file is
// the editor's half: nodeRef/Model implement the interfaces, the registered
// plugins fold into the internal nodeType table, and the render bridges
// (theme, shine, code blocks, run bands) are bound to the editor's own
// machinery at init.

// nodeRef is the editor's nodes.Ref.
type nodeRef struct {
	m  *Model
	it *item
}

func (n nodeRef) UUID() string { return n.it.uuid }
func (n nodeRef) Type() string { return n.it.typ }

func (n nodeRef) Text() string {
	// a structure-only ref (BodyTail's render path) carries no Model: fall back
	// to the raw name instead of panicking — anchors stay unexpanded there.
	if n.m == nil {
		return n.it.name
	}
	return expandAnchors(n.m.tree.displayName(n.it), n.m.chips)
}

func (n nodeRef) SetText(s string) {
	n.it.name = s
	if n.m != nil {
		n.m.unsaved = true
	}
}

func (n nodeRef) Parent() (nodes.Ref, bool) {
	if p := n.it.parent; p != nil && p.uuid != "" {
		return nodeRef{m: n.m, it: p}, true
	}
	return nil, false
}

func (n nodeRef) Siblings() []nodes.Ref {
	if n.it.parent == nil {
		return nil
	}
	out := make([]nodes.Ref, 0, len(n.it.parent.children))
	for _, c := range n.it.parent.children {
		out = append(out, nodeRef{m: n.m, it: c})
	}
	return out
}

func (n nodeRef) Children() []nodes.Ref {
	out := make([]nodes.Ref, 0, len(n.it.children))
	for _, c := range n.it.children {
		out = append(out, nodeRef{m: n.m, it: c})
	}
	return out
}

func (n nodeRef) Is(o nodes.Ref) bool {
	or, ok := o.(nodeRef)
	return ok && n.it == or.it
}

func (n nodeRef) CompletedAt() int64 { return n.it.completedAt }

// removeNodeStateUnder lets plugins cancel in-flight work before a subtree
// disappears locally or through live sync.
func (m *Model) removeNodeStateUnder(it *item) {
	if it == nil {
		return
	}
	var walk func(*item)
	walk = func(n *item) {
		for _, p := range nodes.Registered() {
			if p.OnRemove != nil {
				p.OnRemove(m, n.uuid)
			}
		}
		for _, child := range n.children {
			walk(child)
		}
	}
	walk(it)
}

// ── the fold: nodes.Registered() → the internal nodeType table ──────────────

// foldOnce runs the fold lazily on the first registry lookup: plugin packages
// (nodes, fileeditor) register at their own init, and only cmd knows which of
// them are linked in — so the editor waits until the registry is first read.
var foldOnce sync.Once

// ensurePlugins is a var bound in init: nodeTypes' own initializer reaches
// typeOf through the flash-action hooks, so a direct func reference would be
// an initialization cycle — the indirection defers the edge to runtime.
var ensurePlugins = func() {}

func init() { ensurePlugins = foldPlugins }

// foldPlugins folds every registered plugin into nodeTypes/byType, then
// re-sorts the table into database.TypeOrder so the /type picker order is the
// canonical one no matter which package registered when.
func foldPlugins() {
	foldOnce.Do(func() {
		for _, p := range nodes.Registered() {
			nodeTypes = append(nodeTypes, pluginNodeType(p))
		}
		pos := make(map[string]int, len(database.TypeOrder))
		for i, k := range database.TypeOrder {
			pos[k] = i
		}
		sort.SliceStable(nodeTypes, func(i, j int) bool {
			pi, iOK := pos[nodeTypes[i].key]
			pj, jOK := pos[nodeTypes[j].key]
			if iOK != jOK {
				return iOK // keys outside TypeOrder sink to the end
			}
			return pi < pj
		})
		byType = make(map[string]nodeType, len(nodeTypes))
		for _, nt := range nodeTypes {
			byType[nt.key] = nt
		}
	})
}

// pluginNodeType adapts one nodes.Plugin onto the internal descriptor.
func pluginNodeType(p nodes.Plugin) nodeType {
	nt := nodeType{
		key:             p.Key,
		label:           p.Label,
		sign:            p.Sign,
		inlineEditable:  p.InlineEditable,
		autoFocus:       p.AutoFocus,
		blockFaces:      p.BlockFaces,
		cliDeps:         p.CLIDeps,
		internal:        p.Internal,
		tempOnly:        p.TempOnly,
		disableChips:    p.DisableChips,
		continueOnEnter: p.ContinueOnEnter,
		runInTail:       p.RunInTail,
		fixedColor:      p.FixedColor,
		muteFrom:        p.MuteFrom,
	}
	if p.GlyphRef != nil {
		g := p.GlyphRef
		nt.glyph = func(it *item) (string, string) { return g(nodeRef{it: it}) } // structure-only ref
	}
	if p.Prefix != nil {
		pf := p.Prefix
		nt.prefix = func(it *item) string { return pf(nodeRef{it: it}) } // structure-only ref
	}
	if p.OnType != nil {
		ot := p.OnType
		nt.onType = func(m *Model, it *item) { ot(m, nodeRef{m: m, it: it}) }
	}
	if p.Expand != nil {
		ex := p.Expand
		nt.expand = func(m *Model, it *item) tea.Cmd { return ex(m, nodeRef{m: m, it: it}) }
	}
	if p.OpenHost != nil {
		oh := p.OpenHost
		nt.openHost = func(m *Model, it *item) tea.Cmd { return oh(m, nodeRef{m: m, it: it}) }
	}
	if p.FlashActions != nil {
		fa := p.FlashActions
		nt.flashActions = func(m *Model, it *item) []flashAction {
			acts := fa(m, nodeRef{m: m, it: it})
			out := make([]flashAction, 0, len(acts))
			for _, a := range acts {
				do := a.Do
				out = append(out, flashAction{verb: a.Verb, color: a.Color, do: func(m *Model, it *item) tea.Cmd {
					return do(m, nodeRef{m: m, it: it})
				}})
			}
			return out
		}
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
	if p.SpanColor != nil {
		sc := p.SpanColor
		nt.spanColor = func(_ *item, runes []rune) map[int]string { return sc(runes) }
	}
	if p.BodyTail != nil {
		bt := p.BodyTail
		nt.bodyTail = func(it *item, _ map[string]database.Chip) string { return bt(nodeRef{it: it}) } // structure-only ref
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
	return nt
}

// nodePluginViewAdapter bridges nodes.PluginView onto the internal nodeView.
type nodePluginViewAdapter struct{ v nodes.PluginView }

func (a nodePluginViewAdapter) enter(m *Model, it *item) bool {
	return a.v.Enter(m, nodeRef{m: m, it: it})
}
func (a nodePluginViewAdapter) lines(m *Model, it *item, width int) int {
	return a.v.Lines(m, nodeRef{m: m, it: it}, width)
}
func (a nodePluginViewAdapter) bands(m *Model, it *item, rail string, width, scroll, winH int, focused bool) []string {
	return a.v.Bands(m, nodeRef{m: m, it: it}, rail, width, scroll, winH, focused)
}
func (a nodePluginViewAdapter) key(m *Model, it *item, k tea.KeyMsg) (tea.Cmd, bool) {
	return a.v.Key(m, nodeRef{m: m, it: it}, k)
}
func (a nodePluginViewAdapter) leave(m *Model, it *item) {
	a.v.Leave(m, nodeRef{m: m, it: it})
}

// ── nodes.Host implementation on the Model ──────────────────────────────────

func (m *Model) NodeStore(uuid string) map[string]any { return m.nodeStore(uuid) }
func (m *Model) NodeDB() *database.DB                 { return m.db }
func (m *Model) NodeFlash(msg string)                 { m.flash = msg }
func (m *Model) NodeDepOK(bin string) bool            { return m.depOK(bin) }

// NodeCompute runs one raw code-generation turn: the instruction in, code out.
func (m *Model) NodeCompute(ctx context.Context, prompt string, onEvent func(nlp.Event)) (string, error) {
	return nlp.Compute(ctx, prompt, onEvent)
}

// ── the render bridges: bind nodes' vars to the editor's machinery ──────────

func init() {
	nodes.Theme = func() nodes.Palette {
		return nodes.Palette{
			Reset: cReset, Bold: cBold, FG: cFG, Dim: cDim, Accent: cAccent,
			Red: cRed, Green: cGreen, Yellow: cYellow, Cyan: cCyan,
		}
	}
	nodes.ShineText = ShineText
	nodes.CodeBlockBands = CodeBlockBands
	nodes.RunOut = func(h nodes.Host, uuid string, lines []string) {
		m, ok := h.(*Model)
		if !ok {
			return
		}
		r := m.ensureRun(uuid)
		r.out = r.out[:0]
		for _, l := range lines {
			r.out = append(r.out, outLine{text: l})
		}
		r.dropped = 0
		m.persistRunOut(uuid)
		m.refreshRows()
	}
}
