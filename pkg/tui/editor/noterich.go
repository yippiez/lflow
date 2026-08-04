package editor

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// A note uses the same chip and styled-run representation as a node name. While
// a nested picker is open noteRich identifies which text surface owns its caret.
func (m *Model) richNoteActive() bool { return m.mode == modeNote || m.noteRich }

func noteSpanUUID(it *item) string {
	if it == nil {
		return ""
	}
	return it.uuid + "/note"
}

func (m *Model) richTextItem(cur *item) *item {
	if cur == nil {
		return nil
	}
	if m.richNoteActive() && m.tree != nil {
		return m.tree.resolve(cur)
	}
	return cur
}

func (m *Model) richText(cur *item) string {
	cur = m.richTextItem(cur)
	if cur == nil {
		return ""
	}
	if m.richNoteActive() {
		return cur.note
	}
	return cur.name
}

func (m *Model) setRichText(cur *item, text string) {
	cur = m.richTextItem(cur)
	if cur == nil {
		return
	}
	if m.richNoteActive() {
		cur.note = text
	} else {
		cur.name = text
	}
	m.unsaved = true
}

func (m *Model) richSpanUUID(cur *item) string {
	cur = m.richTextItem(cur)
	if cur == nil {
		return ""
	}
	if m.richNoteActive() {
		return noteSpanUUID(cur)
	}
	return cur.uuid
}

func (m *Model) beginNotePicker(next mode, src pickerSource, searchable bool) {
	m.noteRich = true
	m.mode = next
	m.list.open(m, src, searchable)
}

func (m *Model) finishRichPicker() {
	if m.noteRich {
		m.noteRich = false
		m.mode = modeNote
		return
	}
	m.mode = modeOutline
}

func (m *Model) noteRuneBefore(r rune) bool {
	runes := []rune(m.richText(m.cursorItem()))
	return m.caret > 0 && m.caret <= len(runes) && runes[m.caret-1] == r
}

func (m *Model) noteAtWordStart() bool {
	runes := []rune(m.richText(m.cursorItem()))
	if m.caret <= 0 {
		return true
	}
	if m.caret > len(runes) {
		m.caret = len(runes)
	}
	return m.caret == 0 || strings.ContainsRune(" \t\n([{", runes[m.caret-1])
}

func (m *Model) removeNoteRuneBeforeCaret() {
	cur := m.richTextItem(m.cursorItem())
	if cur == nil || m.caret <= 0 {
		return
	}
	runes := []rune(cur.note)
	if m.caret > len(runes) {
		m.caret = len(runes)
	}
	at := m.caret - 1
	cur.note = string(runes[:at]) + string(runes[m.caret:])
	shiftSpans(noteSpanUUID(cur), at, -1)
	m.persistSpans(noteSpanUUID(cur))
	m.caret = at
	m.unsaved = true
}

func (m *Model) chipifyNoteBeforeCaret(cur *item) bool {
	cur = m.richTextItem(cur)
	if cur == nil {
		return false
	}
	runes := []rune(cur.note)
	for _, sp := range detectTagSpans(cur.note) {
		if sp[1] == m.caret {
			value := strings.TrimPrefix(string(runes[sp[0]:sp[1]]), "#")
			return m.replaceNoteRangeWithChip(cur, sp[0], sp[1], chipKindTag, value)
		}
	}
	for _, sp := range detectDateSpans(cur.note) {
		if sp[1] == m.caret {
			return m.replaceNoteRangeWithChip(cur, sp[0], sp[1], chipKindDate, string(runes[sp[0]:sp[1]]))
		}
	}
	return false
}

func (m *Model) replaceNoteRangeWithChip(cur *item, start, end int, kind, value string) bool {
	anchor := m.createChip(kind, value)
	if anchor == "" {
		return false
	}
	runes := []rune(cur.note)
	cur.note = string(runes[:start]) + anchor + string(runes[end:])
	shiftSpans(noteSpanUUID(cur), start, len([]rune(anchor))-(end-start))
	m.persistSpans(noteSpanUUID(cur))
	m.caret = start + len([]rune(anchor))
	m.unsaved = true
	return true
}

// openNoteInsert is alt+a while /note is active: every chip kind uses the same
// searchable /insert picker as node text, but lands in the note.
func (m *Model) openNoteInsert() (tea.Model, tea.Cmd) {
	m.beginNotePicker(modeInsert, insertSource{}, true)
	return m, nil
}
