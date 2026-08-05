package nodes

import "github.com/lflow/lflow/packages/database"

// a table is an ordinary subtree READ as a grid (see table.go): columns are
// the children, rows are their children, and a cell's children are the
// outline inside it. The face IS the fold state — a folded table draws its
// grid, an open one is the plain outline — so ctrl+space / alt+↑↓ toggle
// table ⇄ nodes, and alt+e opens the grid editor.
//
// Every hook stays in the editor's attachment (see table.go's init): the
// glyph reads the fold state (it.collapsed), which Ref does not expose, and
// bands/view/flashActions/onType are all Model-bound grid machinery.
func init() {
	Register(Plugin{
		Key:            database.TypeTable,
		Label:          "Table",
		InlineEditable: true,
	})
}
