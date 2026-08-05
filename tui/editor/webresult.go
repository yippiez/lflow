package editor

import "github.com/lflow/lflow/tui/database"

// webresult is GENERATED — a web search hit row a web node hangs under it
// (title + URL link chip). Internal, so the /type picker never offers it; a
// re-run replaces the rows by type.
func init() {
	registerType(nodeType{
		key:      database.TypeWebResult,
		label:    "Web Result",
		internal: true,
	})
}
