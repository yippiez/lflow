package editor

import "github.com/lflow/lflow/tui/database"

// archiveresult is GENERATED — an archive.org search hit row an archive node
// hangs under it (item title + /details/ link chip). Internal, so the /type
// picker never offers it; a re-run replaces the rows by type.
func init() {
	registerType(nodeType{
		key:      database.TypeArchiveResult,
		label:    "Archive Result",
		internal: true,
	})
}
