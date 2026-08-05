package chips

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

// The tag chip: a #word at a left boundary. Recognition lives here, beside the
// other shared chip vocabulary, so the editor and the CLI chipify the same way.

// ReTag matches a #word tag at a left boundary (start of text or a non-word
// char) so bare '#'s and mid-word hashes are ignored. The word must start with a
// letter or underscore (#1, #42 mean literal "number one"), may contain inner
// hyphens (#multi-word) but not a trailing one. Submatch 2 is the #word.
var ReTag = regexp.MustCompile(`(^|[^\p{L}\p{N}_#])(#[\p{L}_][\p{L}\p{N}_]*(?:-[\p{L}\p{N}_]+)*)`)

// TagSpans returns the [start,end) rune ranges of each tag's "#word" run.
func TagSpans(name string) [][2]int {
	var spans [][2]int
	for _, loc := range ReTag.FindAllStringSubmatchIndex(name, -1) {
		s, e := loc[4], loc[5] // submatch 2 — the #word, minus any left boundary
		if s < 0 {
			continue
		}
		spans = append(spans, [2]int{utf8.RuneCountInString(name[:s]), utf8.RuneCountInString(name[:e])})
	}
	return spans
}

// TagsIn returns the lowercased tag words (without the leading '#') in text.
func TagsIn(text string) []string {
	var out []string
	for _, m := range ReTag.FindAllStringSubmatch(text, -1) {
		out = append(out, strings.ToLower(m[2][1:]))
	}
	return out
}
