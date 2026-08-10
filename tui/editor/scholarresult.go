package editor

import (
	"strings"

	"github.com/lflow/lflow/tui/database"
)

// scholarresult is GENERATED — one Google Scholar hit a scholar node hangs
// under it: a link chip to the paper, then its citation. Internal, so the
// /type picker never offers it; a re-run replaces the rows by type.
func init() {
	registerType(nodeType{
		key:      database.TypeScholarResult,
		label:    "Scholar Result",
		internal: true,
		muteFrom: scholarMuteFrom,
	})
}

// scholarMuteFrom is the rune index the muted tail starts at — the separator
// before the citation, so a row reads as its title with the provenance quiet
// behind it; -1 mutes nothing. The chip anchor holds the title, so the first
// separator in the stored name is always the one setScholarRows wrote.
func scholarMuteFrom(name string) int {
	i := strings.Index(name, scholarCiteSep)
	if i < 0 {
		return -1
	}
	return len([]rune(name[:i]))
}
