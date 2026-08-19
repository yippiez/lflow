package editor

import "github.com/lflow/lflow/tui/database"

// Result is a vanity marker node: just the ⚑ glyph and nothing else.
func init() {
	registerType(nodeType{
		key:            database.TypeResult,
		label:          "Result",
		inlineEditable: true,
		glyph: func(*item) (string, string) {
			return "⚑", ""
		},
	})
}
