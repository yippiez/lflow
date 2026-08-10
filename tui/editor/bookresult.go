package editor

import "github.com/lflow/lflow/tui/database"

// bookresult is GENERATED — one volume row a books node hangs under it (a link
// chip to the book's page, then its byline and year). Internal, so the /type
// picker never offers it; a re-run replaces the rows by type.
func init() {
	registerType(nodeType{
		key:      database.TypeBookResult,
		label:    "Book Result",
		internal: true,
	})
}
