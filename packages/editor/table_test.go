package editor

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lflow/lflow/packages/database"
)

// newTableModel builds a model whose first row is a table node laid out as
// columns × cells: cols[i][0] is the header, the rest are that column's cells.
func newTableModel(width int, cols ...[]string) (*Model, *item) {
	root := &item{}
	tr := &tree{root: root, byUUID: map[string]*item{}, externalNames: map[string]string{}}
	tbl := &item{uuid: "tbl", name: "sprint", typ: database.TypeTable, parent: root, collapsed: true}
	tr.byUUID["tbl"] = tbl
	for ci, col := range cols {
		c := &item{uuid: string(rune('a'+ci)) + "col", name: col[0], parent: tbl}
		tr.byUUID[c.uuid] = c
		for ri, cell := range col[1:] {
			cl := &item{uuid: c.uuid + string(rune('0'+ri)), name: cell, parent: c}
			tr.byUUID[cl.uuid] = cl
			c.children = append(c.children, cl)
		}
		tbl.children = append(tbl.children, c)
	}
	root.children = append(root.children, tbl)
	m := &Model{tree: tr, viewStack: []*item{root}, width: width, height: 24}
	m.refreshRows()
	return m, tbl
}

// TestTableReadsColumnsThenRows pins the whole structural rule: the table's
// children are the columns and their children are the rows, so row n is the nth
// child of every column.
func TestTableReadsColumnsThenRows(t *testing.T) {
	m, tbl := newTableModel(80,
		[]string{"task", "ship it", "write docs"},
		[]string{"owner", "ada"}, // a ragged column: no cell in row 1
	)
	g := tableOf(m, tbl)
	if len(g.cols) != 2 {
		t.Fatalf("cols = %d, want 2 (the table's children are the columns)", len(g.cols))
	}
	if g.rows != 2 {
		t.Fatalf("rows = %d, want 2 (the longest column's cells)", g.rows)
	}
	if got := g.cellAt(m, 0, 1); got == nil || got.name != "write docs" {
		t.Errorf("cell(0,1) = %v, want the second child of the first column", got)
	}
	if got := g.cellAt(m, 1, 1); got != nil {
		t.Errorf("cell(1,1) = %q, want nil — a short column has no cell there", got.name)
	}
}

// TestTableGridDrawsHeadersAndCells is the render contract: a box with the
// column names on the header row, one row band per row, and every line the same
// width so the grid does not shear.
func TestTableGridDrawsHeadersAndCells(t *testing.T) {
	m, tbl := newTableModel(80,
		[]string{"task", "ship it", "write docs"},
		[]string{"owner", "ada", "grace"},
	)
	lines, _ := m.tableLines(tbl, 60, nil)
	joined := stripSGR(strings.Join(lines, "\n"))
	for _, want := range []string{"task", "owner", "ship it", "grace", "┌", "┼", "┘"} {
		if !strings.Contains(joined, want) {
			t.Errorf("grid missing %q:\n%s", want, joined)
		}
	}
	w := visibleWidth(lines[0])
	for i, l := range lines {
		if got := visibleWidth(l); got != w {
			t.Errorf("line %d width = %d, want %d (the grid must not shear)\n%s", i, got, w, joined)
		}
	}
	if w > 60 {
		t.Errorf("grid width = %d, want <= 60", w)
	}
}

// TestTableCellShowsItsOwnOutline: a cell is a node, so the outline inside it
// renders inside the box.
func TestTableCellShowsItsOwnOutline(t *testing.T) {
	m, tbl := newTableModel(80, []string{"task", "ship it"})
	cell := tableOf(m, tbl).cellAt(m, 0, 0)
	cell.children = []*item{{name: "cut the tag", parent: cell}}
	cell.children[0].children = []*item{{name: "changelog", parent: cell.children[0]}}

	joined := stripSGR(strings.Join(firstOf(m.tableLines(tbl, 60, nil)), "\n"))
	for _, want := range []string{"· cut the tag", "· changelog"} {
		if !strings.Contains(joined, want) {
			t.Errorf("cell outline missing %q:\n%s", want, joined)
		}
	}
}

// TestTableFaceIsTheFoldState: folded draws the grid band, open renders the
// columns as ordinary rows and no grid.
func TestTableFaceIsTheFoldState(t *testing.T) {
	m, tbl := newTableModel(80, []string{"task", "ship it"}, []string{"owner", "ada"})

	if bands := m.tableBandLines(m.rows[0], false, 79); len(bands) == 0 {
		t.Fatalf("folded table drew no grid band")
	}
	if len(m.rows) != 1 {
		t.Fatalf("folded table shows %d rows, want 1 (its columns stay hidden)", len(m.rows))
	}

	m.toggleTableFace(tbl)
	if bands := m.tableBandLines(m.rows[0], false, 79); bands != nil {
		t.Errorf("open table still drew a grid band: %v", bands)
	}
	if len(m.rows) != 5 { // the table, its 2 columns, their 2 cells
		t.Errorf("nodes face shows %d rows, want 4 (columns and cells as plain nodes)", len(m.rows))
	}
}

// TestTableConversionMovesNothing: /type Table is a reading, so converting a
// plain node keeps its subtree byte for byte — and the nodes face shows exactly
// what was there before.
func TestTableConversionMovesNothing(t *testing.T) {
	m, tbl := newTableModel(80, []string{"task", "ship it"}, []string{"owner", "ada"})
	tbl.typ = database.TypeBullets
	tbl.collapsed = false
	m.refreshRows()
	before := len(m.rows)

	tableOnType(m, tbl)
	tbl.typ = database.TypeTable
	if !tableFaceGrid(tbl) {
		t.Errorf("converted table did not land on its grid face")
	}
	if g := tableOf(m, tbl); len(g.cols) != 2 || g.rows != 1 {
		t.Errorf("grid = %d × %d, want 2 × 1 — conversion must not move nodes", len(g.cols), g.rows)
	}
	m.toggleTableFace(tbl)
	if got := len(m.rows); got != before {
		t.Errorf("nodes face shows %d rows, want the %d it had before the conversion", got, before)
	}
}

// TestTableViewTypingEditsTheCell: the grid editor edits the real cell node, so
// the edit is an ordinary node edit that syncs with everything else.
func TestTableViewTypingEditsTheCell(t *testing.T) {
	m, tbl := newTableModel(80, []string{"task", ""}, []string{"owner", ""})
	v := tableView{}
	if !v.Enter(m, tbl) {
		t.Fatalf("Enter declined a table with columns")
	}
	// Enter opens on the header; step down into the first cell and type.
	v.Key(m, tbl, key("down"))
	for _, r := range "ship" {
		v.Key(m, tbl, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	if got := tableOf(m, tbl).cellAt(m, 0, 0).name; got != "ship" {
		t.Errorf("cell text = %q, want %q", got, "ship")
	}
	if !m.unsaved {
		t.Errorf("editing a cell did not mark the tree unsaved")
	}
	// tab crosses into the next column, typing lands there
	v.Key(m, tbl, tea.KeyMsg{Type: tea.KeyTab})
	v.Key(m, tbl, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})
	if got := tableOf(m, tbl).cellAt(m, 1, 0).name; got != "a" {
		t.Errorf("second column cell = %q, want %q", got, "a")
	}
}

// TestTableAddRowKeepsTheGridRectangular: a row is a slice across the columns,
// so ⏎ adds one cell to every column at the same index.
func TestTableAddRowKeepsTheGridRectangular(t *testing.T) {
	m, tbl := newTableModel(80, []string{"task", "ship it"}, []string{"owner", "ada"})
	v := tableView{}
	v.Enter(m, tbl)
	v.Key(m, tbl, key("down")) // onto row 0
	v.Key(m, tbl, tea.KeyMsg{Type: tea.KeyEnter})

	g := tableOf(m, tbl)
	if g.rows != 2 {
		t.Fatalf("rows = %d, want 2 after ⏎", g.rows)
	}
	for i := range g.cols {
		if c := g.cellAt(m, i, 1); c == nil {
			t.Errorf("column %d has no cell in the new row", i)
		} else if c.name != "" {
			t.Errorf("new cell in column %d = %q, want empty", i, c.name)
		}
	}
	if s := tableSelOf(m, tbl); s.row != 1 {
		t.Errorf("selection row = %d, want the new row 1", s.row)
	}
}

// TestTableAddColumnAppends: ⌥n adds a column at the RIGHT edge — a column's
// position is the grid's reading order, not a priority inbox.
func TestTableAddColumnAppends(t *testing.T) {
	m, tbl := newTableModel(80, []string{"task", "ship it"})
	tbl.priority = database.PriorityUp
	v := tableView{}
	v.Enter(m, tbl)
	v.Key(m, tbl, tea.KeyMsg{Type: tea.KeyRunes, Alt: true, Runes: []rune("n")})

	g := tableOf(m, tbl)
	if len(g.cols) != 2 {
		t.Fatalf("cols = %d, want 2", len(g.cols))
	}
	if g.cols[0].name != "task" {
		t.Errorf("first column = %q, want the existing %q — new columns append", g.cols[0].name, "task")
	}
	if s := tableSelOf(m, tbl); s.col != 1 || s.row != -1 {
		t.Errorf("selection = col %d row %d, want the new column's header (1, -1)", s.col, s.row)
	}
}

// TestTableDropRowArmsBeforeDeleting: a row carries its cells' whole subtrees,
// and the grid editor has no undo — so a row with content needs a second ⌥d.
func TestTableDropRowArmsBeforeDeleting(t *testing.T) {
	m, tbl := newTableModel(80, []string{"task", "ship it"}, []string{"owner", "ada"})
	v := tableView{}
	v.Enter(m, tbl)
	v.Key(m, tbl, key("down"))

	altD := tea.KeyMsg{Type: tea.KeyRunes, Alt: true, Runes: []rune("d")}
	v.Key(m, tbl, altD)
	if g := tableOf(m, tbl); g.rows != 1 {
		t.Fatalf("first ⌥d deleted a non-empty row (rows = %d)", g.rows)
	}
	v.Key(m, tbl, altD)
	if g := tableOf(m, tbl); g.rows != 0 {
		t.Errorf("second ⌥d did not delete the row (rows = %d)", g.rows)
	}
}

// TestTableSeedsAnEmptyTable: /type Table on a leaf then alt+e gives something
// to type into rather than an empty box.
func TestTableSeedsAnEmptyTable(t *testing.T) {
	m, _ := newTableModel(80)
	leaf := &item{uuid: "leaf", typ: database.TypeTable, parent: m.tree.root}
	m.tree.byUUID["leaf"] = leaf
	m.tree.root.children = append(m.tree.root.children, leaf)

	if !(tableView{}).Enter(m, leaf) {
		t.Fatalf("Enter declined an empty table")
	}
	g := tableOf(m, leaf)
	if len(g.cols) != 2 || g.rows != 1 {
		t.Errorf("seeded grid = %d × %d, want 2 × 1", len(g.cols), g.rows)
	}
	if !leaf.collapsed {
		t.Errorf("the grid editor did not fold the table to its grid face")
	}
}

// TestTableContextIsAPipeTable: the shape is the meaning, so structured context
// ships rows, not a bullet list.
func TestTableContextIsAPipeTable(t *testing.T) {
	m, tbl := newTableModel(80,
		[]string{"task", "ship it"},
		[]string{"owner", "ada"},
	)
	ctx := tableToContext(m, tbl)
	if ctx.tag != "table" {
		t.Errorf("tag = %q, want table", ctx.tag)
	}
	if !strings.Contains(ctx.attrs, `cols="2"`) || !strings.Contains(ctx.attrs, `rows="1"`) {
		t.Errorf("attrs = %q, want the grid shape", ctx.attrs)
	}
	want := "| task | owner |\n| --- | --- |\n| ship it | ada |"
	if ctx.body != want {
		t.Errorf("body =\n%s\nwant\n%s", ctx.body, want)
	}
}

// TestTableRefusesChipCellsInline: a chip renders collapsed, so a caret index in
// the grid is not an index into the stored text — those cells are edited in the
// nodes face instead of being silently mangled.
func TestTableRefusesChipCellsInline(t *testing.T) {
	m, tbl := newTableModel(80, []string{"task", ""})
	cell := tableOf(m, tbl).cellAt(m, 0, 0)
	m.chips = map[string]database.Chip{"c1": {ID: "c1", Kind: chipKindTag, Value: "release"}}
	cell.name = chipAnchor("c1")
	before := cell.name

	v := tableView{}
	v.Enter(m, tbl)
	v.Key(m, tbl, key("down"))
	v.Key(m, tbl, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})

	if cell.name != before {
		t.Errorf("chip cell was edited in the grid: %q → %q", before, cell.name)
	}
	if m.flash == "" {
		t.Errorf("refusing the edit said nothing in the status bar")
	}
}

func firstOf(lines []string, _ int) []string { return lines }

// TestTableTypingMaterializesARaggedCell: columns are ragged in the outline but
// square on screen, so typing into a slot that has no node yet creates it (and
// any empty cells above it) instead of dropping the keystroke.
func TestTableTypingMaterializesARaggedCell(t *testing.T) {
	m, tbl := newTableModel(80, []string{"task", "ship it", "docs"}, []string{"notes"})
	v := tableView{}
	v.Enter(m, tbl)
	v.Key(m, tbl, tea.KeyMsg{Type: tea.KeyTab}) // onto the "notes" header
	v.Key(m, tbl, key("down"))                  // row 1 of a column with no cells
	v.Key(m, tbl, key("down"))
	for _, r := range "one grid" {
		v.Key(m, tbl, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	g := tableOf(m, tbl)
	if c := g.cellAt(m, 1, 1); c == nil || c.name != "one grid" {
		t.Fatalf("cell(1,1) = %v, want the materialized %q", c, "one grid")
	}
	if c := g.cellAt(m, 1, 0); c == nil || c.name != "" {
		t.Errorf("cell(1,0) = %v, want an empty filler cell above it", c)
	}
	if got := g.cols[1].name; got != "notes" {
		t.Errorf("column header = %q, want %q — typing must not leak into the header", got, "notes")
	}
}

// TestTableFocusedCellKeepsItsFirstRune: the block caret parks past the last
// rune, so a column sized flush to its widest cell would scroll that cell
// sideways the moment the cursor landed on it — dropping its first character.
func TestTableFocusedCellKeepsItsFirstRune(t *testing.T) {
	m, tbl := newTableModel(80, []string{"fruit", "apples"}, []string{"dairy", "milk"})
	v := tableView{}
	v.Enter(m, tbl) // opens on the header, caret at the end of "fruit"

	lines, _ := m.tableLines(tbl, 60, &tableSel{col: 0, row: -1, caret: len("fruit")})
	joined := stripSGR(strings.Join(lines, "\n"))
	if !strings.Contains(joined, "fruit") {
		t.Errorf("focused header lost a rune:\n%s", joined)
	}
	// and the same for the widest cell in the column
	lines, _ = m.tableLines(tbl, 60, &tableSel{col: 0, row: 0, caret: len("apples")})
	if joined = stripSGR(strings.Join(lines, "\n")); !strings.Contains(joined, "apples") {
		t.Errorf("focused cell lost a rune:\n%s", joined)
	}
}
