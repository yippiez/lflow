// Package chiptext is the text-pattern vocabulary for chips: it recognizes the
// inline forms — #tags, canonical dates, and [label](target) links — in a plain
// node name, and Chipify rewrites them into chip anchors. It is the single source
// of truth shared by the editor (which detects the same spans to render and
// auto-chipify on the fly) and the CLI (which chipifies text passed to add/edit).
//
// Only the patterns that have an unambiguous inline form live here. Path chips
// have no text marker (the editor creates them through the /file picker), so they
// are never auto-detected.
package chiptext

import (
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// Chip kind keys. These are plain strings matching the editor's chip-kind
// registry and the values stored in the chips table.
const (
	KindTag  = "tag"
	KindDate = "date"
	KindLink = "link"
)

// ReTag matches a #word tag at a left boundary (start of text or a non-word
// char) so bare '#'s and mid-word hashes are ignored. Submatch 2 is the #word.
var ReTag = regexp.MustCompile(`(^|[^\p{L}\p{N}_#])(#[\p{L}\p{N}_]+)`)

// ReISO matches a canonical date: YYYY-MM-DD optionally followed by HH:MM.
var ReISO = regexp.MustCompile(`(\d{4})-(\d{1,2})-(\d{1,2})(?:[ T](\d{1,2}):(\d{2}))?`)

// reLink matches a markdown-style link "[label](target)".
var reLink = regexp.MustCompile(`\[([^\]]+)\]\(([^)]+)\)`)

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

// DateSpans returns the rune ranges [start,end) of every canonical date in the
// name — a valid YYYY-MM-DD optionally followed by HH:MM, on its own word
// boundary. Natural-language phrases (e.g. "tomorrow") are not matched: only
// already-canonical dates become chips.
func DateSpans(name string) [][2]int {
	var spans [][2]int
	for _, loc := range ReISO.FindAllStringSubmatchIndex(name, -1) {
		if !WordBound(name, loc[0], loc[1]) {
			continue
		}
		group := func(i int) string {
			if loc[2*i] >= 0 {
				return name[loc[2*i]:loc[2*i+1]]
			}
			return ""
		}
		if _, ok := BuildDate(Atoi(group(1)), Atoi(group(2)), Atoi(group(3)), Atoi(group(4)), Atoi(group(5)), time.UTC); !ok {
			continue
		}
		spans = append(spans, [2]int{utf8.RuneCountInString(name[:loc[0]]), utf8.RuneCountInString(name[:loc[1]])})
	}
	return spans
}

// LinkSpan is one "[label](target)" match in rune offsets, with its parts.
type LinkSpan struct {
	Start, End int
	Label      string
	Target     string
}

// LinkSpans returns every "[label](target)" link in name, in order.
func LinkSpans(name string) []LinkSpan {
	var out []LinkSpan
	for _, loc := range reLink.FindAllStringSubmatchIndex(name, -1) {
		out = append(out, LinkSpan{
			Start:  utf8.RuneCountInString(name[:loc[0]]),
			End:    utf8.RuneCountInString(name[:loc[1]]),
			Label:  name[loc[2]:loc[3]],
			Target: name[loc[4]:loc[5]],
		})
	}
	return out
}

// WordBound reports whether the byte range [start,end) sits on its own: not
// glued to a letter or digit on either side.
func WordBound(s string, start, end int) bool {
	if start > 0 {
		r, _ := utf8.DecodeLastRuneInString(s[:start])
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return false
		}
	}
	if end < len(s) {
		r, _ := utf8.DecodeRuneInString(s[end:])
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return false
		}
	}
	return true
}

// Atoi parses an int, returning 0 on error.
func Atoi(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}

// BuildDate validates the parts and returns the time, or false on nonsense like
// month 13 or february 30.
func BuildDate(year, month, day, hour, min int, loc *time.Location) (time.Time, bool) {
	if month < 1 || month > 12 || day < 1 || day > 31 || hour > 23 || min > 59 {
		return time.Time{}, false
	}
	t := time.Date(year, time.Month(month), day, hour, min, 0, 0, loc)
	if t.Day() != day || t.Month() != time.Month(month) {
		return time.Time{}, false
	}
	return t, true
}

// span is one detected inline form to chipify.
type span struct {
	start, end         int
	kind, value, label string
}

// Chipify rewrites every detected inline form in name into a chip anchor by
// calling mk(kind, value, label), which records the chip and returns its anchor
// (or "" to decline, leaving the original text in place). Overlapping matches
// resolve to the earliest — a #tag inside a [link] label stays part of the link.
// The note text is never chipified; only names carry chip anchors.
func Chipify(name string, mk func(kind, value, label string) string) string {
	runes := []rune(name)
	var spans []span
	for _, sp := range TagSpans(name) {
		spans = append(spans, span{sp[0], sp[1], KindTag, strings.TrimPrefix(string(runes[sp[0]:sp[1]]), "#"), ""})
	}
	for _, sp := range DateSpans(name) {
		spans = append(spans, span{sp[0], sp[1], KindDate, string(runes[sp[0]:sp[1]]), ""})
	}
	for _, sp := range LinkSpans(name) {
		spans = append(spans, span{sp.Start, sp.End, KindLink, sp.Target, sp.Label})
	}
	if len(spans) == 0 {
		return name
	}
	sort.Slice(spans, func(i, j int) bool { return spans[i].start < spans[j].start })

	var b strings.Builder
	prev, last := 0, -1
	for _, s := range spans {
		if s.start < last {
			continue // overlapping an earlier span — keep the earlier
		}
		b.WriteString(string(runes[prev:s.start]))
		if anchor := mk(s.kind, s.value, s.label); anchor != "" {
			b.WriteString(anchor)
		} else {
			b.WriteString(string(runes[s.start:s.end]))
		}
		prev, last = s.end, s.end
	}
	b.WriteString(string(runes[prev:]))
	return b.String()
}
