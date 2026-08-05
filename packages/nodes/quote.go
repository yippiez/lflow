package nodes

import (
	"github.com/lflow/lflow/packages/database"
)

// The quote node: a block-quote row, inline-editable, no other behavior of
// its own.
func init() {
	Register(Plugin{
		Key:            database.TypeQuote,
		Label:          "Quote",
		InlineEditable: true,
	})
}
