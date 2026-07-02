package editor

import (
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// An inline completer is the popup behind "#" (tags) and ":" (query commands).
// Like the slash menu it types its trigger + query into the node text and shows
// a filtered list above the status bar; Enter completes, Esc leaves the typed
// text literal. The two kinds differ only in their item source and what Enter
// inserts — a tag chip, or a query-command token — so they share this one mode.

type complKind int

const (
	complTag      complKind = iota // "#": pick an existing tag
	complQueryCmd                  // ":": pick a query time command (query nodes only)
	complAgent                     // "@": pick a configured agent to mention (see agent.go)
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
	q := strings.ToLower(m.compl.query)
	var src []complItem
	switch m.compl.kind {
	case complQueryCmd:
		src = queryCmdItems
	case complAgent:
		for _, a := range m.agents {
			kind := "agent"
			if a.Mock {
				kind = "agent · mock"
			}
			src = append(src, complItem{label: "@" + a.Name, value: a.Name, desc: kind + " — Enter on the node sends the thread"})
		}
	default:
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
	if m.compl.kind == complQueryCmd || m.compl.kind == complAgent {
		if len(items) == 0 || m.compl.sel >= len(items) {
			return // leave the typed text as-is
		}
		token := items[m.compl.sel].value
		if m.compl.kind == complAgent {
			token = "@" + token + " " // a mention stays plain text; Enter later sends
		}
		cur.name = string(runes[:m.compl.start]) + token + string(runes[end:])
		m.caret = m.compl.start + len([]rune(token))
		m.unsaved = true
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
		m.applyCompletion(cur, items)
		m.mode = modeOutline
		return m, nil
	case "backspace":
		if qr := []rune(m.compl.query); len(qr) > 0 {
			m.compl.query = string(qr[:len(qr)-1])
			m.compl.sel = 0
			m.delCharBeforeCaret(cur)
		} else {
			m.delCharBeforeCaret(cur) // remove the trigger itself
			m.mode = modeOutline
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
