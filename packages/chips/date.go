package chips

import (
	"regexp"
	"time"
	"unicode/utf8"

	"github.com/lflow/lflow/packages/utils"
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
		if !utils.WordBound(name, loc[0], loc[1]) {
			continue
		}
		group := func(i int) string {
			if loc[2*i] >= 0 {
				return name[loc[2*i]:loc[2*i+1]]
			}
			return ""
		}
		if _, ok := BuildDate(utils.Atoi(group(1)), utils.Atoi(group(2)), utils.Atoi(group(3)), utils.Atoi(group(4)), utils.Atoi(group(5)), time.UTC); !ok {
			continue
		}
		spans = append(spans, [2]int{utf8.RuneCountInString(name[:loc[0]]), utf8.RuneCountInString(name[:loc[1]])})
	}
	return spans
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
