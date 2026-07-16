package editor

// Concrete pickerSource implementations for the Group-A shared listPicker: the
// slash menu, /type, /style, /theme, and the "#"/":" completer. Each is a
// stateless struct; all live state is on the Model (m.list, plus the inline
// anchors m.slashStart/slashInline and m.compl).

import (
	"fmt"
	"strings"
	"time"

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
	case modeInsert:
		return insertSource{}
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

// --- /insert ---------------------------------------------------------------

// insertKinds lists the chip kinds the /insert picker offers; value is the bare
// kind handed to insertChip.
var insertKinds = []struct{ value, label, desc string }{
	{"agent", "agent", "a coding-agent session chip (Claude Code, Pi)"},
	{"cmd", "bash", "a runnable $ command chip"},
	{"date", "date", "today as a date chip"},
	{"link", "link", "a link chip"},
	{"path", "file", "a file path chip"},
	{"tag", "tag", "a #tag chip"},
}

type insertSource struct{}

func (insertSource) items(m *Model, q string) []pickerItem {
	ql := strings.ToLower(q)
	var out []pickerItem
	for _, k := range insertKinds {
		k := k
		if ql != "" && !fuzzyMatch(k.label, ql) && !fuzzyMatch(strings.ToLower(k.desc), ql) {
			continue
		}
		out = append(out, pickerItem{value: k.value, render: func(bool) string {
			return cFG + fmt.Sprintf("%-6s", k.label) + cDim + " " + k.desc + cReset
		}})
	}
	return out
}

func (insertSource) header(*Model, *listPicker) string { return "" }
func (insertSource) initialSel(*Model) int             { return 0 }

func (insertSource) onSelect(m *Model, it pickerItem) (tea.Model, tea.Cmd) {
	m.mode = modeOutline
	if it.value == "" {
		return m, nil
	}
	return m.insertChip(it.value)
}

// insertChip splices a chip of the given kind at the caret, reusing each kind's
// native flow: the "#" completer, the "[[" finder, the fzf file picker; date
// lands today directly, cmd opens a "$" draft that the double-space rule turns
// into the runnable chip.
func (m *Model) insertChip(kind string) (tea.Model, tea.Cmd) {
	cur := m.cursorItem()
	if cur == nil {
		return m, nil
	}
	mc := m.mirrorContext()
	if !mc.editable || !typeOf(cur.typ).inlineEditable || cur.readonly {
		m.flash = "node is not editable"
		return m, nil
	}
	if !chipsEnabled(cur) {
		m.flash = "chips are disabled for this node type"
		return m, nil
	}
	switch kind {
	case "tag":
		return m.openCompleter(cur, complTag, "#")
	case "link":
		m.openFinder(actLinkInsert)
	case "path":
		if cmd := m.openFilePicker(cur, ""); cmd != nil {
			return m, cmd
		}
		m.flash = "fzf not installed"
	case "date":
		if anchor := m.createChip(chipKindDate, time.Now().Format("2006-01-02")); anchor != "" {
			m.insertLiteralAt(cur, m.caret, anchor)
		}
	case "cmd":
		m.insertLiteralAt(cur, m.caret, "$")
		m.markCmdDraft(cur)
		m.flash = "type the command · double space lands the $ chip"
	case "agent":
		// a single coding-agent session chip — the provider (Claude Code, Pi) is
		// a variation set in its editor, not a separate entry.
		m.insertSessionChip()
	}
	return m, nil
}

// --- /type -----------------------------------------------------------------

type typeSource struct{}

// items lists the pickable types.
func (typeSource) items(m *Model, q string) []pickerItem {
	var out []pickerItem
	for _, t := range m.filteredTypes(q) {
		it := pickerItem{label: typeLabel(t), value: t}
		// a type whose CLI dependency is missing stays listed but disabled:
		// greyed out here, refused with "Missing dependency" on select
		if bin, missing := m.typeDepMissing(t); missing {
			label := it.label
			it.render = func(bool) string {
				return cDim + label + " · missing " + bin + cReset
			}
		}
		out = append(out, it)
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

func (s typeSource) initialSel(m *Model) int {
	cur := m.cursorItem()
	if cur == nil {
		return 0
	}
	for i, it := range s.items(m, "") {
		if it.value == cur.typ {
			return i
		}
	}
	return 0
}

func (typeSource) onSelect(m *Model, it pickerItem) (tea.Model, tea.Cmd) {
	// a disabled (dep-missing) type refuses the pick with the run-time error
	if bin, missing := m.typeDepMissing(it.value); it.value != "" && missing {
		m.mode = modeOutline
		m.flash = "Missing dependency: " + bin
		return m, nil
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
				if t.readonly || t.mirrorOf != "" {
					continue
				}
				// re-picking Todo on a Todo toggles back to Bullet (the default)
				if it.value == database.TypeTodo && t.typ == database.TypeTodo {
					t.typ = database.TypeBullets
				} else {
					t.typ = it.value
				}
			}
			m.unsaved = true
		}
		// picking Code on the single cursor node drops straight into its editor —
		// typing is the whole point, and the block has no inline surface.
		if cur := m.cursorItem(); it.value == database.TypeCode && len(targets) == 1 && targets[0] == cur {
			if v := nodeViewOf(cur); v != nil && v.Enter(m, cur) {
				m.focused = true
				m.focusScroll = 0
			}
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
				if t.readonly || t.mirrorOf != "" {
					continue
				}
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
		pi := pickerItem{label: it.label, value: it.value, desc: it.desc}
		// an agent whose CLI backend is missing stays listed but disabled:
		// greyed here, refused with "Missing dependency" on pick
		if m.compl.kind == complAgent {
			if a, ok := m.agentByName(it.value); ok {
				if bin, missing := m.agentDepMissing(a); missing {
					label := pi.label
					pi.render = func(bool) string {
						return cDim + label + " · missing " + bin + cReset
					}
				}
			}
		}
		out = append(out, pi)
	}
	return out
}

func (completerSource) header(*Model, *listPicker) string { return "" }
func (completerSource) initialSel(*Model) int             { return 0 }

func (completerSource) onSelect(m *Model, it pickerItem) (tea.Model, tea.Cmd) {
	if m.applyCompletion(m.cursorItem(), it) {
		m.mode = modeComplete // :type: immediately offers its values
		return m, nil
	}
	// :in: replaces the inline completer with the node finder.
	if m.mode == modeFinder {
		return m, nil
	}
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
	// Typing the command rather than picking it reaches the same chained value
	// picker as Enter on :type:; the caret is already immediately after the colon.
	if m.compl.kind == complQueryCmd && strings.EqualFold(p.query, "type:") {
		m.compl = complState{kind: complQueryType, start: m.caret}
		p.query = ""
		p.sel = 0
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
