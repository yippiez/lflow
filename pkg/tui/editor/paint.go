package editor

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/lflow/lflow/pkg/tui/database"
	"github.com/lflow/lflow/pkg/tui/style"
)

// The painter: apply a /style choice to a RUN of the node's text instead of
// the whole line. In /style, enter applies the highlighted style to the whole
// node; p takes that same highlighted style into the painter instead: a small
// window appears over the text — ←/→ slide it, shift+←/→ resize it by one
// rune, enter paints the window with the chosen style, u unpaints it, esc
// leaves. Spans persist in node_spans as [start,end)+style — the stored text
// stays markup-free (the no-markup invariant holds; spans are a parallel
// annotation, like chips).

// nodeSpans is the whole outline's span map (uuid → ordered spans), hydrated at
// editor start like tagColors so the render path stays Model-free.
var nodeSpans = map[string][]database.NodeSpan{}

// paint window state: which node is being painted, the [start, start+width)
// rune window, and the /style row captured when p was pressed — mirrored into
// package vars so renderBody can highlight the window.
var (
	paintUUID  string
	paintStart int
	paintWidth int
	paintValue string // the pending style ("red", "bold", …) enter applies
)

func paintBounds() (int, int) {
	return paintStart, paintStart + paintWidth
}

// enterPaint starts painter mode on the cursor node: a one-rune window at the
// caret, carrying the /style row highlighted when p was pressed.
func (m *Model) enterPaint(value string) {
	cur := m.cursorItem()
	if cur == nil || cur.mirrorOf != "" {
		return
	}
	n := len([]rune(cur.name))
	if n == 0 {
		return
	}
	m.mode = modePaint
	paintUUID = cur.uuid
	paintStart = m.caret
	if paintStart > n-1 {
		paintStart = n - 1
	}
	if paintStart < 0 {
		paintStart = 0
	}
	paintWidth = 1
	paintValue = value
	label := stylePickerLabels[value]
	m.flash = "paint " + label + " · ←/→ move · shift+←/→ resize · enter paint · u unpaint · esc done"
}

func (m *Model) leavePaint() {
	m.mode = modeOutline
	paintUUID = ""
}

// handlePaintKey drives the window: plain arrows slide it, shift arrows grow
// and shrink it, enter paints it with the pending style.
func (m *Model) handlePaintKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	cur := m.cursorItem()
	if cur == nil || cur.uuid != paintUUID {
		m.leavePaint()
		return m, nil
	}
	runes := []rune(cur.name)
	n := len(runes)
	switch k.String() {
	case "esc", "q":
		m.leavePaint()
		return m, nil
	case "left":
		if paintStart > 0 {
			paintStart--
		}
		return m, nil
	case "right":
		if paintStart+paintWidth < n {
			paintStart++
		}
		return m, nil
	case "shift+left":
		if paintWidth > 1 {
			paintWidth--
		}
		return m, nil
	case "shift+right":
		if paintStart+paintWidth < n {
			paintWidth++
		}
		return m, nil
	case "ctrl+left", "ctrl+shift+left":
		paintStart = prevWordBoundary(runes, paintStart)
		return m, nil
	case "ctrl+right", "ctrl+shift+right":
		paintStart = nextWordBoundary(runes, paintStart)
		if paintStart > n-paintWidth {
			paintStart = n - paintWidth
		}
		return m, nil
	case "home":
		paintStart = 0
		return m, nil
	case "end":
		paintStart = n - paintWidth
		return m, nil
	case "u":
		lo, hi := paintBounds()
		m.pushUndo("")
		m.setSpanStyle(cur, lo, hi, "") // unpaint the window
		m.flash = "unpainted"
		return m, nil
	case "enter":
		m.applyPaint(cur)
		m.leavePaint()
		m.refreshRows()
		return m, nil
	}
	return m, nil
}

// applyPaint styles the window with the pending /style choice. The window's
// existing style composes: painting bold over a red run keeps both.
func (m *Model) applyPaint(cur *item) {
	lo, hi := paintBounds()
	if paintValue == "" || hi <= lo {
		return
	}
	m.pushUndo("")
	existing := ""
	for _, sp := range nodeSpans[cur.uuid] {
		if sp.Start <= lo && sp.End >= hi {
			existing = sp.Style
			break
		}
	}
	tok := existing
	for _, sp := range stylePickerItems {
		if sp.value == paintValue {
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
