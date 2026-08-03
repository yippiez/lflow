package editor

import (
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// An inline completer is the popup behind "#" (tags) and ":" (icons on normal
// nodes / query commands on query nodes). Like the slash menu
// it types into the node text and shows a filtered list above the status bar.
// Query commands may chain into a value picker — type: offers types, while
// in: opens the node finder. Icons land as plain unicode (see icon.go).

type complKind int

const (
	complTag       complKind = iota // "#": pick an existing tag
	complQueryCmd                   // ":": pick a query command (query nodes only)
	complQueryType                  // value picker immediately after :type:
	complIcon                       // ":": pick an icon shortcode (non-query; also /insert)
)

// complState is the completer's anchor: which trigger opened it and where that
// trigger sits in the name. The live query and highlighted row live on m.list
// (the shared listPicker), since the completer is now a Group-A pickerSource.
type complState struct {
	kind  complKind
	start int // rune index of the trigger char in cur.name
}

// complItem is one row: label is shown, value is what completion inserts (a
// tag/type word or a full ":after:" token), desc is an optional dim hint.
type complItem struct {
	label, value, desc string
}

// queryCmdItems is the fixed menu for ":" in a query node — the filters and
// display flags the query matcher understands (see querytime.go).
var queryCmdItems = []complItem{
	{label: "type()", value: "type()", desc: "node type (todo, log, …)"},
	{label: "in()", value: "in()", desc: "select subtree to search (root by default)"},
	{label: "after()", value: "after()", desc: "dated/created on or after"},
	{label: "before()", value: "before()", desc: "dated/created on or before"},
	{label: "since()", value: "since()", desc: "alias of after()"},
	{label: "until()", value: "until()", desc: "alias of before()"},
	{label: "is(starred)", value: "is(starred)", desc: "pinned with /star"},
	{label: "is(done)", value: "is(done)", desc: "completed todos"},
	{label: "is(open)", value: "is(open)", desc: "not completed"},
	{label: "has(note)", value: "has(note)", desc: "carries a note"},
	{label: "has(children)", value: "has(children)", desc: "has child nodes"},
	{label: "as(tree)", value: "as(tree)", desc: "nest hits in a locked ancestor tree"},
	{label: "as(list)", value: "as(list)", desc: "flat hit list (default)"},
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
	case complQueryType:
		for _, key := range typeOrder() {
			src = append(src, complItem{label: key, value: key, desc: typeLabel(key)})
		}
	case complIcon:
		// filterIcons already applies the query; label is :shortcode, value is the glyph.
		for _, e := range filterIcons(q) {
			src = append(src, complItem{label: ":" + e.shortcode, value: e.glyph})
		}
		return src
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

// applyCompletion replaces the active token. It returns true when selecting
// type: chained directly into its value picker, keeping modeComplete open;
// in: instead moves directly into the node finder.
func (m *Model) applyCompletion(cur *item, chosen pickerItem) (chain bool) {
	if cur == nil {
		return false
	}
	runes := []rune(cur.name)
	end := m.caret
	if end > len(runes) {
		end = len(runes)
	}
	if m.compl.kind == complQueryCmd {
		if chosen.value == "" {
			return false
		}
		cur.name = string(runes[:m.compl.start]) + chosen.value + string(runes[end:])
		m.caret = m.compl.start + len([]rune(chosen.value))
		// An empty "()" is a hole to fill, so the caret lands between the parens
		// rather than after them. A qualifier that already carries its value —
		// is(open) — leaves the caret at the end, ready for the next atom.
		if strings.HasSuffix(chosen.value, "()") {
			m.caret--
		}
		m.unsaved = true
		if strings.EqualFold(chosen.value, "type()") {
			m.compl = complState{kind: complQueryType, start: m.caret}
			m.list = listPicker{searchable: true}
			return true
		}
		if strings.EqualFold(chosen.value, "in()") {
			m.openFinder(actQueryScope)
		}
		return false
	}
	if m.compl.kind == complQueryType {
		if chosen.value == "" {
			return false
		}
		cur.name = string(runes[:m.compl.start]) + chosen.value + string(runes[end:])
		m.caret = m.compl.start + len([]rune(chosen.value))
		m.skipClosingParen(cur)
		m.unsaved = true
		return false
	}
	if m.compl.kind == complIcon {
		if chosen.value == "" {
			return false
		}
		// resolve the catalog entry: label is :shortcode, value is the glyph.
		e, ok := iconByShortcode(strings.TrimPrefix(chosen.label, ":"))
		if !ok {
			for _, ent := range iconCatalog {
				if ent.glyph == chosen.value {
					e, ok = ent, true
					break
				}
			}
		}
		insert := chosen.value
		// painted service icons become icon chips so the brand color survives
		// render (and a bare "Z" in prose stays uncolored). White/emoji stay
		// plain unicode. Query nodes disable chips — fall back to the glyph.
		if ok && iconIsPainted(e) && chipsEnabled(cur) {
			if anchor := m.createLabeledChip(chipKindIcon, e.glyph, e.shortcode); anchor != "" {
				insert = anchor
			}
		}
		// the replaced range is ":" + typed query.
		cur.name = string(runes[:m.compl.start]) + insert + string(runes[end:])
		m.caret = m.compl.start + len([]rune(insert))
		m.unsaved = true
		return false
	}
	tag := strings.TrimSpace(m.list.query)
	if chosen.value != "" {
		tag = chosen.value
	}
	if tag == "" {
		return false
	}
	anchor := m.createChip(chipKindTag, tag)
	if anchor == "" {
		return false
	}
	cur.name = string(runes[:m.compl.start]) + anchor + string(runes[end:])
	m.caret = m.compl.start + len([]rune(anchor))
	m.unsaved = true
	return false
}

// skipClosingParen steps the caret past a ")" sitting immediately after it, so
// completing a value inside "type(…)" leaves the caret ready for the next atom
// instead of stranded inside the brackets.
func (m *Model) skipClosingParen(cur *item) {
	runes := []rune(cur.name)
	if m.caret < len(runes) && runes[m.caret] == ')' {
		m.caret++
	}
}

// typedQueryCmd reports the qualifier a user has finished typing INTO the open
// completer — "type(" as well as the colon spellings — so the popup reacts the
// same way Enter on the matching row does.
func typedQueryCmd(typed string) (string, bool) {
	if key, ok := strings.CutSuffix(strings.ToLower(typed), "("); ok && isQualifierKey(key) {
		return key, true
	}
	key, _, ok := splitQualifier(strings.ToLower(typed))
	if !ok || !strings.HasSuffix(typed, ":") {
		return "", false
	}
	return key, true
}

// dropCompleterTrigger removes the ":" that opened the completer. Typing a
// qualifier out by hand must land the same "type(" text that picking it from the
// popup does, not a stray colon in front of it.
func (m *Model) dropCompleterTrigger(cur *item) {
	runes := []rune(cur.name)
	at := m.compl.start
	if cur == nil || at < 0 || at >= len(runes) || runes[at] != ':' {
		return
	}
	cur.name = string(runes[:at]) + string(runes[at+1:])
	if m.caret > at {
		m.caret--
	}
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
