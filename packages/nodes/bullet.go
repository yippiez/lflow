package nodes

import (
	"github.com/lflow/lflow/packages/database"
)

// The bullet node: the plain default row — no glyph override, no special
// behavior. Every other type is a deliberate departure from this one.
func init() {
	Register(Plugin{
		Key:            database.TypeBullets,
		Label:          "Bullet",
		InlineEditable: true,
	})
}
