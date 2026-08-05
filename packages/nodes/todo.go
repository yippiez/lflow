package nodes

import (
	"github.com/lflow/lflow/packages/database"
)

// The todo node: a checkbox row — ■ once completed, □ while open, both dim —
// and Enter continues the list (a fresh sibling stays a todo instead of
// landing as a bullet).
func init() {
	Register(Plugin{
		Key:             database.TypeTodo,
		Label:           "Todo",
		InlineEditable:  true,
		ContinueOnEnter: true,
		GlyphRef:        todoGlyphRef,
	})
}

func todoGlyphRef(n Ref) (string, string) {
	if n.CompletedAt() > 0 {
		return "■", Theme().Dim
	}
	return "□", Theme().Dim
}
