package editor

import (
	"strings"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mattn/go-runewidth"
)

type mouseLine struct {
	row       int
	textStart int
	runeStart int
	editable  bool
	plain     string
}

type mouseZone struct {
	line, lo, hi int
	row          int
	action       string
	root         *item
	rootUUID     string
}

type mouseSpot struct{ line, column int }

type mouseDrag struct {
	from, to mouseSpot
	live, on bool
}

type mouseFrame struct {
	lines []mouseLine
	zones []mouseZone
	drag  mouseDrag
	hover mouseSpot
}

func (m *Model) mouseLineForRow(row int, rendered string, first, editable bool) mouseLine {
	plain := stripSGR(rendered)
	start := 0
	if row >= 0 && row < len(m.rows) {
		r := m.rows[row]
		if first {
			glyph, _ := glyphFor(r.it)
			start = visibleWidth(" " + connector(r) + glyph + " ")
		} else {
			below := row+1 < len(m.rows) && m.rows[row+1].depth > r.depth
			start = visibleWidth(continuationPrefix(r, below))
		}
	}
	return mouseLine{row: row, textStart: min(start, visibleWidth(plain)), editable: editable, plain: plain}
}

// finishMouseFrame attaches rune offsets and pressable zones to the exact lines
// that survived wrapping, scrolling, overlays, and final frame assembly.
func (m *Model) finishMouseFrame(lines []string) {
	starts := map[int][]int{}
	for i := range m.mouseFrame.lines {
		h := &m.mouseFrame.lines[i]
		if h.row < 0 || h.row >= len(m.rows) {
			continue
		}
		h.plain = stripSGR(lines[i])
		h.runeStart = len(starts[h.row])
		starts[h.row] = append(starts[h.row], i)
		if h.editable && h.runeStart == 0 {
			r := m.rows[h.row]
			glyph, _ := glyphFor(r.it)
			col := visibleWidth(" " + connector(r))
			action := "zoom"
			if m.nodeRunAction(r.it) != nil {
				action = "run"
			}
			m.mouseFrame.zones = append(m.mouseFrame.zones, mouseZone{line: i, lo: col, hi: col + visibleWidth(glyph), row: h.row, action: action})
		}
	}
	// Convert each visual-line ordinal into the source rune offset using the same
	// wrapping helper as keyboard vertical movement.
	for row, screenLines := range starts {
		visual := m.visualRowsFor(row)
		for ordinal, screen := range screenLines {
			if ordinal < len(visual) {
				m.mouseFrame.lines[screen].runeStart = visual[ordinal]
			}
		}
	}
	m.addBreadcrumbZones(lines)
}

func (m *Model) visualRowsFor(index int) []int {
	if index < 0 || index >= len(m.rows) {
		return []int{0}
	}
	r := m.rows[index]
	glyph, _ := glyphFor(r.it)
	name := m.tree.displayName(r.it)
	first := visibleWidth(" " + connector(r) + glyph + " ")
	below := index+1 < len(m.rows) && m.rows[index+1].depth > r.depth
	hang := visibleWidth(continuationPrefix(r, below))
	if hasAnchor(name) {
		return chipVisualRows(name, m.width-1, first, hang, m.chips)
	}
	return visualRows(name, m.width-1, first, hang)
}

func (m *Model) nodeRunAction(it *item) func(*Model, *item) tea.Cmd {
	inline := len(m.flashInlineRunActions(it))
	actions := m.flashActionsFor(it)
	for _, action := range actions[min(inline, len(actions)):] {
		if action.verb == "run" {
			return action.do
		}
	}
	return nil
}

func (m *Model) addBreadcrumbZones(lines []string) {
	// The same words can occur in row text. Breadcrumbs begin exactly where
	// viewFrame placed the status bar, after pageRows main-region lines.
	searchLine, searchByte := min(m.pageRows, len(lines)), 0
	stack, ancestors, ancestorUUIDs := m.viewStack, m.ancestors, m.ancestorUUIDs
	crumbTree := m.tree
	if m.tempActive {
		stack, ancestors, ancestorUUIDs = m.mainStash.viewStack, m.mainStash.ancestors, m.mainStash.ancestorUUIDs
		crumbTree = m.mainStash.tree
	}
	type crumb struct {
		label, uuid string
		root        *item
	}
	crumbs := make([]crumb, 0, len(ancestors)+len(stack))
	for i, label := range ancestors {
		uuid := ""
		if i < len(ancestorUUIDs) {
			uuid = ancestorUUIDs[i]
		}
		crumbs = append(crumbs, crumb{label: label, uuid: uuid})
	}
	for _, root := range stack {
		label := displayAnchors(crumbTree.displayName(root), m.chips)
		if label == "" {
			label = "untitled"
		}
		crumbs = append(crumbs, crumb{label: label, root: root})
	}
	for _, crumb := range crumbs {
		for line := searchLine; line < len(lines); line++ {
			plain := stripSGR(lines[line])
			from := 0
			if line == searchLine {
				from = min(searchByte, len(plain))
			}
			if rel := strings.Index(plain[from:], crumb.label); rel >= 0 {
				at := from + rel
				lo := visibleWidth(plain[:at])
				m.mouseFrame.zones = append(m.mouseFrame.zones, mouseZone{line: line, lo: lo, hi: lo + visibleWidth(crumb.label), action: "crumb", root: crumb.root, rootUUID: crumb.uuid})
				searchLine, searchByte = line, at+len(crumb.label)
				break
			}
		}
	}
}

func (m *Model) handleFrameMouse(msg tea.MouseMsg) tea.Cmd {
	line := msg.Y // Bubble Tea mouse coordinates are zero-based terminal rows.
	spot := mouseSpot{line: line, column: msg.X}
	switch msg.Action {
	case tea.MouseActionPress:
		if msg.Button != tea.MouseButtonLeft {
			return nil
		}
		m.mouseFrame.drag = mouseDrag{}
		for _, z := range m.mouseFrame.zones {
			if line != z.line || msg.X < z.lo || msg.X >= z.hi {
				continue
			}
			if z.action == "crumb" {
				if z.rootUUID != "" {
					m.reopenAt(z.rootUUID, "")
					return nil
				}
				for i, root := range m.viewStack {
					if root == z.root {
						m.viewStack = m.viewStack[:i+1]
						m.cursor, m.caret = 0, 0
						m.refreshRows()
						return nil
					}
				}
			}
			m.cursor, m.caret = z.row, 0
			if z.action == "zoom" {
				return flashZoom(m, m.cursorItem())
			}
			if run := m.nodeRunAction(m.cursorItem()); run != nil {
				return run(m, m.cursorItem())
			}
			return nil
		}
		if line < 0 || line >= len(m.mouseFrame.lines) || !m.mouseFrame.lines[line].editable {
			return nil
		}
		m.placeMouseCaret(line, msg.X)
		m.mouseFrame.drag = mouseDrag{from: spot, to: spot, live: true}
	case tea.MouseActionMotion:
		if m.mouseFrame.drag.live {
			m.mouseFrame.drag.to = spot
			m.mouseFrame.drag.on = spot != m.mouseFrame.drag.from
		} else {
			m.mouseFrame.hover = spot
		}
	case tea.MouseActionRelease:
		if m.mouseFrame.drag.live {
			m.mouseFrame.drag.to = spot
			m.mouseFrame.drag.on = spot != m.mouseFrame.drag.from
			m.mouseFrame.drag.live = false
			if text := m.mouseDragText(); strings.TrimSpace(text) != "" {
				if _, ok := clipWrite(text); ok {
					m.flash = "copied to clipboard"
				} else {
					m.errorFlash("no clipboard (need wl-copy/xclip/pbcopy, or a terminal that takes OSC 52)")
				}
			}
		}
	}
	return nil
}

func (m *Model) placeMouseCaret(line, column int) {
	if line < 0 || line >= len(m.mouseFrame.lines) {
		return
	}
	h := m.mouseFrame.lines[line]
	if !h.editable || h.row < 0 || h.row >= len(m.rows) {
		return
	}
	m.clearSel()
	m.clearTextSel()
	m.cursor = h.row
	want := max(column-h.textStart, 0)
	runes := []rune(m.tree.displayName(m.rows[h.row].it))
	caret, width := h.runeStart, 0
	for caret < len(runes) {
		w := runewidth.RuneWidth(runes[caret])
		if width+w > want {
			break
		}
		width += w
		caret++
	}
	m.caret = caret
	m.clampCaret()
}

func (m *Model) mouseDragBounds() (mouseSpot, mouseSpot, bool) {
	d := m.mouseFrame.drag
	if !d.on {
		return mouseSpot{}, mouseSpot{}, false
	}
	from, to := d.from, d.to
	if to.line < from.line || to.line == from.line && to.column < from.column {
		from, to = to, from
	}
	return from, to, true
}

func (m *Model) mouseDragText() string {
	from, to, ok := m.mouseDragBounds()
	if !ok {
		return ""
	}
	var out []string
	for line := max(from.line, 0); line <= to.line && line < len(m.mouseFrame.lines); line++ {
		h := m.mouseFrame.lines[line]
		if h.row < 0 {
			continue
		}
		runes := []rune(h.plain)
		lo, hi := runeAtCell(runes, h.textStart), len(runes)
		if line == from.line {
			lo = max(lo, runeAtCell(runes, from.column))
		}
		if line == to.line {
			hi = min(hi, runeAtCell(runes, to.column+1))
		}
		if hi >= lo {
			out = append(out, strings.TrimRight(string(runes[lo:hi]), " "))
		}
	}
	return strings.Join(out, "\n")
}

func runeAtCell(runes []rune, cell int) int {
	w := 0
	for i, r := range runes {
		if w >= cell {
			return i
		}
		w += runewidth.RuneWidth(r)
	}
	return len(runes)
}

func (m *Model) paintMouseDrag(lines []string) []string {
	from, to, ok := m.mouseDragBounds()
	if !ok {
		return lines
	}
	for line := max(from.line, 0); line <= to.line && line < len(lines) && line < len(m.mouseFrame.lines); line++ {
		h := m.mouseFrame.lines[line]
		if h.row < 0 {
			continue
		}
		lo, hi := h.textStart, visibleWidth(lines[line])
		if line == from.line {
			lo = max(lo, from.column)
		}
		if line == to.line {
			hi = min(hi, to.column+1)
		}
		if hi > lo {
			lines[line] = mouseHighlight(lines[line], lo, hi, bgTextSel())
		}
	}
	return lines
}

// paintMouseHover brightens the clickable cells under the cursor from dim gray
// to light gray: the node indicator (zoom/run cell) and the breadcrumb labels
// are pressable, so a quiet lift in the FOREGROUND says so before the click.
// The spot is re-resolved against the current frame's zones every render, so a
// stale spot from a resize or scroll never paints.
func (m *Model) paintMouseHover(lines []string) []string {
	if m.mouseFrame.drag.live {
		return lines
	}
	spot := m.mouseFrame.hover
	for _, z := range m.mouseFrame.zones {
		if spot.line != z.line || spot.column < z.lo || spot.column >= z.hi {
			continue
		}
		if z.line < len(lines) && z.hi > z.lo {
			lines[z.line] = mouseHighlight(lines[z.line], z.lo, z.hi, cFG)
		}
		break
	}
	return lines
}

// mouseHighlight paints the cell range [lo,hi) with st. When the highlight
// turns off it re-emits the foreground color that was active when it turned on
// (falling back to cReset when the base was default), so a range painted over
// dim or colored text hands the rest of the line back to its own styling.
func mouseHighlight(s string, lo, hi int, st string) string {
	var b strings.Builder
	column, on := 0, false
	zoneFG, lastFG := "", ""
	for i := 0; i < len(s); {
		if s[i] == '\x1b' {
			e := ansiEscapeEnd(s, i)
			seq := s[i:e]
			b.WriteString(seq)
			if seq == cReset {
				lastFG = ""
			} else if fg := sgrForeground(seq); fg != "" {
				lastFG = fg
			}
			i = e
			continue
		}
		if !on && column >= lo {
			b.WriteString(st)
			zoneFG = lastFG
			on = true
		}
		if on && column >= hi {
			if zoneFG != "" {
				b.WriteString(zoneFG)
			} else {
				b.WriteString(cReset)
			}
			on = false
		}
		r, n := utf8.DecodeRuneInString(s[i:])
		b.WriteString(s[i : i+n])
		column += runewidth.RuneWidth(r)
		i += n
	}
	if on {
		b.WriteString(cReset)
	}
	return b.String()
}

// sgrForeground returns seq itself when it sets the foreground color (30-37,
// 90-97, 38;5;n or 38;2;r;g;b) and "" for any other SGR.
func sgrForeground(seq string) string {
	if len(seq) < 4 || seq[0] != '\x1b' || seq[1] != '[' || seq[len(seq)-1] != 'm' {
		return ""
	}
	params := strings.Split(seq[2:len(seq)-1], ";")
	if len(params) == 0 {
		return ""
	}
	code := params[0]
	if code == "38" {
		if len(params) >= 2 && params[1] == "5" {
			return seq
		}
		if len(params) == 5 && params[1] == "2" {
			return seq
		}
		return ""
	}
	if len(code) == 2 && code[0] == '3' || len(code) == 2 && code[0] == '9' {
		return seq
	}
	return ""
}
