package editor

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/lflow/lflow/pkg/tui/database"
	"github.com/lflow/lflow/pkg/tui/style"
)

// The painter: style a RUN of a node's text instead of the whole line. Enter
// it with p inside /style; shift+←/→ grow the selection from the caret (plain
// ←/→ move its edge); Enter opens the familiar style list applied to the run;
// u unpaints it; esc leaves. Spans persist in node_spans as [start,end)+style —
// the stored text stays markup-free (the no-markup invariant holds; spans are a
// parallel annotation, like chips).

// nodeSpans is the whole outline's span map (uuid → ordered spans), hydrated at
// editor start like tagColors so the render path stays Model-free.
var nodeSpans = map[string][]database.NodeSpan{}

// paint selection state: which node is being painted and the pending [lo,hi)
// rune run, mirrored into package vars so renderBody can invert the run.
var (
	paintUUID   string
	paintAnchor int
	paintCaret  int
)

func paintBounds() (int, int) {
	lo, hi := paintAnchor, paintCaret
	if lo > hi {
		lo, hi = hi, lo
	}
	return lo, hi
}

// enterPaint starts painter mode on the cursor node, anchored at the caret.
func (m *Model) enterPaint() {
	cur := m.cursorItem()
	if cur == nil || cur.mirrorOf != "" {
		return
	}
	m.mode = modePaint
	paintUUID = cur.uuid
	paintAnchor = m.caret
	paintCaret = m.caret
	m.flash = "paint · shift+←/→ select · enter style · u unpaint · esc done"
}

func (m *Model) leavePaint() {
	m.mode = modeOutline
	paintUUID = ""
}

// handlePaintKey drives the run selection.
func (m *Model) handlePaintKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	cur := m.cursorItem()
	if cur == nil || cur.uuid != paintUUID {
		m.leavePaint()
		return m, nil
	}
	n := len([]rune(cur.name))
	clamp := func(v int) int {
		if v < 0 {
			return 0
		}
		if v > n {
			return n
		}
		return v
	}
	switch k.String() {
	case "esc", "q":
		m.leavePaint()
		return m, nil
	case "shift+left", "left":
		paintCaret = clamp(paintCaret - 1)
		return m, nil
	case "shift+right", "right":
		paintCaret = clamp(paintCaret + 1)
		return m, nil
	case "ctrl+left", "ctrl+shift+left":
		paintCaret = prevWordBoundary([]rune(cur.name), paintCaret)
		return m, nil
	case "ctrl+right", "ctrl+shift+right":
		paintCaret = nextWordBoundary([]rune(cur.name), paintCaret)
		return m, nil
	case "home":
		paintCaret = 0
		return m, nil
	case "end":
		paintCaret = n
		return m, nil
	case "u":
		lo, hi := paintBounds()
		if hi > lo {
			m.pushUndo("")
			m.setSpanStyle(cur, lo, hi, "") // unpaint the run
			m.flash = "unpainted"
		}
		return m, nil
	case "enter":
		if lo, hi := paintBounds(); hi > lo {
			m.mode = modePaintStyle
			m.list = listPicker{}
			m.list.sel = 0
		}
		return m, nil
	}
	return m, nil
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

// --- the painter's style list (modePaintStyle) --------------------------------

// paintStyleSource is the /style list re-aimed at the pending run: same rows,
// but the choice styles the painted span instead of the node.
type paintStyleSource struct{}

func (paintStyleSource) items(m *Model, q string) []pickerItem {
	out := make([]pickerItem, 0, len(stylePickerItems))
	for _, sp := range stylePickerItems {
		sp := sp
		out = append(out, pickerItem{value: sp.value, render: func(bool) string {
			if sp.kind == "toggle" {
				return cFG + stylePickerLabels[sp.value] + cReset
			}
			swatch := styleColorCode[sp.value] + "●" + cReset
			return swatch + " " + styleColorCode[sp.value] + stylePickerLabels[sp.value] + cReset
		}})
	}
	return out
}

func (paintStyleSource) header(m *Model, p *listPicker) string {
	return " " + cDim + "paint the selection" + cReset
}

func (paintStyleSource) initialSel(*Model) int { return 0 }

func (paintStyleSource) onSelect(m *Model, it pickerItem) (tea.Model, tea.Cmd) {
	cur := m.cursorItem()
	lo, hi := paintBounds()
	if cur != nil && cur.uuid == paintUUID && it.value != "" && hi > lo {
		m.pushUndo("")
		// the run's existing style composes: picking bold then red keeps both
		existing := ""
		for _, sp := range nodeSpans[cur.uuid] {
			if sp.Start <= lo && sp.End >= hi {
				existing = sp.Style
				break
			}
		}
		tok := existing
		for _, sp := range stylePickerItems {
			if sp.value == it.value {
				if sp.kind == "toggle" {
					tok = style.Toggle(tok, sp.value)
				} else {
					tok = style.SetColor(tok, sp.value)
				}
				break
			}
		}
		m.setSpanStyle(cur, lo, hi, tok)
		m.unsaved = true
	}
	m.leavePaint()
	m.refreshRows()
	return m, nil
}
