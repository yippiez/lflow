package editor

import "github.com/lflow/lflow/tui/database"

// The quote node: a block-quote row, inline-editable, no other behavior of
// its own.
func init() {
	registerType(nodeType{
		key:            database.TypeQuote,
		label:          "Quote",
		inlineEditable: true,
	})
}
