package nodes

import (
	"github.com/lflow/lflow/packages/database"
)

// An empty node is a divider stripped of its rule: a deliberately blank row,
// pure vertical breathing room. Not inline-editable — it has no text; under
// the cursor a single red · marks the otherwise blank line.
func init() {
	Register(Plugin{
		Key:   database.TypeEmpty,
		Label: "Empty",
	})
}
