package editor

// Concrete pickerSource implementations for the Group-A shared listPicker: the
// slash menu, /type, /style, /theme, and the "#"/":" completer. Each is a
// stateless struct; all live state is on the Model (m.list, plus the inline
// anchors m.slashStart/slashInline and m.compl).

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lflow/lflow/pkg/tui/database"
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
	case modeTagColor:
		return tagColorSource{}
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

// modRowFor returns the installed mod-node record behind a type key, so
// the picker knows which rows take the management chords.
func modRowFor(key string) (nodeMod, bool) {
	for _, gn := range loadedMods {
		if gn.Key == key {
			return gn, true
		}
	}
	return nodeMod{}, false
}

// items lists the pickable types. Mod rows LEAD the list — enabled then
// disabled, each wearing its management hint (space toggles the .disabled
// filename suffix, ctrl+d deletes the file) — so they and their hints are
// always inside the picker's scroll window, not buried below the built-ins.
func (typeSource) items(m *Model, q string) []pickerItem {
	var mods, builtins []pickerItem
	for _, t := range m.filteredTypes(q) {
		t := t
		if _, isGen := modRowFor(t); isGen {
			mods = append(mods, pickerItem{value: t, render: func(bool) string {
				return cFG + fmt.Sprintf("%-14s", typeLabel(t)) + cDim + " mod · space disable · ctrl+d uninstall" + cReset
			}})
			continue
		}
		builtins = append(builtins, pickerItem{label: typeLabel(t), value: t})
	}
	lq := strings.ToLower(q)
	for _, gn := range loadedMods {
		if gn.Enabled {
			continue
		}
		gn := gn
		if lq != "" && !fuzzyMatch(strings.ToLower(gn.Label), lq) && !fuzzyMatch(gn.Key, lq) {
			continue
		}
		mods = append(mods, pickerItem{value: gn.Key, render: func(bool) string {
			return cDim + fmt.Sprintf("%-14s", gn.Label) + "mod · disabled · space enable" + cReset
		}})
	}
	return append(mods, builtins...)
}

// onKey claims the management chords on mod rows: space toggles
// enabled/disabled in place, ctrl+d uninstalls. Built-in rows ignore both.
func (typeSource) onKey(m *Model, p *listPicker, key string, items []pickerItem) bool {
	if p.sel < 0 || p.sel >= len(items) {
		return false
	}
	gn, isGen := modRowFor(items[p.sel].value)
	if !isGen {
		return false
	}
	switch key {
	case " ":
		setNodeModEnabled(gn.Key, !gn.Enabled)
		return true
	case "ctrl+d":
		deleteNodeMod(gn.Key)
		if n := len(typeSource{}.items(m, p.query)); p.sel >= n && p.sel > 0 {
			p.sel--
		}
		return true
	}
	return false
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

func (s typeSource) initialSel(m *Model) int {
	cur := m.cursorItem()
	if cur == nil {
		return 0
	}
	// index into the DISPLAYED order (mods lead — see items), not filteredTypes
	for i, it := range s.items(m, "") {
		if it.value == cur.typ {
			return i
		}
	}
	return 0
}

func (typeSource) onSelect(m *Model, it pickerItem) (tea.Model, tea.Cmd) {
	// picking a disabled mod node re-enables it on the way — Enter means "use it"
	if gn, ok := modRowFor(it.value); ok && !gn.Enabled {
		setNodeModEnabled(gn.Key, true)
	}
	if it.value != "" {
		targets := m.selectedItems() // multi-select: retype the whole range
		if len(targets) == 0 {
			if cur := m.cursorItem(); cur != nil {
				targets = []*item{cur}
			}
		}
		if len(targets) > 0 {
			m.pushUndo("")
			for _, t := range targets {
				// re-picking Todo on a Todo toggles back to Bullet (the default)
				if it.value == database.TypeTodo && t.typ == database.TypeTodo {
					t.typ = database.TypeBullets
				} else {
					t.typ = it.value
				}
			}
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
	ql := strings.ToLower(q)
	out := make([]pickerItem, 0, len(stylePickerItems))
	for _, sp := range stylePickerItems {
		sp := sp
		if ql != "" && !fuzzyMatch(strings.ToLower(stylePickerLabels[sp.value]), ql) && !fuzzyMatch(sp.value, ql) {
			continue
		}
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

func (styleSource) header(m *Model, p *listPicker) string {
	if m.selOn {
		// a multi-select styles whole nodes; painting a text portion needs a
		// single node, so p is not offered
		return " " + cDim + "enter apply to selection" + cReset
	}
	// p paints a portion only while nothing is typed (see onKey); once a search
	// query starts, p is a filter rune, so the hint drops away.
	if p.query != "" {
		return " " + cDim + "style: " + cReset + cFG + p.query + cReset
	}
	return " " + cDim + "enter apply to all · p paint a portion · type to filter" + cReset
}

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
	targets := m.selectedItems() // multi-select: restyle the whole range
	if len(targets) == 0 {
		if cur := m.cursorItem(); cur != nil {
			targets = []*item{cur}
		}
	}
	if len(targets) > 0 && it.value != "" {
		m.pushUndo("")
		for _, sp := range stylePickerItems {
			if sp.value != it.value {
				continue
			}
			for _, t := range targets {
				if sp.kind == "toggle" {
					t.style = styleToggle(t.style, sp.value)
				} else {
					t.style = styleSetColor(t.style, sp.value)
				}
			}
			break
		}
		m.unsaved = true
	}
	m.mode = modeOutline
	return m, nil
}

// onKey: p inside /style takes the HIGHLIGHTED style into the painter — a
// window over the node's text picks where that style lands (see paint.go).
// Not with a multi-select: painting targets one node's text. Only while the
// search query is empty, so a typed filter (e.g. "purple") keeps every rune.
func (styleSource) onKey(m *Model, p *listPicker, key string, items []pickerItem) bool {
	if key == "p" && !m.selOn && p.query == "" {
		value := ""
		if p.sel >= 0 && p.sel < len(items) {
			value = items[p.sel].value
		}
		m.enterPaint(value)
		return true
	}
	return false
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
			// theme is a DB-backed setting now; setSetting applies + persists it
			m.setSetting("theme", t.name)
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
