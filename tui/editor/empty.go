package editor

import "github.com/lflow/lflow/tui/database"

// An empty node is a divider stripped of its rule: a deliberately blank row,
// pure vertical breathing room. Not inline-editable — it has no text; under
// the cursor it shows a normal red bullet glyph, same as any other selected
// row, and stays fully blank otherwise.
func init() {
	registerType(nodeType{
		key:   database.TypeEmpty,
		label: "Empty",
	})
}
