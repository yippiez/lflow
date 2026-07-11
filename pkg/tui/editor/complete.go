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

// complState is the completer's anchor: which trigger opened it and where that
// trigger sits in the name. The live query and highlighted row live on m.list
// (the shared listPicker), since the completer is now a Group-A pickerSource.
type complState struct {
	kind  complKind
	start int // rune index of the trigger char in cur.name
}

// complItem is one row: label is shown, value is what completion inserts (the
// tag word, or the full ":after:" token), desc is an optional dim hint.
type complItem struct {
	label, value, desc string
}

// queryCmdItems is the fixed menu for ":" in a query node — the filters and
// display flags the query matcher understands (see querytime.go).
var queryCmdItems = []complItem{
	{label: ":after:", value: ":after:", desc: "dated/created on or after"},
	{label: ":before:", value: ":before:", desc: "dated/created on or before"},
	{label: ":since:", value: ":since:", desc: "alias of :after:"},
	{label: ":until:", value: ":until:", desc: "alias of :before:"},
	{label: ":type:", value: ":type:", desc: "node type (todo, log, …)"},
	{label: ":breadcrumb:", value: ":breadcrumb:", desc: "group hits under path crumbs"},
	{label: ":list:", value: ":list:", desc: "flat hit list (default)"},
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

// complItems is the filtered list for the live completer, for the given query.
func (m *Model) complItems(query string) []complItem {
	q := strings.ToLower(query)
	var src []complItem
	switch m.compl.kind {
	case complQueryCmd:
		src = queryCmdItems
	case complAgent:
		// just the name — no descriptions in the mention picker
		for _, a := range m.agents {
			src = append(src, complItem{label: "@" + a.Name, value: a.Name})
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
	m.list = listPicker{searchable: true}
	m.caret += len([]rune(trigger))
	m.mode = modeComplete
	m.unsaved = true
	return m, nil
}

// applyCompletion replaces the typed "trigger+query" with the chosen result: a
// tag chip, or a query-command token. chosen is the highlighted row (a zero
// pickerItem when nothing is highlighted): a tag then falls back to the typed
// word (so a brand-new tag still commits) and a query command no-ops.
func (m *Model) applyCompletion(cur *item, chosen pickerItem) {
	if cur == nil {
		return
	}
	runes := []rune(cur.name)
	end := m.caret
	if end > len(runes) {
		end = len(runes)
	}
	if m.compl.kind == complQueryCmd {
		if chosen.value == "" {
			return // leave the typed text as-is
		}
		cur.name = string(runes[:m.compl.start]) + chosen.value + string(runes[end:])
		m.caret = m.compl.start + len([]rune(chosen.value))
		m.unsaved = true
		return
	}
	if m.compl.kind == complAgent {
		if chosen.value == "" {
			return // leave the typed text as-is
		}
		// a mention lands as an agent chip — the structured red @Name token that
		// binds the node to its agent; alt+r later starts the thread (agent.go)
		anchor := m.createChip(chipKindAgent, chosen.value)
		if anchor == "" {
			return
		}
		cur.name = string(runes[:m.compl.start]) + anchor + " " + string(runes[end:])
		m.caret = m.compl.start + len([]rune(anchor)) + 1
		m.unsaved = true
		return
	}
	// tag: the highlighted tag, else the typed word (new tag)
	tag := strings.TrimSpace(m.list.query)
	if chosen.value != "" {
		tag = chosen.value
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

// Completer key handling now lives in the shared listPicker (see picker_list.go)
// via completerSource in picker_sources.go; the completer's inline-text behavior
// is its inlineTextSource hooks.
