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

	"github.com/lflow/lflow/tui/database"
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
	case modeAgentPick:
		return agentStartSource{}
	case modeAgentColor:
		return agentColorSource{}
	case modeCharacterPick:
		return characterSource{}
	case modeCharacterColor:
		return characterColorSource{}
	case modeCite:
		return zoteroSource{}
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

// header shows a hint when the query has no matches: the menu STAYS open on a
// dead-end query so backspace can trim it back to something that matches —
// closing on no match left the typed "/query" as stranded literal text with
// no way back into the menu. Enter commits the literal text; esc strips it.
func (slashSource) header(m *Model, p *listPicker) string {
	if p.query != "" && len(m.filteredSlash(p.query)) == 0 {
		return " " + cDim + "no matches · enter keeps the text · esc cancels" + cReset
	}
	return ""
}

func (slashSource) initialSel(*Model) int { return 0 }

func (slashSource) onSelect(m *Model, it pickerItem) (tea.Model, tea.Cmd) {
	if it.value == "" {
		m.mode = modeOutline
		return m, nil
	}
	m.stripSlashText()
	return m.runSlash(it.value)
}

// onEscape takes back the "/query" the menu mirrored into the node, so a
// dismissed menu leaves the node reading exactly as it did before it opened.
func (slashSource) onEscape(m *Model) { m.stripSlashText() }

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
	// a dead-end query never closes the menu: it stays open on the "no matches"
	// hint so backspace trims the query back to something that matches (the
	// header carries the keep/cancel affordances)
	return false
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

// insertKinds lists the kinds the /insert picker offers; value is the bare
// kind handed to insertChip. "icon" is plain unicode (not a chip) — offered so
// query nodes (where ":" is the query-command completer) can still insert icons.
var insertKinds = []struct{ value, label, desc string }{
	{"agent", "Agent", "a coding session you already have"},
	{"zotero", "Zotero", "a citation from your Zotero library (or type @@)"},
	{"cmd", "Bash", "a runnable $ command chip (or type $$)"},
	{"date", "Date", "today as a date chip"},
	{"icon", "Icon", "an icon or emoji via shortcode"},
	{"link", "Link", "a link chip"},
	{"molecule", "Molecule", "a ⌬ molecule chip"},
	{"tag", "Tag", "a #tag chip"},
}

// insertLabelWidth is the label column in /insert: one wider than the longest
// label, so the descriptions line up whatever a kind ends up being called.
var insertLabelWidth = func() int {
	w := 0
	for _, k := range insertKinds {
		if n := len([]rune(k.label)); n > w {
			w = n
		}
	}
	return w + 1
}()

type insertSource struct{}

// items filters the kinds by NAME first: a name match ranks above a kind that
// only mentions the query in its description, so typing a chip's own name lands
// on that chip. The description is matched as a plain substring rather than
// fuzzily — subsequence matching over a whole sentence hits nearly everything
// ("mol" is in "a citation fro-m y-o-ur zotero -l-ibrary"), which is how a
// query for one kind used to select another.
func (insertSource) items(m *Model, q string) []pickerItem {
	ql := strings.ToLower(q)
	var byName, byDesc []pickerItem
	for _, k := range insertKinds {
		k := k
		desc := k.desc
		// when the caret already sits after a molecule, the entry offers to fold
		// THAT notation into a chip instead of landing an empty one — the detection
		// is what turns a pasted SMILES into a one-key conversion.
		if k.value == chipKindMol {
			if tok, _, _, ok := molTokenBeforeCaret(m.cursorItem(), m.caret); ok {
				desc = "convert " + strings.TrimSpace(molChipDisplay(tok))
			}
		}
		it := pickerItem{value: k.value, render: func(bool) string {
			return cFG + fmt.Sprintf("%-*s", insertLabelWidth, k.label) + cDim + " " + desc + cReset
		}}
		switch {
		case ql == "" || fuzzyMatch(strings.ToLower(k.label), ql):
			byName = append(byName, it)
		case strings.Contains(strings.ToLower(k.desc), ql):
			byDesc = append(byDesc, it)
		}
	}
	return append(byName, byDesc...)
}

// header names the picker and echoes the query, the same shape /type wears —
// the two pickers are the same gesture ("what goes here?"), so they read alike.
func (insertSource) header(_ *Model, p *listPicker) string {
	query := p.query
	if query == "" {
		query = cDim + "type to search" + cReset
	} else {
		query = cFG + query + cReset
	}
	return " " + cDim + "insert: " + cReset + query
}

func (insertSource) initialSel(*Model) int { return 0 }

func (insertSource) onSelect(m *Model, it pickerItem) (tea.Model, tea.Cmd) {
	m.mode = modeOutline
	if it.value == "" {
		m.finishRichPicker()
		return m, nil
	}
	return m.insertChip(it.value)
}

// insertChip splices a chip of the given kind at the caret, reusing each kind's
// native flow: the "#" completer, the "[[" finder; date lands today directly,
// cmd opens a "$" draft that the double-space rule turns into the runnable
// chip. "icon" is plain unicode via the shortcode completer and skips the
// chipsEnabled guard so query nodes can reach it.
func (m *Model) insertChip(kind string) (tea.Model, tea.Cmd) {
	cur := m.cursorItem()
	if cur == nil {
		return m, nil
	}
	mc := m.mirrorContext()
	if !mc.editable || !typeOf(cur.typ).inlineEditable || cur.readonly {
		m.errorFlash("node is not editable")
		return m, nil
	}
	if kind == "icon" {
		return m.openIconPicker(cur)
	}
	if !chipsEnabled(cur) {
		m.errorFlash("chips are disabled for this node type")
		return m, nil
	}
	switch kind {
	case "agent":
		// search the sessions the CLIs already have; the chip lands on select.
		// This is the ONLY way in: a session chip is one of the things /insert
		// splices, not a command of its own. The command it returns is what pumps
		// the store scan filling the list.
		return m, m.openAgentPicker()
	case "tag":
		return m.openCompleter(cur, complTag, "#")
	case "zotero":
		// a citation chip at the caret, like every other thing on this menu —
		// /insert splices something INTO a node. Turning the node into the whole
		// paper is a change of what the node IS, so it lives on /type → Zotero.
		return m.openCitePicker(citeChip)
	case "link":
		m.openFinder(actLinkInsert)
	case "date":
		if anchor := m.createChip(chipKindDate, time.Now().Format("2006-01-02")); anchor != "" {
			m.insertLiteralAt(cur, m.caret, anchor)
		}
	case "cmd":
		// an empty $ chip, filled in with alt+i — the same shape "$$" lands by
		// typing. There is no draft: a "$" is literal everywhere now, so /insert
		// and "$$" are the two ways a bash chip forms.
		if anchor := m.createChip(chipKindBash, ""); anchor != "" {
			m.insertLiteralAt(cur, m.caret, anchor)
			m.flash = "empty $ chip · alt+i edits the command"
		}
	case chipKindMol:
		// convert the detected notation in place, else land an empty chip to fill in
		if m.molConvertBeforeCaret(cur) {
			m.flash = "molecule → ⌬ chip"
		} else if anchor := m.createChip(chipKindMol, ""); anchor != "" {
			m.insertLiteralAt(cur, m.caret, anchor)
			m.flash = "empty ⌬ chip · no molecule found before the caret"
		}
	}
	// Agent/Zotero/tag/icon/link hand off to another picker and keep noteRich
	// until that nested surface settles. Immediate insertions return to /note.
	if m.noteRich && m.mode == modeOutline {
		m.finishRichPicker()
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
		m.errorFlash("Missing dependency: " + bin)
		return m, nil
	}
	// a Zotero item is only ever made by choosing WHICH entry it mirrors, so the
	// pick hands straight over to the library picker — which sets the type
	// itself once an entry is chosen, leaving nothing behind if you escape.
	if it.value == database.TypeZotero {
		m.mode = modeOutline
		return m.openCitePicker(citeMirror)
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
				// a mirror styles the node it shows, like every other content edit
				t = m.editTargetOf(t)
				if t == nil {
					continue
				}
				// re-picking Todo on a Todo toggles back to Bullet (the default)
				if it.value == database.TypeTodo && t.typ == database.TypeTodo {
					m.setNodeType(t, database.TypeBullets)
				} else {
					m.setNodeType(t, it.value)
				}
				// a type may want to land on its own face right away (the Table
				// folds to its grid) — one hook, no per-type branch here
				if h := typeOf(t.typ).onType; h != nil {
					h(m, t)
				}
			}
			m.unsaved = true
		}
		// picking Code on the single cursor node drops straight into its editor —
		// typing is the whole point, and the block has no inline surface.
		if cur := m.cursorItem(); it.value == database.TypeCode && len(targets) == 1 && targets[0] == cur {
			if v := nodeViewOf(cur); v != nil && v.enter(m, cur) {
				m.focused = true
				m.focusScroll = 0
			}
		}
	}
	// an onType hook may have moved the mode elsewhere on its own (the Line node's
	// character picker) — only fall back to the outline if it left mode alone.
	if m.mode == modeType {
		m.mode = modeOutline
	}
	return m, nil
}

// --- /style ----------------------------------------------------------------

type styleSource struct{}

func (styleSource) items(m *Model, q string) []pickerItem {
	cur := m.renderItem(m.cursorItem()) // a mirror shows the source's style
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
		return " " + cDim + "enter apply to selection" + cReset
	}
	if _, _, _, ok := m.textSelection(); ok {
		// a horizontal selection narrows the target to that run of the text
		return " " + cDim + "enter style the selected text" + cReset
	}
	if p.query != "" {
		return " " + cDim + "style: " + cReset + cFG + p.query + cReset
	}
	return " " + cDim + "enter to apply the first part · shift+←/→ styles a portion · type to filter" + cReset
}

func (styleSource) initialSel(m *Model) int {
	cur := m.renderItem(m.cursorItem())
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
	// a horizontal selection (shift+←/→) narrows the target to that run of the
	// node's text: the style lands on the selected words only, never the line
	if cur, lo, hi, ok := m.textSelection(); ok && it.value != "" {
		m.applyStyleToSpan(cur, lo, hi, it.value)
		m.clearTextSel()
		m.finishRichPicker()
		m.refreshRows()
		return m, nil
	}
	// with the note open and nothing selected, the style is for the NOTE — the
	// surface the caret is actually in. Styling the row from inside its note is
	// what made a note look like an echo of its node; the two are separate
	// surfaces with separate styling, and /style follows the caret.
	if cur := m.richTextItem(m.cursorItem()); m.richNoteActive() && cur != nil && it.value != "" {
		m.applyStyleToSpan(cur, 0, len([]rune(cur.note)), it.value) // it pushes its own undo
		m.finishRichPicker()
		m.refreshRows()
		return m, nil
	}
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
				// a mirror styles the node it SHOWS: the color belongs to the one
				// real node, so it lands everywhere that node appears
				t = m.editTargetOf(t)
				if t == nil {
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
	m.finishRichPicker()
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
		// icons: colored glyph + dim :shortcode (see iconRowRender)
		if m.compl.kind == complIcon {
			if e, ok := iconByShortcode(strings.TrimPrefix(it.label, ":")); ok {
				e := e
				pi.render = func(bool) string { return iconRowRender(e) }
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
	m.finishRichPicker()
	return m, nil
}

func (completerSource) onRune(m *Model, p *listPicker, r []rune) bool {
	// "#1" means "number one", not a tag: if the first char typed after a '#' is
	// not a tag letter, drop the completer and keep the text literal.
	if m.compl.kind == complTag && len(p.query) == 0 && len(r) > 0 && !isTagLetter(r[0]) {
		p.query = ""
		return true
	}
	p.query += string(r)
	p.sel = 0
	if cur := m.cursorItem(); cur != nil {
		runes := []rune(m.richText(cur))
		m.boundCaret(len(runes))
		ins := []rune(string(r))
		m.setRichText(cur, string(runes[:m.caret])+string(ins)+string(runes[m.caret:]))
		m.caret += len(ins)
	}
	// Typing the qualifier rather than picking it reaches the same chained value
	// picker as Enter on type:. Dropping the trigger colon first keeps hand-typed
	// text on the flat spelling; the caret then sits right after "type:".
	if key, done := typedQueryCmd(p.query); done && m.compl.kind == complQueryCmd {
		m.dropCompleterTrigger(m.cursorItem())
		p.query = ""
		p.sel = 0
		if key == "type" {
			m.compl = complState{kind: complQueryType, start: m.caret}
			return false
		}
		// Every other qualifier takes a free-form value (a date, a state word) —
		// there is nothing left to pick, so hand the keyboard back to the outline.
		m.mode = modeOutline
		return true
	}
	return false // the completer never auto-closes on typing (allows a brand-new tag)
}

func (completerSource) onSpace(m *Model, p *listPicker) bool {
	// space ends the completer. A tag commits the typed "#word" into a chip; a
	// query command leaves the text literal. Either way the space is typed normally.
	cur := m.cursorItem()
	if m.compl.kind == complTag && cur != nil {
		if m.noteRich {
			m.chipifyNoteBeforeCaret(cur)
		} else {
			m.chipifyBeforeCaret(cur)
		}
	}
	if cur != nil {
		runes := []rune(m.richText(cur))
		m.boundCaret(len(runes))
		m.setRichText(cur, string(runes[:m.caret])+" "+string(runes[m.caret:]))
		m.caret++
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
