package editor

import "github.com/lflow/lflow/packages/database"

// A divider renders as a full-width rule, hiding the glyph; an optional text
// runs on the midpoint of the rule, edited like any node. It is otherwise a
// normal node: it nests, moves, takes a /note, and is removed with ctrl+d.
// The rule rendering itself stays central in the editor.
func init() {
	registerType(nodeType{
		key:            database.TypeDivider,
		label:          "Divider",
		inlineEditable: true,
	})
}
