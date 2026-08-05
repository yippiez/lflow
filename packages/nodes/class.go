package nodes

import (
	"github.com/lflow/lflow/packages/database"
	"github.com/lflow/lflow/packages/editor"
)

// The class node: a type container as a first-class citizen — a Python class,
// a Rust struct/enum/trait/impl/mod. Its text is the header without braces or
// colon (Rust keeps its own container keyword: `struct Point`); members are
// the children. Each codec restores the syntax on save.
func init() {
	editor.RegisterNodePlugin(editor.NodePlugin{
		Key:            database.TypeClass,
		Label:          "Class",
		InlineEditable: true,
		Glyph:          func() (string, string) { return "◇", editor.NodeTheme().Cyan },
		BodyTail:       editor.NodeCodeBodyTail,
	})
}
