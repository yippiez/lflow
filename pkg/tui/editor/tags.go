package editor

import (
	"strings"

	"github.com/lflow/lflow/pkg/tui/chiptext"
)

// A tag is a #word token — '#' followed by letters, digits or underscores —
// anchored at a left boundary (start of text or a non-word char) so bare '#'s
// and mid-word hashes are ignored. Tags carry no stored markup: the literal
// "#word" lives in the node name. They render in a fixed muted gray (a /color
// never bleeds into them) and drive STRICT tag search — "#log" matches the tag
// "#log" only, never the word "log" nor the tag "#logic". The pattern itself
// lives in pkg/tui/chiptext, shared with the CLI's chipify.
var reTag = chiptext.ReTag

// detectTagSpans returns the [start,end) rune ranges of each tag's "#word" run.
func detectTagSpans(name string) [][2]int { return chiptext.TagSpans(name) }

// tagsIn returns the lowercased tag words (without the leading '#') in text.
func tagsIn(text string) []string { return chiptext.TagsIn(text) }

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
