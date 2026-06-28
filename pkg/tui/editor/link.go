package editor

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/lflow/lflow/pkg/browser"
)

// A link chip points at a node or a website. Its target is stored in the chip
// value — "lflow://node/<uuid>" for a node, a URL otherwise — and its arbitrary
// display name in the chip label. Create one with "[[" (or /link).

const nodeLinkScheme = "lflow://node/"

// nodeLinkURI builds a node link target from a uuid.
func nodeLinkURI(uuid string) string { return nodeLinkScheme + uuid }

// insertLinkChip splices a new link chip (target + name) in at the caret.
func (m *Model) insertLinkChip(value, label string) {
	cur := m.cursorItem()
	if cur == nil {
		return
	}
	m.pushUndo("")
	anchor := m.createLabeledChip(chipKindLink, value, label)
	if anchor == "" {
		return
	}
	runes := []rune(cur.name)
	m.boundCaret(len(runes))
	cur.name = string(runes[:m.caret]) + anchor + string(runes[m.caret:])
	m.caret += len([]rune(anchor))
	m.unsaved = true
}

// insertURLLink inserts a link chip pointing at a typed/pasted URL, its name
// defaulting to the host. Called from the [[ finder when the query is a URL.
func (m *Model) insertURLLink(raw string) (tea.Model, tea.Cmd) {
	m.mode = modeOutline
	url := browser.Normalize(raw)
	m.insertLinkChip(url, browser.Host(url))
	m.flash = "linked → " + clipStr(url, 32)
	m.refreshRows()
	return m, nil
}
