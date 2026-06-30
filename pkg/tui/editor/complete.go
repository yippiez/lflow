package editor

import (
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// An inline completer is the popup behind the "@" chip menu and its sub-entries
// (@tag picks a tag, @cmd types a command) and behind ":" query commands. Like the
// slash menu it types its trigger + query into the node text and shows a filtered
// list above the status bar; Enter completes, Esc leaves the typed text literal.
// The kinds differ only in their item source and what Enter does, so they share
// this one mode.

type complKind int

const (
	complTag      complKind = iota // tag: pick an existing tag (reached via @tag)
	complQueryCmd                  // ":": pick a query time command (query nodes only)
	complChipMenu                  // "@": pick a chip kind to insert (file/link/tag/cmd)
	complCmd                       // command entry, reached via @cmd: type a command, insert a cmd chip
)

// complState is the live completer: where its trigger sits in the name, the
// query typed after it, and the highlighted row.
type complState struct {
	kind  complKind
	start int // rune index of the trigger char in cur.name
	query string
	sel   int
}

// complItem is one row: label is shown, value is what completion inserts (the
// tag word, or the full ":after:" token), desc is an optional dim hint.
type complItem struct {
	label, value, desc string
}

// queryCmdItems is the fixed menu for ":" in a query node — the time filters the
// query matcher understands (see querytime.go).
var queryCmdItems = []complItem{
	{label: ":after:", value: ":after:", desc: "dated/created on or after"},
	{label: ":before:", value: ":before:", desc: "dated/created on or before"},
	{label: ":since:", value: ":since:", desc: "alias of :after:"},
	{label: ":until:", value: ":until:", desc: "alias of :before:"},
}

// existingTags is every distinct tag in the outline, sorted. It unions the chip
// store (every tag chip across the whole DB — LoadChips is global) with the
// legacy plain-text "#word" tags still in the loaded nodes' names, so a tag
// shows up whether or not it has been backfilled into a chip yet.
func (m *Model) existingTags() []string {
	set := map[string]bool{}
	for _, c := range m.chips {
		if c.Kind == chipKindTag && c.Value != "" {
			set[c.Value] = true
		}
	}
	if m.tree != nil {
		cur := m.cursorItem() // skip the node being typed, so its in-progress "#be" is not a tag
		for _, it := range m.tree.byUUID {
			if it == cur {
				continue
			}
			for _, t := range tagsIn(it.name) {
				set[t] = true
			}
		}
	}
	out := make([]string, 0, len(set))
	for t := range set {
		out = append(out, t)
	}
	sort.Strings(out)
	return out
}

// complItems is the filtered list for the live completer.
func (m *Model) complItems() []complItem {
	switch m.compl.kind {
	case complChipMenu:
		return m.chipMenuItems()
	case complCmd:
		return m.cmdComplItems()
	}
	q := strings.ToLower(m.compl.query)
	var src []complItem
	if m.compl.kind == complQueryCmd {
		src = queryCmdItems
	} else {
		for _, t := range m.existingTags() {
			src = append(src, complItem{label: "#" + t, value: t})
		}
	}
	if q == "" {
		return src
	}
	var ret []complItem
	for _, it := range src {
		if strings.Contains(strings.ToLower(it.label), q) {
			ret = append(ret, it)
		}
	}
	return ret
}

// chipMenuItems is the "@" picker: every chip kind that has a creator and is
// allowed on the cursor node's type, filtered by what's typed after the "@".
func (m *Model) chipMenuItems() []complItem {
	typ := ""
	if cur := m.cursorItem(); cur != nil {
		typ = cur.typ
	}
	q := strings.ToLower(strings.TrimSpace(m.compl.query))
	var out []complItem
	for _, s := range chipSpecs {
		if s.create == nil || (s.allowOn != nil && !s.allowOn(typ)) {
			continue
		}
		if q != "" && !strings.Contains(strings.ToLower(s.menu), q) {
			continue
		}
		out = append(out, complItem{label: s.marker + " @" + s.menu, value: s.kind, desc: s.desc})
	}
	return out
}

// cmdComplItems is the @cmd input's single live row: a preview of the command
// typed so far. Enter on it splices the cmd chip; an empty input offers nothing.
func (m *Model) cmdComplItems() []complItem {
	cmd := strings.TrimSpace(m.compl.query)
	if cmd == "" {
		return nil
	}
	return []complItem{{label: "$ " + cmd, value: cmd, desc: "enter to insert · alt+r runs it"}}
}

// openCompleter types the trigger into the node and switches to the completer.
func (m *Model) openCompleter(cur *item, kind complKind, trigger string) (tea.Model, tea.Cmd) {
	runes := []rune(cur.name)
	m.boundCaret(len(runes))
	cur.name = string(runes[:m.caret]) + trigger + string(runes[m.caret:])
	m.compl = complState{kind: kind, start: m.caret}
	m.caret += len([]rune(trigger))
	m.mode = modeComplete
	m.unsaved = true
	return m, nil
}

// applyCompletion replaces the typed "trigger+query" with the chosen result: a
// tag chip, or a query-command token. With nothing highlighted, a tag falls back
// to the typed word (so a brand-new tag still commits) and a query command no-ops.
func (m *Model) applyCompletion(cur *item, items []complItem) {
	if cur == nil {
		return
	}
	runes := []rune(cur.name)
	end := m.caret
	if end > len(runes) {
		end = len(runes)
	}
	if m.compl.kind == complQueryCmd {
		if len(items) == 0 || m.compl.sel >= len(items) {
			return // leave the typed text as-is
		}
		token := items[m.compl.sel].value
		cur.name = string(runes[:m.compl.start]) + token + string(runes[end:])
		m.caret = m.compl.start + len([]rune(token))
		m.unsaved = true
		return
	}
	if m.compl.kind == complCmd {
		cmd := strings.TrimSpace(m.compl.query)
		if cmd == "" {
			return // empty input: leave the "$" literal
		}
		m.replaceRangeWithChip(cur, m.compl.start, end, chipKindCmd, cmd)
		return
	}
	// tag: the highlighted tag, else the typed word (new tag)
	tag := strings.TrimSpace(m.compl.query)
	if len(items) > 0 && m.compl.sel < len(items) {
		tag = items[m.compl.sel].value
	}
	if tag == "" {
		return
	}
	anchor := m.createChip(chipKindTag, tag)
	if anchor == "" {
		return
	}
	cur.name = string(runes[:m.compl.start]) + anchor + string(runes[end:])
	m.caret = m.compl.start + len([]rune(anchor))
	m.unsaved = true
}

// applyChipMenu commits an "@" picker choice: it strips the typed "@query" and
// then runs the chosen chip kind's creator (which opens its picker/completer/input
// and sets the next mode itself). With nothing highlighted it just drops the text.
func (m *Model) applyChipMenu(cur *item, items []complItem) (tea.Model, tea.Cmd) {
	m.mode = modeOutline
	if cur != nil {
		runes := []rune(cur.name)
		end := m.caret
		if end > len(runes) {
			end = len(runes)
		}
		cur.name = string(runes[:m.compl.start]) + string(runes[end:])
		m.caret = m.compl.start
		m.unsaved = true
	}
	if cur == nil || len(items) == 0 || m.compl.sel >= len(items) {
		return m, nil
	}
	if s, ok := chipSpecByKind[items[m.compl.sel].value]; ok && s.create != nil {
		return s.create(m, cur)
	}
	return m, nil
}

// delCharBeforeCaret removes one rune left of the caret (the completer's
// backspace, which keeps the typed text and the popup in sync).
func (m *Model) delCharBeforeCaret(cur *item) {
	if cur == nil || m.caret <= 0 {
		return
	}
	runes := []rune(cur.name)
	if m.caret > len(runes) {
		m.caret = len(runes)
	}
	cur.name = string(runes[:m.caret-1]) + string(runes[m.caret:])
	m.caret--
	m.unsaved = true
}

func (m *Model) handleCompleteKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	cur := m.cursorItem()
	items := m.complItems()
	switch k.String() {
	case "esc":
		m.mode = modeOutline // leave the typed trigger+query as literal text
		return m, nil
	case "up":
		if m.compl.sel > 0 {
			m.compl.sel--
		}
		return m, nil
	case "down":
		if m.compl.sel < len(items)-1 {
			m.compl.sel++
		}
		return m, nil
	case "enter", "tab":
		if m.compl.kind == complChipMenu {
			return m.applyChipMenu(cur, items) // the creator sets the next mode
		}
		m.applyCompletion(cur, items)
		m.mode = modeOutline
		return m, nil
	case "backspace":
		if qr := []rune(m.compl.query); len(qr) > 0 {
			m.compl.query = string(qr[:len(qr)-1])
			m.compl.sel = 0
			m.delCharBeforeCaret(cur)
		} else {
			if m.caret > m.compl.start {
				m.delCharBeforeCaret(cur) // remove the trigger char (none for @date)
			}
			m.mode = modeOutline
		}
		return m, nil
	}

	if k.Type == tea.KeySpace && !k.Alt && m.compl.kind == complCmd {
		// a shell command carries spaces ("ls -la"); keep the completer open and
		// treat the space as a query character.
		m.compl.query += " "
		m.compl.sel = 0
		if cur != nil {
			runes := []rune(cur.name)
			m.boundCaret(len(runes))
			cur.name = string(runes[:m.caret]) + " " + string(runes[m.caret:])
			m.caret++
			m.unsaved = true
		}
		return m, nil
	}
	if k.Type == tea.KeySpace && !k.Alt {
		// space ends the completer. A tag commits the typed "#word" into a chip
		// (the long-standing fast path); a query command just leaves "·query"
		// literal. Either way the space is then typed normally.
		m.mode = modeOutline
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
		return m, nil
	}
	if k.Type == tea.KeyRunes && !k.Alt {
		m.compl.query += string(k.Runes)
		m.compl.sel = 0
		if cur != nil {
			runes := []rune(cur.name)
			m.boundCaret(len(runes))
			ins := []rune(string(k.Runes))
			cur.name = string(runes[:m.caret]) + string(ins) + string(runes[m.caret:])
			m.caret += len(ins)
			m.unsaved = true
		}
	}
	return m, nil
}
