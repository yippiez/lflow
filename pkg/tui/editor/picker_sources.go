package editor

// Concrete pickerSource implementations for the Group-A shared listPicker: the
// slash menu, /type, /style, /theme, and the "#"/":" completer. Each is a
// stateless struct; all live state is on the Model (m.list, plus the inline
// anchors m.slashStart/slashInline and m.compl).

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
)

// listSource maps the current mode to its pickerSource. Returns nil outside the
// Group-A picker modes.
func (m *Model) listSource() pickerSource {
	switch m.mode {
	case modeSlash:
		return slashSource{}
	case modeType:
		return typeSource{}
	case modeStyle:
		return styleSource{}
	case modeTheme:
		return themeSource{}
	case modeComplete:
		return completerSource{}
	}
	return nil
}

// handleListMode routes a key for the active Group-A picker through the shared
// listPicker.
func (m *Model) handleListMode(k tea.KeyMsg, src pickerSource) (tea.Model, tea.Cmd) {
	_, mm, cmd := m.list.handleKey(m, k, src)
	return mm, cmd
}

// --- slash menu ------------------------------------------------------------

type slashSource struct{}

func (slashSource) items(m *Model, q string) []pickerItem {
	var out []pickerItem
	for _, c := range m.filteredSlash(q) {
		c := c
		out = append(out, pickerItem{value: c.name, render: func(bool) string {
			return cFG + fmt.Sprintf("%-14s", c.name) + cDim + " " + c.desc + cReset
		}})
	}
	return out
}

func (slashSource) header(*Model, *listPicker) string { return "" }
func (slashSource) initialSel(*Model) int             { return 0 }

func (slashSource) onSelect(m *Model, it pickerItem) (tea.Model, tea.Cmd) {
	if it.value == "" {
		m.mode = modeOutline
		return m, nil
	}
	m.stripSlashText()
	return m.runSlash(it.value)
}

func (slashSource) onRune(m *Model, p *listPicker, r []rune) bool {
	p.query += string(r)
	p.sel = 0
	if m.slashInline {
		if cur := m.cursorItem(); cur != nil {
			runes := []rune(cur.name)
			ins := []rune(string(r))
			cur.name = string(runes[:m.caret]) + string(ins) + string(runes[m.caret:])
			m.caret += len(ins)
		}
	}
	// nothing matches anymore: it was ordinary text, keep it as typed and close
	return len(m.filteredSlash(p.query)) == 0
}

func (s slashSource) onSpace(m *Model, p *listPicker) bool {
	// space is just another query rune for the slash menu
	return s.onRune(m, p, []rune{' '})
}

func (slashSource) onBackspace(m *Model, p *listPicker) bool {
	cur := m.cursorItem()
	if qr := []rune(p.query); len(qr) > 0 {
		p.query = string(qr[:len(qr)-1])
		p.sel = 0
		if m.slashInline && cur != nil && m.caret > 0 {
			runes := []rune(cur.name)
			cur.name = string(runes[:m.caret-1]) + string(runes[m.caret:])
			m.caret--
		}
		return false
	}
	if m.slashInline && cur != nil {
		m.stripSlashText()
	}
	return true
}

// --- /type -----------------------------------------------------------------

type typeSource struct{}

func (typeSource) items(m *Model, q string) []pickerItem {
	var out []pickerItem
	for _, t := range m.filteredTypes(q) {
		out = append(out, pickerItem{label: typeLabels[t], value: t})
	}
	return out
}

func (typeSource) header(m *Model, p *listPicker) string {
	query := p.query
	if query == "" {
		query = cDim + "type to search" + cReset
	} else {
		query = cFG + query + cReset
	}
	return " " + cDim + "type: " + cReset + query
}

func (typeSource) initialSel(m *Model) int {
	cur := m.cursorItem()
	if cur == nil {
		return 0
	}
	for i, t := range m.filteredTypes("") {
		if t == cur.typ {
			return i
		}
	}
	return 0
}

func (typeSource) onSelect(m *Model, it pickerItem) (tea.Model, tea.Cmd) {
	if it.value != "" {
		if cur := m.cursorItem(); cur != nil {
			m.pushUndo("")
			cur.typ = it.value
			m.unsaved = true
		}
	}
	m.mode = modeOutline
	return m, nil
}

// --- /style ----------------------------------------------------------------

type styleSource struct{}

func (styleSource) items(m *Model, q string) []pickerItem {
	cur := m.cursorItem()
	out := make([]pickerItem, 0, len(stylePickerItems))
	for _, sp := range stylePickerItems {
		sp := sp
		out = append(out, pickerItem{value: sp.value, render: func(bool) string {
			if sp.kind == "toggle" {
				active := ""
				if cur != nil && styleHas(cur.style, sp.value) {
					active = cDim + " (on)" + cReset
				}
				return cFG + stylePickerLabels[sp.value] + active + cReset
			}
			swatch := styleColorCode[sp.value] + "●" + cReset
			return swatch + " " + styleColorCode[sp.value] + stylePickerLabels[sp.value] + cReset
		}})
	}
	return out
}

func (styleSource) header(*Model, *listPicker) string { return "" }

func (styleSource) initialSel(m *Model) int {
	cur := m.cursorItem()
	if cur == nil {
		return 0
	}
	for i, sp := range stylePickerItems {
		if sp.kind == "toggle" && styleHas(cur.style, sp.value) {
			return i
		}
	}
	c := styleColor(cur.style)
	for i, sp := range stylePickerItems {
		if sp.kind == "color" && sp.value == c {
			return i
		}
	}
	return 0
}

func (styleSource) onSelect(m *Model, it pickerItem) (tea.Model, tea.Cmd) {
	if cur := m.cursorItem(); cur != nil && it.value != "" {
		m.pushUndo("")
		for _, sp := range stylePickerItems {
			if sp.value == it.value {
				if sp.kind == "toggle" {
					cur.style = styleToggle(cur.style, sp.value)
				} else {
					cur.style = styleSetColor(cur.style, sp.value)
				}
				break
			}
		}
		m.unsaved = true
	}
	m.mode = modeOutline
	return m, nil
}

// --- /theme ----------------------------------------------------------------

type themeSource struct{}

func (themeSource) items(m *Model, q string) []pickerItem {
	out := make([]pickerItem, 0, len(themes))
	for _, t := range themes {
		t := t
		out = append(out, pickerItem{value: t.name, render: func(bool) string {
			active := ""
			if t.name == activeThemeName {
				active = cDim + " (on)" + cReset
			}
			swatches := t.accent + "●" + t.red + "●" + t.yellow + "●" +
				t.green + "●" + t.cyan + "●" + t.purple + "●" + cReset
			return cFG + fmt.Sprintf("%-9s", t.name) + active + "  " + swatches
		}})
	}
	return out
}

func (themeSource) header(*Model, *listPicker) string { return "" }

func (themeSource) initialSel(m *Model) int {
	for i, t := range themes {
		if t.name == activeThemeName {
			return i
		}
	}
	return 0
}

func (themeSource) onSelect(m *Model, it pickerItem) (tea.Model, tea.Cmd) {
	for _, t := range themes {
		if t.name == it.value {
			applyTheme(t)
			m.saveTheme(t.name)
			m.flash = "theme · " + activeThemeName
			break
		}
	}
	m.mode = modeOutline
	return m, nil
}

// --- "#"/":" completer -----------------------------------------------------

type completerSource struct{}

func (completerSource) items(m *Model, q string) []pickerItem {
	var out []pickerItem
	for _, it := range m.complItems(q) {
		out = append(out, pickerItem{label: it.label, value: it.value, desc: it.desc})
	}
	return out
}

func (completerSource) header(*Model, *listPicker) string { return "" }
func (completerSource) initialSel(*Model) int             { return 0 }

func (completerSource) onSelect(m *Model, it pickerItem) (tea.Model, tea.Cmd) {
	m.applyCompletion(m.cursorItem(), it)
	m.mode = modeOutline
	return m, nil
}

func (completerSource) onRune(m *Model, p *listPicker, r []rune) bool {
	p.query += string(r)
	p.sel = 0
	if cur := m.cursorItem(); cur != nil {
		runes := []rune(cur.name)
		m.boundCaret(len(runes))
		ins := []rune(string(r))
		cur.name = string(runes[:m.caret]) + string(ins) + string(runes[m.caret:])
		m.caret += len(ins)
		m.unsaved = true
	}
	return false // the completer never auto-closes on typing (allows a brand-new tag)
}

func (completerSource) onSpace(m *Model, p *listPicker) bool {
	// space ends the completer. A tag commits the typed "#word" into a chip; a
	// query command leaves the text literal. Either way the space is typed normally.
	cur := m.cursorItem()
	if m.compl.kind == complTag && cur != nil {
		m.chipifyBeforeCaret(cur)
	}
	if cur != nil {
		runes := []rune(cur.name)
		m.boundCaret(len(runes))
		cur.name = string(runes[:m.caret]) + " " + string(runes[m.caret:])
		m.caret++
		m.unsaved = true
	}
	return true
}

func (completerSource) onBackspace(m *Model, p *listPicker) bool {
	cur := m.cursorItem()
	if qr := []rune(p.query); len(qr) > 0 {
		p.query = string(qr[:len(qr)-1])
		p.sel = 0
		m.delCharBeforeCaret(cur)
		return false
	}
	m.delCharBeforeCaret(cur) // remove the trigger itself
	return true
}
