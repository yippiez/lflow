// Package chips is the text-pattern vocabulary for chips: it recognizes the
// inline forms — #tags, canonical dates, and [label](target) links — in a plain
// node name, and Chipify rewrites them into chip anchors. It is the single source
// of truth shared by the editor (which detects the same spans to render and
// auto-chipify on the fly) and the CLI (which chipifies text passed to add/edit).
//
// Only the patterns that have an unambiguous inline form live here. Chip kinds
// created through an explicit picker rather than typed text (e.g. icon chips)
// are never auto-detected.
//
// One file per chip kind: tag.go, date.go, link.go — each holds that kind's
// pattern and span detection — plus service.go (link targets that belong to a
// known web service) and this general file (the Chipify machinery itself).
package chips

import (
	"sort"
	"strings"
)

// Chip kind keys. These are plain strings matching the editor's chip-kind
// registry and the values stored in the chips table.
const (
	kindTag    = "tag"
	kindDate   = "date"
	kindLink   = "link"
	kindZotero = "zotero"
)

// zoteroScheme prefixes a Zotero select URI. A "[label](target)" whose target
// is one is a CITATION, not a plain web link, so it chipifies as a zotero chip
// — that is how `lflow add` writes a citation the editor lights up. Spelled out
// here rather than imported so this vocabulary package stays a leaf.
const zoteroScheme = "zotero://select/"

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
		spans = append(spans, span{sp[0], sp[1], kindTag, strings.TrimPrefix(string(runes[sp[0]:sp[1]]), "#"), ""})
	}
	for _, sp := range DateSpans(name) {
		spans = append(spans, span{sp[0], sp[1], kindDate, string(runes[sp[0]:sp[1]]), ""})
	}
	for _, sp := range linkSpans(name) {
		kind := kindLink
		if strings.HasPrefix(sp.Target, zoteroScheme) {
			kind = kindZotero
		}
		spans = append(spans, span{sp.Start, sp.End, kind, sp.Target, sp.Label})
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
