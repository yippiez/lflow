package editor

import "github.com/lflow/lflow/tui/database"

// The todo node: a checkbox row — ■ once completed, □ while open, both dim —
// and Enter continues the list (a fresh sibling stays a todo instead of
// landing as a bullet).
func init() {
	registerType(nodeType{
		key:             database.TypeTodo,
		label:           "Todo",
		inlineEditable:  true,
		continueOnEnter: true,
		glyph:           todoGlyph,
	})
}

func todoGlyph(it *item) (string, string) {
	if it.completedAt > 0 {
		return "■", cDim
	}
	return "□", cDim
}
