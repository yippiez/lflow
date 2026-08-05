package editor

import "github.com/lflow/lflow/packages/database"

// The bullet node: the plain default row — no glyph override, no special
// behavior. Every other type is a deliberate departure from this one.
func init() {
	registerType(nodeType{
		key:            database.TypeBullets,
		label:          "Bullet",
		inlineEditable: true,
	})
}
