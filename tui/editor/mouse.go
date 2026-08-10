package editor

// Mouse support. The wheel scrolls the outline; in the "click" mouse mode three
// more gestures reach into the page: a click on a row's outline circle zooms
// into that node, a click on a #tag opens /goto already searching that tag, and
// a click on a toolbar breadcrumb walks the view back to that node — the
// segment under the pointer lifting out of the bar's gray first, so a crumb
// reads as clickable before anybody clicks it.
//
// All three share one mechanism: the renderer RECORDS the cells it painted.
// Deriving them from a mouse coordinate instead would mean a second
// implementation of the row layout — the connector rail's width, how far a chip
// expands, where the status bar wrapped — and the two would part ways the first
// time a glyph moved. So each frame leaves behind the zones it drew, in screen
// coordinates, and a click is a lookup rather than a calculation.

import (
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mattn/go-runewidth"

	"github.com/lflow/lflow/tui/database"
)

// The values of the "mouse" preference (see settings.go).
const (
	mouseSelect = "select" // the terminal owns the mouse: native drag-select
	mouseWheel  = "wheel"  // captured: the wheel scrolls the outline
	mouseClick  = "click"  // captured: the wheel scrolls AND the page answers clicks
)

// mouseCapture is the capture command a mouse preference asks for. Cell-motion
// tracking is enough to report the wheel, but the breadcrumb hover needs motion
// with NO button held, which only all-motion tracking reports — so the click
// mode costs an event per pointer move, and the wheel mode still does not.
func mouseCapture(mode string) tea.Cmd {
	switch mode {
	case mouseClick:
		return tea.EnableMouseAllMotion
	case mouseWheel:
		return tea.EnableMouseCellMotion
	}
	return tea.DisableMouse
}

// hitKind is what a recorded zone does when it is clicked.
type hitKind uint8

const (
	hitRow   hitKind = iota // anywhere on a row's line: put the cursor there
	hitGlyph                // the outline circle: zoom into the node
	hitTag                  // a #tag in a row's text: /goto, searching that tag
	hitCrumb                // one toolbar breadcrumb segment: walk the view there
)

// hitZone is one rectangle of a painted frame that answers to the mouse: a
// single screen row and the visible column range [x0, x1).
type hitZone struct {
	kind   hitKind
	row    int
	x0, x1 int
	idx    int    // hitRow/hitGlyph: the m.rows index; hitCrumb: the crumb index
	tag    string // hitTag: the tag word, without its '#'
}

// crumb is one breadcrumb segment and where clicking it lands. A segment above
// the loaded root names a node the tree is not loaded around, so it reopens
// there (uuid); a zoom-stack segment only pops the stack back to itself (keep =
// the view-stack length to keep).
type crumb struct {
	label string
	uuid  string
	keep  int
}

// mouseState is the last frame's hit zones plus the hover the toolbar paints.
// body and bar zones are recorded in REGION-relative rows, because neither
// region knows its own screen origin until viewFrame stacks them; merge shifts
// both into the frame coordinates a click is tested against.
type mouseState struct {
	body  []hitZone
	bar   []hitZone
	zones []hitZone
	// hover is the breadcrumb segment under the pointer, ONE-based so the zero
	// value — a model that has never seen the mouse — means nothing is hovered.
	hover int
}

// reset drops the previous frame's zones. The hover survives it: the pointer has
// not moved just because the outline repainted.
func (s *mouseState) reset() { s.body, s.bar, s.zones = nil, nil, nil }

// merge lifts the region-relative rows into frame coordinates: bodyTop is the
// screen row the focused region's first line landed on, barTop the status bar's.
func (s *mouseState) merge(bodyTop, barTop int) {
	s.zones = make([]hitZone, 0, len(s.body)+len(s.bar))
	for _, z := range s.body {
		z.row += bodyTop
		s.zones = append(s.zones, z)
	}
	for _, z := range s.bar {
		z.row += barTop
		s.zones = append(s.zones, z)
	}
}

// at returns the zone under a screen cell. Each line records its specific zones
// (the glyph, then the tags) before the row-wide fallback, so the first match is
// always the most specific one.
func (s *mouseState) at(x, y int) (hitZone, bool) {
	for _, z := range s.zones {
		if z.row == y && x >= z.x0 && x < z.x1 {
			return z, true
		}
	}
	return hitZone{}, false
}

// mouseClicks reports whether the page answers clicks — the "click" preference.
func (m *Model) mouseClicks() bool { return m.setting("mouse") == mouseClick }

// handleMouse routes a mouse event. The wheel scrolls in both captured modes
// (see scrollBody / scrollTempPanel); clicks and hover are the click mode's.
// Wheel events bypass handleKey, so they never clear the scroll pin — the next
// real key does, exactly like after a pgup. A click DOES unpin it, because it
// moves the cursor and the window has to follow.
func (m *Model) handleMouse(msg tea.MouseMsg) tea.Cmd {
	if m.mode != modeOutline || m.setting("mouse") == mouseSelect {
		// select mode never captures, but a toggle mid-flight can still land one
		// last event from the terminal — the preference decides, not the arrival
		return nil
	}
	switch msg.Button {
	case tea.MouseButtonWheelUp:
		if msg.Action == tea.MouseActionPress && !m.scrollTempPanel(-wheelStep, msg.Y) {
			m.scrollBody(-wheelStep)
		}
		return nil
	case tea.MouseButtonWheelDown:
		if msg.Action == tea.MouseActionPress && !m.scrollTempPanel(wheelStep, msg.Y) {
			m.scrollBody(wheelStep)
		}
		return nil
	}
	if !m.mouseClicks() {
		return nil
	}
	switch msg.Action {
	case tea.MouseActionMotion, tea.MouseActionRelease:
		// Only the toolbar reacts to a bare pointer, so only a crumb is tracked:
		// underlining whatever row the mouse happened to cross would turn every
		// pointer move into a repaint of the outline for no information.
		m.mouse.hover = 0
		if z, ok := m.mouse.at(msg.X, msg.Y); ok && z.kind == hitCrumb {
			m.mouse.hover = z.idx + 1
		}
		return nil
	case tea.MouseActionPress:
		if msg.Button != tea.MouseButtonLeft {
			return nil
		}
		if z, ok := m.mouse.at(msg.X, msg.Y); ok {
			return m.clickZone(z)
		}
	}
	return nil
}

// clickZone performs a zone's gesture.
func (m *Model) clickZone(z hitZone) tea.Cmd {
	switch z.kind {
	case hitGlyph:
		// the circle is the node's handle: clicking it zooms in, the mouse's
		// spelling of alt+right
		m.clickRow(z.idx)
		m.zoomInto(m.cursorItem())
	case hitTag:
		// a tag is a question ("what else is filed under this?"), so clicking one
		// asks it: /goto, already searching that tag. The cursor moves to the tag's
		// own row first, which is also what keeps that node out of its own results.
		m.clickRow(z.idx)
		if !m.tempActive {
			m.openFinderQuery(actGoto, "#"+z.tag)
		}
	case hitRow:
		m.clickRow(z.idx)
	case hitCrumb:
		m.followCrumb(z.idx)
	}
	return nil
}

// clickRow puts the cursor on a clicked row. The caret lands at the END of the
// row's text rather than under the pointer: a column maps back to a rune offset
// only through the chip expansion the renderer just performed, and a caret that
// lands one character off every time a row carries a chip is worse than one that
// lands somewhere predictable. The click also unpins a wheel/pgup scroll, the
// way any cursor-moving key does.
func (m *Model) clickRow(idx int) {
	if idx < 0 || idx >= len(m.rows) {
		return
	}
	m.clearSel()
	m.clearTextSel()
	m.scrolling = false
	m.cursor = idx
	if cur := m.cursorItem(); cur != nil {
		m.caret = len([]rune(m.caretText(cur)))
	}
	m.clampCaret()
}

// zoomInto pushes the view onto a node — the shared body of alt+right and a
// click on a row's glyph. A mirror carries no children in memory, so it zooms
// into its source, whose children are the ones that actually render (see
// mirrorContext).
func (m *Model) zoomInto(cur *item) {
	if cur == nil {
		return
	}
	if cur.mirrorOf != "" {
		src, ok := m.tree.byUUID[m.tree.sourceUUID(cur)]
		if !ok {
			return
		}
		cur = src
	}
	m.viewStack = append(m.viewStack, cur)
	m.cursor = 0
	m.caret = 0
	m.refreshRows()
}

// followCrumb walks the view to a clicked breadcrumb segment: a zoom-stack
// segment pops the stack back to it and lands the cursor on the node we left, an
// ancestor above the loaded root reopens the tree there with the node we came
// from under the cursor. The current view root is already where we are, so its
// own crumb does nothing.
func (m *Model) followCrumb(i int) {
	if m.tempActive {
		return // the toolbar describes the main outline; temp holds the focus
	}
	crumbs := m.crumbTargets()
	if i < 0 || i >= len(crumbs) {
		return
	}
	m.mouse.hover = 0
	c := crumbs[i]
	if c.uuid != "" {
		// focus the crumb one level deeper — the node we are arriving from
		focus := m.viewStack[0].uuid
		if i+1 < len(crumbs) && crumbs[i+1].uuid != "" {
			focus = crumbs[i+1].uuid
		}
		m.reopenAt(c.uuid, focus)
		return
	}
	if c.keep < 1 || c.keep >= len(m.viewStack) {
		return
	}
	zoomed := m.viewRoot()
	m.viewStack = m.viewStack[:c.keep]
	m.refreshRows()
	m.cursor = m.rowIndexOf(zoomed)
	m.caret = 0
}

// crumbTargets rebuilds the breadcrumb path the toolbar last painted, so a click
// resolves against exactly the segments that were on screen. It mirrors
// bottomBar's stash swap: the toolbar always describes the main outline, even
// while the Temporary Domain holds focus.
func (m *Model) crumbTargets() []crumb {
	t, ancestors, stack := m.tree, m.ancestors, m.viewStack
	if m.tempActive && m.mainStash.tree != nil {
		t, ancestors, stack = m.mainStash.tree, m.mainStash.ancestors, m.mainStash.viewStack
	}
	return breadcrumbs(t, m.chips, ancestors, stack)
}

// breadcrumbs is the toolbar path: the forest ancestors above the loaded root,
// then the zoom stack down to the current view root. One place builds it, so the
// bar's text and the mouse's targets cannot disagree about where a segment ends.
func breadcrumbs(t *tree, chips map[string]database.Chip, ancestors []ancestor, stack []*item) []crumb {
	out := make([]crumb, 0, len(ancestors)+len(stack))
	for _, a := range ancestors {
		out = append(out, crumb{label: a.name, uuid: a.uuid})
	}
	for i, v := range stack {
		name := displayAnchors(t.displayName(v), chips) // resolve chip anchors
		if name == "" {
			name = "untitled"
		}
		out = append(out, crumb{label: name, keep: i + 1})
	}
	return out
}

// recordBodyZones records one painted body line's clickable cells at
// body-relative screen row srow. r is the outline row the line renders (idx its
// index in m.rows) and first marks the group's opening line — the one carrying
// the glyph. Specific zones go in ahead of the row-wide fallback (see at).
func (m *Model) recordBodyZones(srow, idx int, r row, first bool, line string, maxLine int) {
	if first {
		if c := glyphColumn(r); c >= 0 {
			m.mouse.body = append(m.mouse.body, hitZone{kind: hitGlyph, row: srow, x0: c, x1: c + 1, idx: idx})
		}
	}
	// Tags are found in the PAINTED text, not in the stored name: a tag that
	// lives as a chip anchor is then as clickable as one typed literally, and the
	// columns are the ones actually on screen — past a connector rail, past
	// however wide the chips ahead of it expanded. A code block is excluded: its
	// content is source, where a "#include" or a "# comment" is not a tag.
	if r.it != nil && typeOf(r.it.typ).blockCode == nil {
		runes, cols := visibleCells(line)
		for _, sp := range database.TagSpans(string(runes)) {
			if sp[0] < 0 || sp[1] > len(runes) || sp[1] <= sp[0] {
				continue
			}
			last := sp[1] - 1
			m.mouse.body = append(m.mouse.body, hitZone{
				kind: hitTag,
				row:  srow,
				x0:   cols[sp[0]],
				x1:   cols[last] + runewidth.RuneWidth(runes[last]),
				idx:  idx,
				tag:  string(runes[sp[0]+1 : sp[1]]), // drop the '#'
			})
		}
	}
	m.mouse.body = append(m.mouse.body, hitZone{kind: hitRow, row: srow, x0: 0, x1: maxLine, idx: idx})
}

// glyphColumn is the screen column of a row's glyph: past the line's leading
// space and the connector rail, exactly where renderRow paints it. An empty row
// and a divider have no glyph to click and report -1.
func glyphColumn(r row) int {
	if r.it == nil || r.it.typ == database.TypeEmpty || r.it.typ == database.TypeDivider {
		return -1
	}
	return 1 + visibleWidth(connector(r))
}

// visibleCells strips a painted line to the runes a terminal actually shows and
// the screen column each one starts at — the map a mouse X needs to point back
// into styled text.
func visibleCells(line string) (runes []rune, cols []int) {
	col := 0
	for i := 0; i < len(line); {
		if line[i] == '\x1b' {
			i = ansiEscapeEnd(line, i)
			continue
		}
		r, sz := utf8.DecodeRuneInString(line[i:])
		runes = append(runes, r)
		cols = append(cols, col)
		col += runewidth.RuneWidth(r)
		i += sz
	}
	return runes, cols
}
