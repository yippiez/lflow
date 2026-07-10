package editor

import tea "github.com/charmbracelet/bubbletea"

// textField is a single-line caret-editable string — the shared within-a-field
// editing primitive behind the /note editor (handleNoteKey) and the link-chip
// editor (handleLinkEditKey). It owns ONLY the caret vocabulary those two
// plain fields have in common: rune insert, backspace, word-delete, left/right,
// word-jump, home/end. Field switching, commit and cancel stay at each call
// site — this type never knows about them.
//
// It deliberately does NOT try to serve the outline node-name path in keys.go:
// that path is entangled with chip anchors, slash/tag/date/path/link triggers,
// mirror guards and paste fan-out, so it is not a plain text field.
type textField struct {
	value string
	caret int
}

// runes returns the value as a rune slice and clamps the caret into range so
// every operation below is bounds-safe.
func (f *textField) runes() []rune {
	r := []rune(f.value)
	if f.caret > len(r) {
		f.caret = len(r)
	}
	if f.caret < 0 {
		f.caret = 0
	}
	return r
}

// insert splices s (sanitized like a node name — C0/DEL and bracketed-paste
// markers stripped) at the caret.
func (f *textField) insert(s string) {
	ins := []rune(sanitizeName(s))
	r := f.runes()
	f.value = string(r[:f.caret]) + string(ins) + string(r[f.caret:])
	f.caret += len(ins)
}

// backspace deletes the rune before the caret.
func (f *textField) backspace() {
	r := f.runes()
	if f.caret > 0 {
		f.value = string(r[:f.caret-1]) + string(r[f.caret:])
		f.caret--
	}
}

// deleteWordLeft removes from the previous word boundary up to the caret
// (ctrl+backspace / ctrl+h / ctrl+w).
func (f *textField) deleteWordLeft() {
	r := f.runes()
	if f.caret > 0 {
		from := prevWordBoundary(r, f.caret)
		f.value = string(r[:from]) + string(r[f.caret:])
		f.caret = from
	}
}

func (f *textField) left() {
	f.runes()
	if f.caret > 0 {
		f.caret--
	}
}

func (f *textField) right() {
	if f.caret < len(f.runes()) {
		f.caret++
	}
}

func (f *textField) wordLeft()  { f.caret = prevWordBoundary(f.runes(), f.caret) }
func (f *textField) wordRight() { f.caret = nextWordBoundary(f.runes(), f.caret) }
func (f *textField) home()      { f.runes(); f.caret = 0 }
func (f *textField) end()       { f.caret = len(f.runes()) }

// handleKey applies one of the shared caret keys and reports whether the key
// belonged to the field. Space (unless alt-held) and non-alt rune keys insert;
// the movement and delete keys act as named. Any other key returns false so the
// call site can handle field switching, commit (enter) and cancel (esc).
func (f *textField) handleKey(k tea.KeyMsg) bool {
	switch k.String() {
	case "backspace":
		f.backspace()
	case "ctrl+backspace", "ctrl+h", "ctrl+w":
		f.deleteWordLeft()
	case "left":
		f.left()
	case "right":
		f.right()
	case "ctrl+left":
		f.wordLeft()
	case "ctrl+right":
		f.wordRight()
	case "home":
		f.home()
	case "end":
		f.end()
	default:
		if k.Type == tea.KeySpace && !k.Alt {
			f.insert(" ")
			return true
		}
		if k.Type == tea.KeyRunes && !k.Alt {
			f.insert(string(k.Runes))
			return true
		}
		return false
	}
	return true
}
