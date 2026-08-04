package editor

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
)

// Horizontal selection: shift+←/→ grows the cursor node's own text one rune at
// a time; ctrl/alt+shift+←/→ grows it by a word. This follows the selection
// vocabulary used by ordinary text editors. Esc (or any plain movement/typing)
// drops it.
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
	cur := m.richTextItem(m.cursorItem())
	if !m.textSelOn || cur == nil || m.richSpanUUID(cur) != textSelUUID {
		return nil, 0, 0, false
	}
	lo, hi := textSelLo, textSelHi
	if n := len([]rune(m.richText(cur))); hi > n {
		hi = n
	}
	if hi <= lo {
		return nil, 0, 0, false
	}
	return cur, lo, hi, true
}

// isTypedRune reports whether a key is a character the user typed — the thing
// that should replace a selection. Alt-chords are commands, not text; a paste is
// text and replaces the run the same way.
func isTypedRune(k tea.KeyMsg) bool {
	if k.Alt {
		return false
	}
	return k.Type == tea.KeyRunes && len(k.Runes) > 0 || k.Type == tea.KeySpace
}

// deleteTextSelection removes the selected run from whichever surface owns it —
// a node's name or its note — and parks the caret where the run began. It
// reports whether anything was deleted.
//
// It is what makes a selection behave like a selection: typing over one replaces
// it, backspace over one removes it. Selecting a word and typing used to leave
// the word sitting there with a stray letter beside it, which is not what any
// editor anywhere does.
func (m *Model) deleteTextSelection() bool {
	cur, lo, hi, ok := m.textSelection()
	if !ok {
		return false
	}
	// the note is edited on the resolved node; a name goes through editTarget so
	// a mirror deletes from its source
	target := cur
	if !m.richNoteActive() {
		if target = m.editTargetOf(cur); target == nil {
			return false
		}
	}
	m.pushUndo("")

	text := m.richText(target)
	runes := []rune(text)
	if hi > len(runes) {
		hi = len(runes)
	}
	if lo < 0 || lo >= hi {
		return false
	}
	// a chip inside the run goes with it — the anchor would otherwise survive as
	// an orphan "@?" pointing at a record nothing can reach
	for _, sp := range anchorSpans(runes) {
		if sp.start >= lo && sp.end <= hi {
			m.deleteChipID(sp.id)
		}
	}
	m.setRichText(target, string(runes[:lo])+string(runes[hi:]))
	id := m.richSpanUUID(target)
	shiftSpans(id, lo, lo-hi)
	m.persistSpans(id)
	m.caret = lo
	m.clearTextSel()
	m.unsaved = true
	return true
}

// extendTextSel moves the caret one step (a word when byWord, otherwise one
// rune) and grows the selection to it. The first press anchors at the caret;
// the selection never leaves the node it started on.
func (m *Model) extendTextSel(dir int, byWord bool) {
	cur := m.richTextItem(m.cursorItem())
	if cur == nil {
		return
	}
	runes := []rune(m.richText(cur))
	if len(runes) == 0 {
		return
	}
	m.clearSel() // rows or runes, never both
	caret := m.boundCaret(len(runes))
	if !m.textSelOn || m.richSpanUUID(cur) != textSelUUID {
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
	cur := m.richTextItem(m.cursorItem())
	if !m.textSelOn || cur == nil {
		textSelUUID = ""
		return
	}
	runes := []rune(m.richText(cur))
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
	textSelUUID, textSelLo, textSelHi = m.richSpanUUID(cur), lo, hi
	m.flash = fmt.Sprintf("selected %q · alt+c styles it", trimFlash(string(runes[lo:hi]), 16))
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
