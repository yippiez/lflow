package nodes

import (
	"github.com/lflow/lflow/packages/database"
)

// webresult is GENERATED — a web search hit row a web node hangs under it
// (title + URL link chip). Internal, so the /type picker never offers it; a
// re-run replaces the rows by type.
func init() {
	Register(Plugin{
		Key:            database.TypeWebResult,
		Label:          "Web Result",
		Internal:       true,
		InlineEditable: false,
	})
}
