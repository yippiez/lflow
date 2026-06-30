package editor

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/lflow/lflow/pkg/tui/database"
)

// chipSpec is the per-kind CREATION descriptor — the single place a chip kind's
// trigger gating, its "@" menu entry, and its creator are declared. It is the
// creation-side companion to the appearance registry (chipKinds in chip.go):
// chipKinds says how a chip LOOKS, chipSpecs say how a chip is MADE.
//
// Before this, each kind's gating and creator were special-cased across editor.go
// (the ">", "[[", "#" key handlers), file.go, link.go and complete.go. A new chip
// kind now adds one entry here (mirroring the nodeType registry in registry.go),
// instead of editing every trigger site.
type chipSpec struct {
	kind    string                                         // chipKind* constant
	menu    string                                         // "@" picker label, e.g. "file"
	desc    string                                         // "@" picker hint
	marker  string                                         // glyph shown in the picker, matching the chip's display marker
	allowOn func(typ string) bool                          // node types that may insert this kind
	create  func(m *Model, cur *item) (tea.Model, tea.Cmd) // open the kind's picker/completer/input; nil → not offered via "@"
}

// textChip reports whether a node type takes the rich text chips (path/link):
// text-ish nodes get them; types where the trigger char is real syntax (bash
// redirect, code, query, quote, json) keep it literal.
func textChip(typ string) bool {
	switch typ {
	case database.TypeBash, database.TypeCode, database.TypeQuery, database.TypeQuote, database.TypeJSON:
		return false
	}
	return typeOf(typ).inlineEditable
}

// notCodeChip reports whether a node type takes tag/date chips: every editable
// type except bash and code, where "#" is a comment.
func notCodeChip(typ string) bool {
	switch typ {
	case database.TypeBash, database.TypeCode:
		return false
	}
	return typeOf(typ).inlineEditable
}

// chipSpecs is the ordered creation registry; the "@" picker shows it in this
// order. A kind with a nil create is reachable only via its legacy trigger (cmd
// via "$…  ", date via ctrl+t) until it grows a picker.
var chipSpecs = []chipSpec{
	{
		kind: chipKindPath, menu: "file", desc: "insert a file path (fuzzy fzf picker)", marker: "›",
		allowOn: textChip,
		create:  func(m *Model, cur *item) (tea.Model, tea.Cmd) { return m, m.openFilePicker(cur) },
	},
	{
		kind: chipKindLink, menu: "link", desc: "link to a node or URL", marker: "→",
		allowOn: textChip,
		create: func(m *Model, cur *item) (tea.Model, tea.Cmd) {
			m.openFinder(actLinkInsert)
			return m, nil
		},
	},
	{
		kind: chipKindTag, menu: "tag", desc: "tag this node", marker: "#",
		allowOn: notCodeChip,
		create:  func(m *Model, cur *item) (tea.Model, tea.Cmd) { return m.openCompleter(cur, complTag, "#") },
	},
	// A date chip has NO "@" entry and no trigger char: it is recognised from a
	// typed phrase / ctrl+t (see date.go) and is intentionally left out of this
	// menu. The entry stays here (create nil) only to document that exclusion.
	{
		kind: chipKindDate, menu: "date", desc: "(typed phrase / ctrl+t)", marker: "◷",
		allowOn: notCodeChip,
	},
	{
		kind: chipKindCmd, menu: "cmd", desc: "inline shell command (alt+r runs)", marker: "$",
		allowOn: func(typ string) bool { return typ != database.TypeBash && typeOf(typ).inlineEditable },
		// opens the command input; Enter splices a cmd chip (see complCmd).
		create: func(m *Model, cur *item) (tea.Model, tea.Cmd) { return m.openCompleter(cur, complCmd, "$") },
	},
}

var chipSpecByKind = func() map[string]chipSpec {
	m := make(map[string]chipSpec, len(chipSpecs))
	for _, s := range chipSpecs {
		m[s.kind] = s
	}
	return m
}()

// anyChipAllowed reports whether any creatable chip kind is offered on a node
// type — the guard that decides whether "@" opens the chip menu there.
func anyChipAllowed(typ string) bool {
	for _, s := range chipSpecs {
		if s.create != nil && (s.allowOn == nil || s.allowOn(typ)) {
			return true
		}
	}
	return false
}
