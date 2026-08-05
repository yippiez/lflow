package nodes

import (
	"github.com/lflow/lflow/packages/database"
)

// Pir: the magic keywords' one home. "ultracode" and "ultraloop" shine on
// this row and on no other, so the animation reads as a marked instruction
// rather than as any note that happens to contain the word. Nothing else yet
// — the node's own behaviour is still to be written, and an ordinary editable
// row is the right placeholder for one.
func init() {
	Register(Plugin{
		Key:            database.TypePir,
		Label:          "Pir",
		InlineEditable: true,
	})
}
