package chips

import (
	"regexp"
	"unicode/utf8"
)

// The link chip: a markdown-style "[label](target)" pair whose target is a
// zotero://select/ URI is a CITATION, not a web link. Recognition lives here,
// beside the other shared chip vocabulary, so the editor and the CLI chipify
// the same way.

// reLink matches a markdown-style link "[label](target)".
var reLink = regexp.MustCompile(`\[([^\]]+)\]\(([^)]+)\)`)

// linkSpan is one "[label](target)" match in rune offsets, with its parts.
type linkSpan struct {
	Start, End int
	Label      string
	Target     string
}

// linkSpans returns every "[label](target)" link in name, in order.
func linkSpans(name string) []linkSpan {
	var out []linkSpan
	for _, loc := range reLink.FindAllStringSubmatchIndex(name, -1) {
		out = append(out, linkSpan{
			Start:  utf8.RuneCountInString(name[:loc[0]]),
			End:    utf8.RuneCountInString(name[:loc[1]]),
			Label:  name[loc[2]:loc[3]],
			Target: name[loc[4]:loc[5]],
		})
	}
	return out
}
