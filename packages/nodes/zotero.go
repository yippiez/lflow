package nodes

import "github.com/lflow/lflow/packages/database"

// a mirrored Zotero entry (see zoteroitem.go): the paper itself in the tree,
// its tags on the title row and its attachments, annotations and notes as
// locked children. Never inline-editable — the subtree is Zotero's, and
// alt+r re-reads it. Every node of the mirror wears this type; the binding
// in zotero_nodes says which Zotero object each one stands for.
//
// Every hook stays in the editor's attachment (see zoteroitem.go's init):
// glyph, run, flashActions and bodyTail all read zoteroBindingFor, which
// resolves against the editor-private zoteroBindings map (and, for bodyTail,
// the node's raw style/collapsed state) — none of that is reachable from a
// structure-only Ref.
func init() {
	Register(Plugin{
		Key:            database.TypeZotero,
		Label:          "Zotero",
		InlineEditable: false,
	})
}
