/* Copyright 2025 Lflow Authors
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package editor

import (
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// dateMatch is a natural-language time phrase found in a row: "now",
// "bugün", "11 şubat 2025 saat 15:20", "2025-02-11", "11/02/2025 9:30".
// ctrl+t replaces the phrase with a [[...]] date pill.
type dateMatch struct {
	start, end int // rune offsets of the phrase in the name
	t          time.Time
	hasTime    bool
	phrase     string
}

// pill renders the canonical pill token the phrase converts into.
func (d dateMatch) pill() string {
	if d.hasTime {
		return "[[" + d.t.Format("2006-01-02 15:04") + "]]"
	}
	return "[[" + d.t.Format("2006-01-02") + "]]"
}

// display is the pill content without brackets, for the bottom-bar hint.
func (d dateMatch) display() string {
	return strings.TrimSuffix(strings.TrimPrefix(d.pill(), "[["), "]]")
}

var monthsByName = map[string]time.Month{
	"ocak": time.January, "şubat": time.February, "mart": time.March,
	"nisan": time.April, "mayıs": time.May, "haziran": time.June,
	"temmuz": time.July, "ağustos": time.August, "eylül": time.September,
	"ekim": time.October, "kasım": time.November, "aralık": time.December,
	"january": time.January, "february": time.February, "march": time.March,
	"april": time.April, "may": time.May, "june": time.June,
	"july": time.July, "august": time.August, "september": time.September,
	"october": time.October, "november": time.November, "december": time.December,
}

const monthAlternation = `ocak|şubat|mart|nisan|mayıs|haziran|temmuz|ağustos|eylül|ekim|kasım|aralık|` +
	`january|february|march|april|may|june|july|august|september|october|november|december`

// monthLookup folds the matched month name with both the english and the
// turkish casing rules: MAYIS lowers to mayıs, APRIL must not become aprıl.
func monthLookup(s string) (time.Month, bool) {
	if m, ok := monthsByName[strings.ToLower(s)]; ok {
		return m, true
	}
	if m, ok := monthsByName[strings.ToLowerSpecial(unicode.TurkishCase, s)]; ok {
		return m, true
	}
	return 0, false
}

// optional clock suffix: "saat 15:20", "at 15:20", "15.20"
const clockSuffix = `(?:\s+(?:saat\s+|at\s+)?(\d{1,2})[:.](\d{2}))?`

var (
	reRelative = regexp.MustCompile(`(?i)(now|şimdi|today|bugün|tomorrow|yarın|yesterday|dün)`)
	reNamed    = regexp.MustCompile(`(?i)(\d{1,2})\s+(` + monthAlternation + `)\s+(\d{4})` + clockSuffix)
	reISO      = regexp.MustCompile(`(\d{4})-(\d{1,2})-(\d{1,2})(?:[ T](\d{1,2}):(\d{2}))?`)
	reNumeric  = regexp.MustCompile(`(\d{1,2})[./](\d{1,2})[./](\d{4})` + clockSuffix)
)

// wordBound reports whether the byte range [start,end) sits on its own:
// not glued to a letter or digit on either side, and not already inside a
// [[...]] pill.
func wordBound(s string, start, end int) bool {
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
	return !insidePill(s, start)
}

// insidePill reports whether the byte offset falls inside a [[...]] span. An
// open [[ with no matching ]] counts as a pill that runs to the end of the
// string: an unclosed bracket is treated as inside-pill context so a half-typed
// "[[now" suppresses the ctrl+t hint rather than nesting a second pill.
func insidePill(s string, off int) bool {
	i := 0
	for {
		open := strings.Index(s[i:], "[[")
		if open < 0 {
			return false
		}
		open += i
		closing := strings.Index(s[open+2:], "]]")
		if closing < 0 {
			return off >= open
		}
		end := open + 2 + closing + 2
		if off >= open && off < end {
			return true
		}
		i = end
	}
}

func atoi(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}

// buildDate validates the parts and returns the time, or false on nonsense
// like month 13 or february 30.
func buildDate(year, month, day, hour, min int, loc *time.Location) (time.Time, bool) {
	if month < 1 || month > 12 || day < 1 || day > 31 || hour > 23 || min > 59 {
		return time.Time{}, false
	}
	t := time.Date(year, time.Month(month), day, hour, min, 0, 0, loc)
	if t.Day() != day || t.Month() != time.Month(month) {
		return time.Time{}, false
	}
	return t, true
}

// detectDate finds the leftmost convertible time phrase in name.
func detectDate(name string, now time.Time) *dateMatch {
	var best *dateMatch
	consider := func(d *dateMatch) {
		if d != nil && (best == nil || d.start < best.start) {
			best = d
		}
	}

	consider(detectRelative(name, now))
	consider(detectPattern(name, reNamed, func(g []string) (time.Time, bool, bool) {
		month, ok := monthLookup(g[2])
		if !ok {
			return time.Time{}, false, false
		}
		hasTime := g[4] != ""
		t, ok := buildDate(atoi(g[3]), int(month), atoi(g[1]), atoi(g[4]), atoi(g[5]), now.Location())
		return t, hasTime, ok
	}))
	consider(detectPattern(name, reISO, func(g []string) (time.Time, bool, bool) {
		hasTime := g[4] != ""
		t, ok := buildDate(atoi(g[1]), atoi(g[2]), atoi(g[3]), atoi(g[4]), atoi(g[5]), now.Location())
		return t, hasTime, ok
	}))
	consider(detectPattern(name, reNumeric, func(g []string) (time.Time, bool, bool) {
		hasTime := g[4] != ""
		t, ok := buildDate(atoi(g[3]), atoi(g[2]), atoi(g[1]), atoi(g[4]), atoi(g[5]), now.Location())
		return t, hasTime, ok
	}))

	return best
}

// detectRelative finds the day words: now, bugün, tomorrow, dün...
func detectRelative(name string, now time.Time) *dateMatch {
	for _, loc := range reRelative.FindAllStringIndex(name, -1) {
		if !wordBound(name, loc[0], loc[1]) {
			continue
		}
		word := strings.ToLowerSpecial(unicode.TurkishCase, name[loc[0]:loc[1]])
		midnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
		var t time.Time
		hasTime := false
		switch word {
		case "now", "şimdi":
			t, hasTime = now, true
		case "today", "bugün":
			t = midnight
		case "tomorrow", "yarın":
			t = midnight.AddDate(0, 0, 1)
		case "yesterday", "dün":
			t = midnight.AddDate(0, 0, -1)
		default:
			continue
		}
		return &dateMatch{
			start:   utf8.RuneCountInString(name[:loc[0]]),
			end:     utf8.RuneCountInString(name[:loc[1]]),
			t:       t,
			hasTime: hasTime,
			phrase:  name[loc[0]:loc[1]],
		}
	}
	return nil
}

// detectPattern runs one absolute-date regexp and converts the first hit
// whose boundaries and calendar parts hold up.
func detectPattern(name string, re *regexp.Regexp, build func(groups []string) (time.Time, bool, bool)) *dateMatch {
	for _, loc := range re.FindAllStringSubmatchIndex(name, -1) {
		if !wordBound(name, loc[0], loc[1]) {
			continue
		}
		groups := make([]string, re.NumSubexp()+1)
		for g := 0; g <= re.NumSubexp(); g++ {
			if loc[2*g] >= 0 {
				groups[g] = name[loc[2*g]:loc[2*g+1]]
			}
		}
		t, hasTime, ok := build(groups)
		if !ok {
			continue
		}
		return &dateMatch{
			start:   utf8.RuneCountInString(name[:loc[0]]),
			end:     utf8.RuneCountInString(name[:loc[1]]),
			t:       t,
			hasTime: hasTime,
			phrase:  name[loc[0]:loc[1]],
		}
	}
	return nil
}
