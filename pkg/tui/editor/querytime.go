package editor

import (
	"strings"
	"time"

	"github.com/lflow/lflow/pkg/tui/database"
)

// A query node's text may carry time filters alongside its words. Two tokens are
// recognised — ":after:<date>" and ":before:<date>" (":since:"/":until:" alias
// them) — and AND with the residual substring/tag query. A node passes the time
// filter when ANY of its dates falls in the window; a node's dates are its
// creation time (so log nodes match by their timestamp) plus every date chip /
// inline date in its name.

// timeQuery is the parsed time filter plus the leftover text query. It also
// carries a ":type:<key>" filter (any number, OR'd) so a query can select node
// types alongside its time window and words, and the ":tree:" display flag —
// group hits under muted ancestor breadcrumbs instead of a flat list.
type timeQuery struct {
	after, before *time.Time
	types         []string
	tree          bool
	text          string
}

func (tq timeQuery) hasFilter() bool {
	return tq.hasTimeFilter() || len(tq.types) > 0
}

// hasTimeFilter reports whether a date window (after/before) is set — distinct
// from hasFilter, which also counts a ":type:" filter.
func (tq timeQuery) hasTimeFilter() bool { return tq.after != nil || tq.before != nil }

// matchType reports whether typ is selected: true when no ":type:" filter is set,
// otherwise typ must equal one of the filter's keys (case-insensitive).
func (tq timeQuery) matchType(typ string) bool {
	if len(tq.types) == 0 {
		return true
	}
	typ = strings.ToLower(typ)
	for _, t := range tq.types {
		if t == typ {
			return true
		}
	}
	return false
}

// matchDates reports whether any of dates lands inside [after, before] (either
// bound may be open).
func (tq timeQuery) matchDates(dates []time.Time) bool {
	for _, d := range dates {
		if tq.after != nil && d.Before(*tq.after) {
			continue
		}
		if tq.before != nil && d.After(*tq.before) {
			continue
		}
		return true
	}
	return false
}

// parseTimeQuery pulls the time tokens out of raw and parses each operand with
// the editor's shared date scanner (ISO, dd/mm/yyyy, "today"/"yesterday", …, a
// single whitespace-free token). A date-only :before bound extends to the end of
// that day so the day itself counts as "before or on".
func parseTimeQuery(raw string, now time.Time) timeQuery {
	var tq timeQuery
	var kept []string
	for _, f := range strings.Fields(raw) {
		lf := strings.ToLower(f)
		if rest, ok := cutAnyPrefix(lf, ":after:", ":since:"); ok {
			if t, _, ok := parseQueryDate(rest, now); ok {
				lo := t
				tq.after = &lo
			}
			continue
		}
		if rest, ok := cutAnyPrefix(lf, ":before:", ":until:"); ok {
			if t, hasTime, ok := parseQueryDate(rest, now); ok {
				hi := t
				if !hasTime {
					hi = time.Date(t.Year(), t.Month(), t.Day(), 23, 59, 59, 0, t.Location())
				}
				tq.before = &hi
			}
			continue
		}
		if rest, ok := cutAnyPrefix(lf, ":type:"); ok {
			if rest != "" {
				tq.types = append(tq.types, rest)
			}
			continue
		}
		if lf == ":tree:" || lf == ":tree" {
			tq.tree = true
			continue
		}
		kept = append(kept, f)
	}
	tq.text = strings.Join(kept, " ")
	return tq
}

// cutAnyPrefix returns s with the first matching prefix removed, or false.
func cutAnyPrefix(s string, prefixes ...string) (string, bool) {
	for _, p := range prefixes {
		if strings.HasPrefix(s, p) {
			return s[len(p):], true
		}
	}
	return "", false
}

// parseQueryDate resolves a time operand to its time, whether it carried an
// explicit clock time, and ok. The leftmost recognised date wins.
func parseQueryDate(operand string, now time.Time) (time.Time, bool, bool) {
	ms := detectAllDates(operand, now)
	if len(ms) == 0 {
		return time.Time{}, false, false
	}
	best := ms[0]
	for _, mm := range ms[1:] {
		if mm.start < best.start {
			best = mm
		}
	}
	return best.t, best.hasTime, true
}

// nodeDates is the set of times a node matches against: its creation time plus
// every date chip / inline date in its (anchor-expanded) name.
func (m *Model) nodeDates(name string, addedOn int64, now time.Time) []time.Time {
	var out []time.Time
	if addedOn > 0 {
		out = append(out, time.Unix(0, addedOn))
	}
	plain := database.ExpandAnchors(name, m.chips)
	for _, dm := range detectAllDates(plain, now) {
		out = append(out, dm.t)
	}
	return out
}
