package editor

import "fmt"

// Horizontal selection: shift+←/→ grows a run of the cursor node's own TEXT —
// the horizontal twin of multi-select's shift+↑/↓. The unit mirrors the vertical
// one: shift+↑/↓ takes a whole row, so shift+←/→ takes a whole WORD;
// ctrl/alt+shift+←/→ adjust the edge by a single rune. esc (or any plain
// movement/typing) drops it.
//
// This is what /style then acts on: with a selection live, picking a style
// paints exactly that run instead of the whole node (see spans.go). It replaces
// the old painter mode — there is no p-to-paint window anymore, no separate
// mode: select the text, press style, done. The selection is view-state only:
// nothing about it is persisted.

// The live selection, mirrored into package vars so renderBody (which is
// Model-free by design) can draw the bar. Empty uuid = nothing selected.
var (
	textSelUUID string
	textSelLo   int
	textSelHi   int
)

// clearTextSel drops the horizontal selection.
func (m *Model) clearTextSel() {
	m.textSelOn = false
	textSelUUID = ""
}

// textSelection returns the cursor node and its selected [lo, hi) rune run.
// ok is false when nothing is selected, the run is empty, or the cursor moved
// off the node the selection belongs to.
func (m *Model) textSelection() (*item, int, int, bool) {
	cur := m.cursorItem()
	if !m.textSelOn || cur == nil || cur.uuid != textSelUUID {
		return nil, 0, 0, false
	}
	lo, hi := textSelLo, textSelHi
	if n := len([]rune(cur.name)); hi > n {
		hi = n
	}
	if hi <= lo {
		return nil, 0, 0, false
	}
	return cur, lo, hi, true
}

// extendTextSel moves the caret one step (a word, or one rune when byWord is
// false) and grows the selection to it. The first press anchors at the caret;
// the selection never leaves the node it started on.
func (m *Model) extendTextSel(dir int, byWord bool) {
	cur := m.cursorItem()
	if cur == nil {
		return
	}
	runes := []rune(cur.name)
	if len(runes) == 0 {
		return
	}
	m.clearSel() // rows or runes, never both
	caret := m.boundCaret(len(runes))
	if !m.textSelOn || cur.uuid != textSelUUID {
		m.textSelOn = true
		m.textSelAnchor = caret
	}
	switch {
	case byWord && dir < 0:
		caret = prevWordBoundary(runes, caret)
	case byWord:
		caret = wordEndAfter(runes, caret)
	default:
		caret += dir
	}
	if caret < 0 {
		caret = 0
	}
	if caret > len(runes) {
		caret = len(runes)
	}
	// a chip anchor is atomic: an edge that landed inside one takes the whole chip
	if sp := spanContaining(anchorSpans(runes), caret); sp != nil {
		if dir < 0 {
			caret = sp.start
		} else {
			caret = sp.end
		}
	}
	m.caret = caret
	m.syncTextSel()
}

// syncTextSel republishes the ordered selection bounds for the renderer.
func (m *Model) syncTextSel() {
	cur := m.cursorItem()
	if !m.textSelOn || cur == nil {
		textSelUUID = ""
		return
	}
	runes := []rune(cur.name)
	lo, hi := m.textSelAnchor, m.caret
	if lo > hi {
		lo, hi = hi, lo
	}
	if lo < 0 {
		lo = 0
	}
	if hi > len(runes) {
		hi = len(runes)
	}
	if hi <= lo {
		textSelUUID = "" // an empty run draws nothing, but the anchor stays live
		return
	}
	textSelUUID, textSelLo, textSelHi = cur.uuid, lo, hi
	m.flash = fmt.Sprintf("selected %q · alt+y styles it", trimFlash(string(runes[lo:hi]), 16))
}

// wordEndAfter returns the index just past the next word. The selection's right
// edge stops on the word's last rune rather than at the start of the following
// one (nextWordBoundary, which the caret keys use), so one shift+→ takes exactly
// "some" and not "some ".
func wordEndAfter(runes []rune, i int) int {
	for i < len(runes) && runes[i] == ' ' {
		i++
	}
	for i < len(runes) && runes[i] != ' ' {
		i++
	}
	return i
}

// trimFlash shortens a selected run for the status line.
func trimFlash(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "…"
}
