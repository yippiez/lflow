package editor

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mattn/go-runewidth"

	"github.com/lflow/lflow/pkg/tui/database"
)

// The Table node is a GRID READING of an ordinary subtree — not a new storage
// shape, not a per-table column set, not one node holding rows of text:
//
//	table            the node typed /table
//	├─ column        a first-level child IS a column; its text is the header
//	│  ├─ cell       a column's child IS the cell in that row (row n = nth child)
//	│  │  └─ …       a cell's own children are the outline INSIDE that cell
//	│  └─ cell
//	└─ column
//
// Because it is only a reading, ANY node converts: /type Table moves nothing and
// loses nothing, and every node under it stays an ordinary node that indents,
// mirrors, takes a note and syncs as before. The columns keep whatever types
// they had — a cell whose type carries no plain body (a divider, an image, a
// voice clip) stands in with its type name rather than an empty box, which is
// the whole "which children are allowed in a table" rule: all of them are, and
// the ones that are not text say what they are.
//
// The two faces:
//
//	grid face   the node is FOLDED — the grid draws as a band beneath its row
//	            and the columns/cells stay hidden, like any folded subtree
//	nodes face  the node is OPEN — the same content as a plain outline
//
// The face IS the fold state, so ctrl+space / alt+↑ / alt+↓ toggle table ⇄ nodes
// with no extra key and no extra state, and the choice persists like any fold.
// alt+e opens the grid editor (cell cursor, typing edits the cell, ⏎ new row,
// ⌥n new column, ⌥→ opens a cell's own outline).

const (
	glyphTable = "▦"
	// a column grows to its content, capped here, and is squeezed no narrower
	// than tableColMin when the whole grid has to fit the terminal.
	tableColMax = 28
	tableColMin = 4
	// how many lines of a cell's inner outline show inside the box before the
	// rest collapses into a "⋯ n more" line.
	tableCellSub = 4
)

// tableFaceGrid reports the grid face: a folded table draws its grid, an open
// one is the plain outline.
func tableFaceGrid(it *item) bool { return it != nil && it.collapsed }

// tableGlyph marks a table row; the grid face lights the glyph accent so the two
// faces read apart at a glance.
func tableGlyph(it *item) (string, string) {
	if tableFaceGrid(it) {
		return glyphTable, cAccent
	}
	return glyphTable, cDim
}

// setTableFace folds (grid) or opens (nodes) a table, persisting the fold like
// any other collapse.
func (m *Model) setTableFace(it *item, grid bool) {
	if it == nil || it.collapsed == grid {
		return
	}
	it.collapsed = grid
	m.persistCollapsed(it)
	m.refreshRows()
}

func (m *Model) toggleTableFace(it *item) { m.setTableFace(it, !tableFaceGrid(it)) }

// tableOnType lands a node freshly converted with /type on its grid face: the
// columns and rows it already had ARE the table, so the grid is the answer to
// "make this a table" — the nodes face is one alt+↓ away.
func tableOnType(m *Model, it *item) { m.setTableFace(it, true) }

// tableFlashActions names the table's own alt+s actions: the face toggle reads
// as the face you would get, and "edit" opens the grid editor.
func tableFlashActions(m *Model, it *item) []flashAction {
	verb := "table"
	if tableFaceGrid(it) {
		verb = "nodes"
	}
	return []flashAction{
		{verb: verb, color: cCyan, do: func(m *Model, it *item) tea.Cmd { m.toggleTableFace(it); return nil }},
		{verb: "edit", color: cYellow, do: flashExpandDo},
	}
}

// ── the grid reading ────────────────────────────────────────────────────────

// tableGrid is a table node's subtree read as columns × rows.
type tableGrid struct {
	cols []*item // the table's children, left to right
	rows int     // the longest column's cell count — the grid is ragged, drawn square
}

// tableKids is the child list a table reading walks. It goes through the tree so
// a mirrored table shows the source's live columns; a nil Model (tests, the
// bodyTail-style pure hooks) falls back to the item's own children.
func tableKids(m *Model, it *item) []*item {
	if it == nil {
		return nil
	}
	if m != nil && m.tree != nil {
		return m.tree.childItems(it)
	}
	return it.children
}

// tableOf reads the grid off a table node.
func tableOf(m *Model, it *item) tableGrid {
	var g tableGrid
	for _, c := range tableKids(m, it) {
		g.cols = append(g.cols, c)
		if n := len(tableKids(m, c)); n > g.rows {
			g.rows = n
		}
	}
	return g
}

// cellAt returns the node in a grid slot, or nil where a short column has no
// cell for that row (the grid is ragged in the outline, square on screen).
func (g tableGrid) cellAt(m *Model, col, rowIdx int) *item {
	if col < 0 || col >= len(g.cols) {
		return nil
	}
	kids := tableKids(m, g.cols[col])
	if rowIdx < 0 || rowIdx >= len(kids) {
		return nil
	}
	return kids[rowIdx]
}

// tableCellText is a cell's PLAIN editable body — chip anchors resolved, one
// line. It is what the caret edits, so it carries no decoration.
func (m *Model) tableCellText(it *item) string {
	if it == nil {
		return ""
	}
	name := it.name
	if m != nil && m.tree != nil {
		name = displayAnchors(m.tree.displayName(it), m.chips)
	}
	return oneLine(stripControlBytes(name))
}

// tableCellDisplay is how an unfocused cell reads: the plain text plus the
// decoration its type carries. A type with no plain body stands in with its own
// name — an image cell says "image" instead of leaving the box blank.
func (m *Model) tableCellDisplay(it *item) string {
	if it == nil {
		return ""
	}
	switch it.typ {
	case database.TypeDivider:
		return "───"
	case database.TypeImage:
		return "image"
	case database.TypeVoice:
		return "voice"
	}
	text := m.tableCellText(it)
	if it.typ == database.TypeTodo {
		if it.completedAt > 0 {
			return glyphTodoDone + " " + text
		}
		return glyphTodo + " " + text
	}
	return text
}

// tableCellEditable reports whether the grid may edit a cell in place. A cell
// holding chips is refused: the chip renders collapsed, so a caret index into the
// displayed text would not be an index into the stored text. Those cells are
// edited in the nodes face, where the normal chip machinery runs.
func (m *Model) tableCellEditable(it *item) bool {
	if it == nil || it.readonly || it.mirrorOf != "" || !typeOf(it.typ).inlineEditable {
		return false
	}
	return m.tableCellText(it) == oneLine(stripControlBytes(it.name))
}

// tableSubLines renders the outline INSIDE a cell: one dim line per descendant,
// indented per level, so a cell that holds a subtree shows it in its box.
func (m *Model) tableSubLines(c *item) []string {
	var out []string
	var walk func(n *item, depth int)
	walk = func(n *item, depth int) {
		for _, k := range tableKids(m, n) {
			out = append(out, strings.Repeat("  ", depth)+"· "+m.tableCellDisplay(k))
			walk(k, depth+1)
		}
	}
	walk(c, 0)
	if len(out) > tableCellSub {
		rest := len(out) - tableCellSub
		out = append(out[:tableCellSub:tableCellSub], fmt.Sprintf("⋯ %d more", rest))
	}
	return out
}

// ── layout ──────────────────────────────────────────────────────────────────

// tableColWidths sizes the columns: each takes its natural content width (capped
// at tableColMax), then the widest are shaved a cell at a time until the whole
// grid fits `avail` display columns, never below tableColMin.
func (m *Model) tableColWidths(g tableGrid, avail int) []int {
	w := make([]int, len(g.cols))
	for i, col := range g.cols {
		w[i] = runewidth.StringWidth(m.tableHeaderText(col, i))
		for r := 0; r < g.rows; r++ {
			c := g.cellAt(m, i, r)
			lines := append([]string{m.tableCellDisplay(c)}, m.tableSubLines(c)...)
			for _, l := range lines {
				if lw := runewidth.StringWidth(l); lw > w[i] {
					w[i] = lw
				}
			}
		}
		// one cell more than the content: the block caret parks PAST the last
		// rune, and a box flush with its text would scroll the focused cell
		// sideways to make room — the widest cell in a column losing its first
		// character the moment the cursor lands on it.
		w[i]++
		if w[i] > tableColMax {
			w[i] = tableColMax
		}
		if w[i] < tableColMin {
			w[i] = tableColMin
		}
	}
	// each column costs "│ " + content + " "; the closing "│" costs one more
	budget := avail - (3*len(w) + 1)
	for budget > 0 {
		total := 0
		for _, x := range w {
			total += x
		}
		if total <= budget {
			break
		}
		widest, idx := 0, -1
		for i, x := range w {
			if x > widest && x > tableColMin {
				widest, idx = x, i
			}
		}
		if idx < 0 {
			break // everything is at the floor; the band clips what is left
		}
		w[idx]--
	}
	return w
}

// tableHeaderText is a column's header: its own text, or a dim-worthy
// placeholder naming its position while it is still empty.
func (m *Model) tableHeaderText(col *item, idx int) string {
	if t := m.tableCellText(col); t != "" {
		return t
	}
	return fmt.Sprintf("column %d", idx+1)
}

// tableSel is the focused cell: its column, its row (-1 is the header row) and
// the caret inside its text.
type tableSel struct{ col, row, caret int }

// tableLines paints the grid. sel == nil renders it read-only (the always-on
// band beneath a folded table); a sel draws the block caret in that cell (the
// focused editor). selLine is the content index of the selected cell's first
// line so the caller can scroll it into view.
func (m *Model) tableLines(it *item, width int, sel *tableSel) (lines []string, selLine int) {
	g := tableOf(m, it)
	if len(g.cols) == 0 {
		return []string{cDim + "  empty table · ⌥e opens the grid, ⌥n adds a column" + cReset}, 0
	}
	if width < 8 {
		width = 8
	}
	w := m.tableColWidths(g, width)

	rule := func(l, mid, r string) string {
		var b strings.Builder
		b.WriteString(cDim + l)
		for i, cw := range w {
			if i > 0 {
				b.WriteString(mid)
			}
			b.WriteString(strings.Repeat("─", cw+2))
		}
		b.WriteString(r + cReset)
		return b.String()
	}

	// rowLines lays one grid row out: every column's content side by side, the
	// row as tall as its tallest cell. rowIdx -1 is the header row.
	rowLines := func(rowIdx int) []string {
		cells := make([][]string, len(g.cols))
		height := 0
		for i := range g.cols {
			c := g.cols[i]
			if rowIdx >= 0 {
				c = g.cellAt(m, i, rowIdx)
			}
			focused := sel != nil && sel.col == i && sel.row == rowIdx
			cells[i] = m.tableCellLines(c, i, rowIdx, w[i], focused, sel)
			if len(cells[i]) > height {
				height = len(cells[i])
			}
		}
		out := make([]string, height)
		for li := 0; li < height; li++ {
			var b strings.Builder
			for i := range g.cols {
				b.WriteString(cDim + "│" + cReset + " ")
				if li < len(cells[i]) {
					b.WriteString(cells[i][li])
				} else {
					b.WriteString(strings.Repeat(" ", w[i]))
				}
				b.WriteString(" ")
			}
			b.WriteString(cDim + "│" + cReset)
			out[li] = b.String()
		}
		return out
	}

	lines = append(lines, rule("┌", "┬", "┐"))
	if sel != nil && sel.row < 0 {
		selLine = len(lines)
	}
	lines = append(lines, rowLines(-1)...)
	for r := 0; r < g.rows; r++ {
		lines = append(lines, rule("├", "┼", "┤"))
		if sel != nil && sel.row == r {
			selLine = len(lines)
		}
		lines = append(lines, rowLines(r)...)
	}
	lines = append(lines, rule("└", "┴", "┘"))
	return lines, selLine
}

// tableCellLines renders one cell's content to exactly w display columns per
// line: its text (bold in the header row), then the dim outline it holds. The
// focused cell is drawn on ONE line with the block caret, horizontally scrolled
// so the caret is always in the box — wrapping there would break the mapping
// from caret index to screen cell.
func (m *Model) tableCellLines(c *item, col, rowIdx, w int, focused bool, sel *tableSel) []string {
	base, text := cFG, ""
	if rowIdx < 0 {
		base = cFG + cBold
		text = m.tableHeaderText(c, col)
		if m.tableCellText(c) == "" {
			base = cDim // the "column n" placeholder is not content
		}
	} else {
		text = m.tableCellDisplay(c)
	}
	var out []string
	if focused && sel != nil {
		// the caret edits the PLAIN text — a header edits its column node's own
		// text, never the "column n" placeholder standing in for it
		out = append(out, tableCaretLine(m.tableCellText(c), sel.caret, w))
	} else {
		segs := wrapPlain(text, w)
		if len(segs) == 0 {
			segs = []string{""}
		}
		for _, s := range segs {
			out = append(out, tablePad(base+s+cReset, runewidth.StringWidth(s), w))
		}
	}
	// only a CELL shows the outline it holds — a column's children are its cells,
	// which are the rows below, not content of the header box.
	if rowIdx >= 0 {
		for _, s := range m.tableSubLines(c) {
			s = elideMiddle(s, w)
			out = append(out, tablePad(cDim+s+cReset, runewidth.StringWidth(s), w))
		}
	}
	return out
}

// tablePad pads a styled cell line (whose visible width is vis) out to w cells.
func tablePad(styled string, vis, w int) string {
	if vis > w {
		return clip(styled, w)
	}
	return styled + strings.Repeat(" ", w-vis)
}

// tableCaretLine draws the focused cell's text on one line with the block cursor
// on the caret cell, scrolled horizontally so the caret stays inside the box.
func tableCaretLine(text string, caret, w int) string {
	r := []rune(text)
	if caret < 0 {
		caret = 0
	}
	if caret > len(r) {
		caret = len(r)
	}
	start := 0
	if caret >= w {
		start = caret - w + 1
	}
	var b strings.Builder
	b.WriteString(cFG)
	vis := 0
	for i := start; i < len(r); i++ {
		rw := runewidth.RuneWidth(r[i])
		if vis+rw > w {
			break
		}
		if i == caret {
			b.WriteString(cInvert + string(r[i]) + cReset + cFG)
		} else {
			b.WriteString(string(r[i]))
		}
		vis += rw
	}
	if caret >= len(r) && vis < w {
		b.WriteString(cInvert + " " + cReset + cFG)
		vis++
	}
	b.WriteString(cReset)
	return tablePad(b.String(), vis, w)
}

// tableBandLines hangs the grid beneath a folded table node — the always-on
// face, drawn like the note and run-output bands, in the outline flow.
func (m *Model) tableBandLines(r row, below bool, maxLine int) []string {
	if !tableFaceGrid(r.it) {
		return nil // the nodes face: the columns render as ordinary rows
	}
	if m.quitting {
		// the final scrollback frame is the whole outline FULLY EXPANDED — every
		// fold opens there, this one included — so a grid above the same columns
		// and cells would print the table twice.
		return nil
	}
	rail := continuationPrefix(r, below)
	lines, _ := m.tableLines(m.renderItem(r.it), maxLine-visibleWidth(rail), nil)
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = clip(rail+cReset+l, maxLine)
	}
	return out
}

// ── the grid editor (alt+e) ─────────────────────────────────────────────────

// tableView is the table's inline expanded view: the same grid, with a cell
// cursor. It is stateless like every nodeView — the selection lives in the
// ephemeral per-node store, the cell text lives in the cells themselves, so an
// edit here is an edit to a real node and flushes with everything else.
type tableView struct{}

func tableSelOf(m *Model, it *item) tableSel {
	d := m.nodeStore(it.uuid)
	col, _ := d["tblCol"].(int)
	row, ok := d["tblRow"].(int)
	if !ok {
		row = -1 // a fresh table opens on its header row
	}
	caret, _ := d["tblCaret"].(int)
	return tableSel{col: col, row: row, caret: caret}
}

func tableSetSel(m *Model, it *item, s tableSel) {
	d := m.nodeStore(it.uuid)
	d["tblCol"], d["tblRow"], d["tblCaret"] = s.col, s.row, s.caret
}

// clampSel holds the selection inside the grid and the caret inside its cell.
func (m *Model) tableClampSel(it *item, s *tableSel) {
	g := tableOf(m, it)
	if s.col >= len(g.cols) {
		s.col = len(g.cols) - 1
	}
	if s.col < 0 {
		s.col = 0
	}
	if s.row >= g.rows {
		s.row = g.rows - 1
	}
	if s.row < -1 {
		s.row = -1
	}
	if n := len([]rune(m.tableCellText(m.tableCellFor(it, *s)))); s.caret > n {
		s.caret = n
	}
	if s.caret < 0 {
		s.caret = 0
	}
}

// tableCellFor resolves the selected node: the column node on the header row,
// else the cell (nil where a short column has none yet).
func (m *Model) tableCellFor(it *item, s tableSel) *item {
	g := tableOf(m, it)
	if s.col < 0 || s.col >= len(g.cols) {
		return nil
	}
	if s.row < 0 {
		return g.cols[s.col]
	}
	return g.cellAt(m, s.col, s.row)
}

// Enter opens the grid editor: an empty table is seeded with something to type
// into, and the table always folds to its grid face first — editing the grid
// while the same nodes also render as rows below it would draw them twice.
func (v tableView) Enter(m *Model, it *item) bool {
	if it == nil {
		return false
	}
	if len(tableKids(m, it)) == 0 {
		m.tableSeed(it)
	}
	m.setTableFace(it, true)
	s := tableSelOf(m, it)
	m.tableClampSel(it, &s)
	s.caret = len([]rune(m.tableCellText(m.tableCellFor(it, s))))
	tableSetSel(m, it, s)
	return true
}

// Leave keeps the selection for the next alt+e; cells are edited in place, so
// there is nothing to flush. The row-delete arming is dropped.
func (v tableView) Leave(m *Model, it *item) { delete(m.nodeStore(it.uuid), "tblArm") }

// Lines is the hint header plus the grid.
func (v tableView) Lines(m *Model, it *item, width int) int {
	lines, _ := m.tableLines(it, width, nil)
	return 1 + len(lines)
}

// Bands renders the hint line and the grid as bands beneath the node,
// self-windowed with the selected cell kept in view.
func (v tableView) Bands(m *Model, it *item, rail string, width, scroll, winH int, focused bool) []string {
	s := tableSelOf(m, it)
	var sel *tableSel
	if focused {
		m.tableClampSel(it, &s)
		sel = &s
	}
	lines, selLine := m.tableLines(it, width-visibleWidth(rail), sel)
	content := []string{clip(rail+cReset+m.tableHint(it), width)}
	for _, l := range lines {
		content = append(content, clip(rail+cReset+l, width))
	}
	if focused {
		if cl := selLine + 1; cl < scroll { // +1 for the hint line
			scroll = cl
		} else if cl >= scroll+winH {
			scroll = cl - winH + 1
		}
		if scroll < 0 {
			scroll = 0
		}
		m.focusScroll = scroll
	}
	return NodeWindowBands(content, scroll, winH)
}

// tableHint is the editor's header line: the shape, then the keys that act on
// the grid rather than on the outline.
func (m *Model) tableHint(it *item) string {
	g := tableOf(m, it)
	shape := fmt.Sprintf("%d × %d", len(g.cols), g.rows)
	if armed, _ := m.nodeStore(it.uuid)["tblArm"].(string); armed != "" {
		return cReset + cDim + "  table · " + shape + " · " + cReset + cRed +
			"⌥d again deletes this row" + cReset
	}
	return cReset + cDim + "  table · " + shape +
		" · ⇥ cell · ⏎ row · ⌥n column · ⌥d drop row · ⌥→ open cell · esc done" + cReset
}

// Key drives the grid: arrows and tab walk the cells, typing edits the cell
// under the cursor, and the structural keys grow the grid. Keys it does not
// claim fall through to central handling (esc, ctrl+c).
func (v tableView) Key(m *Model, it *item, k tea.KeyMsg) (tea.Cmd, bool) {
	key := k.String()
	if key != "alt+d" {
		delete(m.nodeStore(it.uuid), "tblArm") // any other key disarms the row delete
	}
	g := tableOf(m, it)
	s := tableSelOf(m, it)
	m.tableClampSel(it, &s)
	text := []rune(m.tableCellText(m.tableCellFor(it, s)))

	switch key {
	case "left":
		// inside the text first, then across to the previous column's end
		if s.caret > 0 {
			s.caret--
		} else if s.col > 0 {
			s.col--
			s.caret = len([]rune(m.tableCellText(m.tableCellFor(it, s))))
		}
	case "right":
		if s.caret < len(text) {
			s.caret++
		} else if s.col < len(g.cols)-1 {
			s.col++
			s.caret = 0
		}
	case "up":
		if s.row > -1 {
			s.row--
			s.caret = len([]rune(m.tableCellText(m.tableCellFor(it, s))))
		}
	case "down":
		if s.row < g.rows-1 {
			s.row++
			s.caret = len([]rune(m.tableCellText(m.tableCellFor(it, s))))
		}
	case "tab":
		s.col++
		if s.col >= len(g.cols) { // past the last column: wrap into the next row
			s.col = 0
			if s.row < g.rows-1 {
				s.row++
			}
		}
		s.caret = len([]rune(m.tableCellText(m.tableCellFor(it, s))))
	case "shift+tab":
		s.col--
		if s.col < 0 {
			s.col = len(g.cols) - 1
			if s.row > -1 {
				s.row--
			}
		}
		s.caret = len([]rune(m.tableCellText(m.tableCellFor(it, s))))
	case "home":
		s.caret = 0
	case "end":
		s.caret = len(text)
	case "enter":
		// a row is a slice across the columns, so a new row is one fresh cell in
		// every column at the same index
		if m.tableAddRow(it, s.row) {
			s.row++
			s.caret = 0
		}
	case "alt+n":
		if m.tableAddColumn(it) {
			s.col = len(g.cols)
			s.row, s.caret = -1, 0
		}
	case "alt+d":
		m.tableDropRow(it, &s)
	case "alt+right":
		// a cell is a node: open its own outline in the zoomed view
		if c := m.tableCellFor(it, s); c != nil {
			tableSetSel(m, it, s)
			return m.tableOpenCell(it, c), true
		}
	case "backspace":
		if s.caret > 0 && m.tableEditCell(it, s, func(r []rune) []rune {
			return append(append([]rune{}, r[:s.caret-1]...), r[s.caret:]...)
		}) {
			s.caret--
		}
	default:
		switch {
		case k.Type == tea.KeySpace && !k.Alt:
			m.tableInsert(it, &s, " ")
		case k.Type == tea.KeyRunes && !k.Alt:
			m.tableInsert(it, &s, string(k.Runes))
		default:
			return nil, false // esc, ctrl+c … → central handling
		}
	}
	tableSetSel(m, it, s)
	return nil, true
}

// tableInsert types text into the selected cell, advancing the caret.
func (m *Model) tableInsert(it *item, s *tableSel, ins string) bool {
	r := []rune(ins)
	if !m.tableEditCell(it, *s, func(cur []rune) []rune {
		out := append([]rune{}, cur[:s.caret]...)
		out = append(out, r...)
		return append(out, cur[s.caret:]...)
	}) {
		return false
	}
	s.caret += len(r)
	return true
}

// tableEditCell applies an edit to the selected cell's text. A slot with no cell
// yet is materialized first (with any gap above it), so typing into a ragged
// column simply fills it in. A cell the grid may not edit in place (a chip, a
// lock, a mirror, a bodyless type) is refused with a hint to the nodes face.
func (m *Model) tableEditCell(it *item, s tableSel, edit func([]rune) []rune) bool {
	c := m.tableEnsureCell(it, s)
	if c == nil {
		return false
	}
	if !m.tableCellEditable(c) {
		m.errorFlash("Cell is not editable here · alt+↓ for the nodes face")
		return false
	}
	c.name = string(edit([]rune(c.name)))
	m.unsaved = true
	return true
}

// tableEnsureCell returns the selected node, creating the cell (and any empty
// cells above it in that column) when the column is shorter than the row index.
func (m *Model) tableEnsureCell(it *item, s tableSel) *item {
	if c := m.tableCellFor(it, s); c != nil {
		return c
	}
	g := tableOf(m, it)
	if s.col < 0 || s.col >= len(g.cols) || s.row < 0 || m.tree == nil {
		return nil
	}
	col := m.tree.resolve(g.cols[s.col])
	if col == nil || col.structureLocked {
		return nil
	}
	for len(col.children) <= s.row {
		n, err := m.tree.newItem()
		if err != nil {
			m.err = err
			return nil
		}
		n.parent = col
		col.children = append(col.children, n)
	}
	m.unsaved = true
	m.refreshRows()
	return col.children[s.row]
}

// tableAddRow inserts a fresh cell into EVERY column just below `after` (-1 is
// the header row, so a new row lands on top), keeping the grid rectangular.
func (m *Model) tableAddRow(it *item, after int) bool {
	g := tableOf(m, it)
	if len(g.cols) == 0 || m.tree == nil {
		return false
	}
	added := false
	for _, c := range g.cols {
		col := m.tree.resolve(c)
		if col == nil || col.structureLocked {
			continue
		}
		n, err := m.tree.newItem()
		if err != nil {
			m.err = err
			return added
		}
		idx := after + 1
		if idx > len(col.children) {
			idx = len(col.children)
		}
		if idx < 0 {
			idx = 0
		}
		n.parent = col
		m.tree.insertChildAt(col, idx, n)
		added = true
	}
	if added {
		m.unsaved = true
		m.refreshRows()
	}
	return added
}

// tableAddColumn appends an empty column. It appends deliberately rather than
// honoring /priority: a column's position is the grid's left-to-right order, not
// an inbox.
func (m *Model) tableAddColumn(it *item) bool {
	if m.tree == nil {
		return false
	}
	tbl := m.tree.resolve(it)
	if tbl == nil || tbl.structureLocked {
		return false
	}
	n, err := m.tree.newItem()
	if err != nil {
		m.err = err
		return false
	}
	n.parent = tbl
	tbl.children = append(tbl.children, n)
	m.unsaved = true
	m.refreshRows()
	return true
}

// tableDropRow deletes the selected row across every column. A row that holds
// anything (text or a nested outline) needs a second alt+d — the grid editor has
// no undo of its own, and a row carries its cells' whole subtrees with it.
func (m *Model) tableDropRow(it *item, s *tableSel) {
	g := tableOf(m, it)
	if s.row < 0 || s.row >= g.rows || m.tree == nil {
		return
	}
	d := m.nodeStore(it.uuid)
	armed, _ := d["tblArm"].(string)
	arm := fmt.Sprintf("%d", s.row)
	if armed != arm && !m.tableRowEmpty(g, s.row) {
		d["tblArm"] = arm
		return
	}
	delete(d, "tblArm")
	for i := range g.cols {
		c := g.cellAt(m, i, s.row)
		if c == nil || c.structureLocked {
			continue
		}
		m.removeNodeStateUnder(c)
		m.tree.remove(c)
	}
	m.unsaved = true
	m.refreshRows()
	if g.rows-1 <= s.row { // the last row went: step back onto the one above
		s.row--
	}
	m.tableClampSel(it, s)
}

// tableRowEmpty reports a row with no text and no nested outline anywhere.
func (m *Model) tableRowEmpty(g tableGrid, rowIdx int) bool {
	for i := range g.cols {
		c := g.cellAt(m, i, rowIdx)
		if c == nil {
			continue
		}
		if strings.TrimSpace(c.name) != "" || len(tableKids(m, c)) > 0 {
			return false
		}
	}
	return true
}

// tableSeed gives a brand-new table something to type into: two empty columns
// and one empty row.
func (m *Model) tableSeed(it *item) {
	if m.tree == nil || it.structureLocked {
		return
	}
	m.tableAddColumn(it)
	m.tableAddColumn(it)
	m.tableAddRow(it, -1)
}

// tableOpenCell leaves the grid and zooms the view into a cell, so the outline
// INSIDE that cell is edited as an ordinary outline. alt+left comes back.
func (m *Model) tableOpenCell(it *item, c *item) tea.Cmd {
	tableView{}.Leave(m, it)
	m.focused = false
	target := m.tree.resolve(c)
	// push the whole path down from the current view root — table › column ›
	// cell — so the breadcrumb still says where the cell lives
	var path []*item
	for p := target; p != nil && p != m.viewRoot(); p = p.parent {
		path = append([]*item{p}, path...)
	}
	if len(path) == 0 || path[0].parent != m.viewRoot() {
		path = []*item{target} // a detached chain (a grafted mirror): just the cell
	}
	m.viewStack = append(m.viewStack, path...)
	m.cursor, m.caret = 0, 0
	m.refreshRows()
	return nil
}

// ── structured context ──────────────────────────────────────────────────────

// tableToContext ships the grid as a pipe table — the shape is the meaning here,
// and a consumer reading <table> should see rows, not a bullet list. A cell's
// inner outline rides along after a "·" so nothing in the subtree is lost.
func tableToContext(m *Model, it *item) contextXML {
	g := tableOf(m, it)
	if len(g.cols) == 0 {
		return contextXML{tag: "table"}
	}
	cellText := func(c *item) string {
		text := m.tableCellDisplay(c)
		for _, s := range m.tableSubLines(c) {
			text = strings.TrimSpace(text + " " + strings.TrimSpace(s))
		}
		return strings.ReplaceAll(text, "|", "/")
	}
	var b strings.Builder
	head := make([]string, len(g.cols))
	sep := make([]string, len(g.cols))
	for i, col := range g.cols {
		head[i] = strings.ReplaceAll(m.tableHeaderText(col, i), "|", "/")
		sep[i] = "---"
	}
	b.WriteString("| " + strings.Join(head, " | ") + " |\n")
	b.WriteString("| " + strings.Join(sep, " | ") + " |")
	for r := 0; r < g.rows; r++ {
		cells := make([]string, len(g.cols))
		for i := range g.cols {
			cells[i] = cellText(g.cellAt(m, i, r))
		}
		b.WriteString("\n| " + strings.Join(cells, " | ") + " |")
	}
	return contextXML{
		tag:   "table",
		attrs: fmt.Sprintf(`cols="%d" rows="%d"`, len(g.cols), g.rows),
		body:  b.String(),
	}
}
