package editor

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

// A tag is a #word token — '#' followed by letters, digits or underscores —
// anchored at a left boundary (start of text or a non-word char) so bare '#'s
// and mid-word hashes are ignored. Tags carry no stored markup: the literal
// "#word" lives in the node name. They render in a fixed muted gray (a /color
// never bleeds into them) and drive STRICT tag search — "#log" matches the tag
// "#log" only, never the word "log" nor the tag "#logic".
var reTag = regexp.MustCompile(`(^|[^\p{L}\p{N}_#])(#[\p{L}\p{N}_]+)`)

// detectTagSpans returns the [start,end) rune ranges of each tag's "#word" run.
func detectTagSpans(name string) [][2]int {
	var spans [][2]int
	for _, loc := range reTag.FindAllStringSubmatchIndex(name, -1) {
		s, e := loc[4], loc[5] // submatch 2 — the #word, minus any left boundary
		if s < 0 {
			continue
		}
		start := utf8.RuneCountInString(name[:s])
		end := utf8.RuneCountInString(name[:e])
		spans = append(spans, [2]int{start, end})
	}
	return spans
}

// tagsIn returns the lowercased tag words (without the leading '#') in text.
func tagsIn(text string) []string {
	var out []string
	for _, m := range reTag.FindAllStringSubmatch(text, -1) {
		out = append(out, strings.ToLower(m[2][1:]))
	}
	return out
}

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
