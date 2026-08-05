package editor

import "github.com/lflow/lflow/packages/database"

// An empty node is a divider stripped of its rule: a deliberately blank row,
// pure vertical breathing room. Not inline-editable — it has no text; under
// the cursor a single red · marks the otherwise blank line.
func init() {
	registerType(nodeType{
		key:   database.TypeEmpty,
		label: "Empty",
	})
}
