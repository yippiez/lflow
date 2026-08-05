package editor

import (
	"strings"
	"unicode"

	"github.com/lflow/lflow/tui/database"
)

// A tag is a #word token — '#' followed by a letter or underscore, then letters,
// digits or underscores — anchored at a left boundary (start of text or a
// non-word char) so bare '#'s, mid-word hashes and "#1" ("number one") are
// ignored. Tags carry no stored markup: the literal "#word" lives in the node
// name. They render in a fixed muted gray (a /color never bleeds into them) and
// drive STRICT tag search — "#log" matches the tag "#log" only, never the word
// "log" nor the tag "#logic". The pattern itself lives in tui/database,
// shared with the CLI's chipify.
var reTag = database.ReTag

// isTagLetter reports whether r may start a tag word — the first character after
// '#' — matching the ReTag pattern's opening class.
func isTagLetter(r rune) bool { return unicode.IsLetter(r) || r == '_' }

// detectTagSpans returns the [start,end) rune ranges of each tag's "#word" run.
func detectTagSpans(name string) [][2]int { return database.TagSpans(name) }

// tagsIn returns the lowercased tag words (without the leading '#') in text.
func tagsIn(text string) []string { return database.TagsIn(text) }

// tagQuery reports whether a search string is a bare tag (e.g. "#log") and, if
// so, returns its lowercased word for a strict whole-tag match.
func tagQuery(q string) (string, bool) {
	q = strings.TrimSpace(q)
	if !strings.HasPrefix(q, "#") {
		return "", false
	}
	if m := reTag.FindStringSubmatch(q); m != nil && m[2] == q {
		return strings.ToLower(q[1:]), true
	}
	return "", false
}

// nodeHasTag reports whether text carries the exact tag word (case-insensitive).
func nodeHasTag(text, tag string) bool {
	for _, t := range tagsIn(text) {
		if t == tag {
			return true
		}
	}
	return false
}
