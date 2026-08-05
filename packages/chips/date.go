package chips

import (
	"regexp"
	"strconv"
	"time"
	"unicode"
	"unicode/utf8"
)

// The date chip: a canonical YYYY-MM-DD (optionally with HH:MM) on its own word
// boundary. Recognition lives here, beside the other shared chip vocabulary, so
// the editor and the CLI chipify the same way.

// ReISO matches a canonical date: YYYY-MM-DD optionally followed by HH:MM.
var ReISO = regexp.MustCompile(`(\d{4})-(\d{1,2})-(\d{1,2})(?:[ T](\d{1,2}):(\d{2}))?`)

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
