package editor

import "github.com/lflow/lflow/packages/database"

// The class node: a type container as a first-class citizen — a Python class,
// a Rust struct/enum/trait/impl/mod. Its text is the header without braces or
// colon (Rust keeps its own container keyword: `struct Point`); members are
// the children. Each codec restores the syntax on save.
func init() {
	registerType(nodeType{
		key:            database.TypeClass,
		label:          "Class",
		inlineEditable: true,
		glyph: func(*item) (string, string) {
			return "◇", cCyan
		},
		bodyTail: codeBodyTail,
	})
}
