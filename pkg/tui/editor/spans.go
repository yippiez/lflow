package editor

import (
	"github.com/lflow/lflow/pkg/tui/database"
	"github.com/lflow/lflow/pkg/tui/style"
)

// Styled runs: a /style choice landing on a RUN of the node's text instead of
// the whole line. The run is the horizontal selection (shift+←/→, see
// textsel.go) — with one live, the /style picker styles exactly the selected
// text; with none, it styles the node as before. Runs persist in node_spans as
// [start,end)+style — the stored text stays markup-free (the no-markup
// invariant holds; runs are a parallel annotation, like chips).

// nodeSpans is the whole outline's span map (uuid → ordered spans), hydrated at
// editor start like tagColors so the render path stays Model-free.
var nodeSpans = map[string][]database.NodeSpan{}

// applyStyleToSpan lands a /style choice on [lo,hi) of the node's text. The
// run's existing style composes — bold over a red run keeps both — and, as in
// the whole-node picker, an attribute already on (or the color already in
// effect) is taken back off, so re-picking is the unstyle.
func (m *Model) applyStyleToSpan(cur *item, lo, hi int, value string) {
	if value == "" || hi <= lo {
		return
	}
	// a mirror's text belongs to its source, so the span does too
	if cur = m.editTargetOf(cur); cur == nil {
		return
	}
	m.pushUndo("")
	styled := cur
	if m.noteRich {
		cpy := *cur
		cpy.uuid = noteSpanUUID(cur)
		cpy.name = cur.note
		styled = &cpy
	}
	existing := "" // the style already covering the whole run, if any
	for _, sp := range nodeSpans[styled.uuid] {
		if sp.Start <= lo && sp.End >= hi {
			existing = sp.Style
			break
		}
	}
	tok := existing
	for _, sp := range stylePickerItems {
		if sp.value == value {
			if sp.kind == "toggle" {
				tok = style.Toggle(tok, sp.value)
			} else {
				tok = style.SetColor(tok, sp.value) // same color again clears it
			}
			break
		}
	}
	m.setSpanStyle(styled, lo, hi, tok)
	m.unsaved = true
	label := stylePickerLabels[value]
	if !style.Has(tok, value) && style.Color(tok) != value {
		label += " off" // re-picking took it back off the run
	}
	m.flash = "styled " + trimFlash(string([]rune(styled.name)[lo:hi]), 24) + " · " + label
}

// setSpanStyle rewrites the node's spans so [lo,hi) carries exactly the given
// style token string ("" = unstyled): overlapping spans are split around the
// run, then the run is inserted (unless clearing) and neighbors merge.
func (m *Model) setSpanStyle(cur *item, lo, hi int, styleTok string) {
	var next []database.NodeSpan
	for _, sp := range nodeSpans[cur.uuid] {
		if sp.End <= lo || sp.Start >= hi { // untouched
			next = append(next, sp)
			continue
		}
		if sp.Start < lo { // left remainder
			next = append(next, database.NodeSpan{NodeUUID: cur.uuid, Start: sp.Start, End: lo, Style: sp.Style})
		}
		if sp.End > hi { // right remainder
			next = append(next, database.NodeSpan{NodeUUID: cur.uuid, Start: hi, End: sp.End, Style: sp.Style})
		}
	}
	if styleTok != "" {
		next = append(next, database.NodeSpan{NodeUUID: cur.uuid, Start: lo, End: hi, Style: styleTok})
	}
	sortSpans(next)
	next = mergeSpans(next)
	if len(next) == 0 {
		delete(nodeSpans, cur.uuid)
	} else {
		nodeSpans[cur.uuid] = next
	}
	if m.db != nil {
		_ = database.ReplaceNodeSpans(m.db, cur.uuid, next)
	}
}

func sortSpans(spans []database.NodeSpan) {
	for i := 1; i < len(spans); i++ {
		for j := i; j > 0 && spans[j].Start < spans[j-1].Start; j-- {
			spans[j], spans[j-1] = spans[j-1], spans[j]
		}
	}
}

// mergeSpans joins touching runs that carry the same style.
func mergeSpans(spans []database.NodeSpan) []database.NodeSpan {
	var out []database.NodeSpan
	for _, sp := range spans {
		if n := len(out); n > 0 && out[n-1].End >= sp.Start && out[n-1].Style == sp.Style {
			if sp.End > out[n-1].End {
				out[n-1].End = sp.End
			}
			continue
		}
		out = append(out, sp)
	}
	return out
}

// shiftSpans adjusts a node's spans after a text edit at rune pos: delta > 0
// inserted runes, delta < 0 deleted them. Edges clamp; emptied runs drop.
func shiftSpans(uuid string, pos, delta int) {
	spans, ok := nodeSpans[uuid]
	if !ok {
		return
	}
	move := func(v int) int {
		if delta > 0 {
			if v >= pos {
				return v + delta
			}
			return v
		}
		cut := -delta
		switch {
		case v <= pos:
			return v
		case v >= pos+cut:
			return v - cut
		default:
			return pos
		}
	}
	var next []database.NodeSpan
	for _, sp := range spans {
		sp.Start, sp.End = move(sp.Start), move(sp.End)
		if sp.End > sp.Start {
			next = append(next, sp)
		}
	}
	if len(next) == 0 {
		delete(nodeSpans, uuid)
		return
	}
	nodeSpans[uuid] = next
}

// persistSpans writes a node's (possibly shifted) spans through to the DB.
func (m *Model) persistSpans(uuid string) {
	if m.db == nil {
		return
	}
	_ = database.ReplaceNodeSpans(m.db, uuid, nodeSpans[uuid])
}

// spanSGRFor precomputes each rune's painted SGR override for a node name.
// Returns nil when the node carries no spans (the common case, zero cost).
func spanSGRFor(uuid string, n int) []string {
	spans, ok := nodeSpans[uuid]
	if !ok {
		return nil
	}
	out := make([]string, n)
	for _, sp := range spans {
		code := ""
		if c := style.Color(sp.Style); c != "" {
			code += styleColorCode[c]
		}
		code += styleAttrs(sp.Style)
		if code == "" {
			continue
		}
		for i := sp.Start; i < sp.End && i < n; i++ {
			out[i] = code
		}
	}
	return out
}
