package editor

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/lflow/lflow/pkg/browser"
	"github.com/lflow/lflow/pkg/tui/database"
)

// A link chip points at a node or a website. Its target is stored in the chip
// value — "lflow://node/<uuid>" for a node, a URL otherwise — and its arbitrary
// display name in the chip label. Create one with @link (or /link), follow it with
// alt+g.

const nodeLinkScheme = "lflow://node/"

// nodeLinkURI builds a node link target from a uuid.
func nodeLinkURI(uuid string) string { return nodeLinkScheme + uuid }

// nodeLinkUUID returns the uuid a node-link target points at, or ok=false for a
// URL target.
func nodeLinkUUID(value string) (string, bool) {
	if strings.HasPrefix(value, nodeLinkScheme) {
		return strings.TrimPrefix(value, nodeLinkScheme), true
	}
	return "", false
}

// linkChipAtCaret returns the link chip the caret sits on (its anchor begins at
// the caret, or ends exactly at it), or ok=false.
func (m *Model) linkChipAtCaret(cur *item) (database.Chip, bool) {
	return m.chipAtCaret(cur, chipKindLink)
}

// followLink acts on a link chip: a node target jumps the editor there, a URL
// opens in the browser.
func (m *Model) followLink(c database.Chip) (tea.Model, tea.Cmd) {
	if uuid, ok := nodeLinkUUID(c.Value); ok {
		n, err := database.GetNode(m.db, uuid)
		if err != nil {
			m.flash = "link target missing"
			return m, nil
		}
		if _, err := m.saveAll(); err != nil {
			m.err = err
			return m.quit()
		}
		t, err := loadTree(m.db, n.UUID)
		if err != nil {
			m.err = err
			return m.quit()
		}
		m.tree = t
		m.viewStack = []*item{t.root}
		m.undoStack = nil
		m.refreshAncestors()
		m.cursor = 0
		m.caret = 0
		m.unsaved = false
		m.refreshRows()
		m.flash = "→ " + clipStr(n.Name, 24)
		return m, nil
	}
	if err := browser.Open(c.Value); err != nil {
		m.flash = "open failed: " + err.Error()
	} else {
		m.flash = "opened " + clipStr(c.Value, 32)
	}
	return m, nil
}

// insertLinkChip splices a new link chip (target + name) in at the caret.
func (m *Model) insertLinkChip(value, label string) {
	cur := m.cursorItem()
	if cur == nil {
		return
	}
	m.pushUndo("")
	m.insertChipAtCaret(cur, chipKindLink, value, label)
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

// ── alt+e link editor (modeLinkEdit) ───────────────────────────────────────

// openLinkEdit enters the two-field editor for a link chip's name and target.
func (m *Model) openLinkEdit(c database.Chip) {
	m.mode = modeLinkEdit
	m.linkEditID = c.ID
	m.linkEditName = c.Label
	m.linkEditTarget = c.Value
	m.linkEditField = 0
}

// saveLinkEdit writes the edited name/target back to the chip store.
func (m *Model) saveLinkEdit() {
	c, ok := m.chips[m.linkEditID]
	if !ok {
		return
	}
	c.Label = m.linkEditName
	c.Value = strings.TrimSpace(m.linkEditTarget)
	// canonicalize a bare URL target ("www.x" → "https://www.x"); leave node links
	// and already-schemed URLs alone
	if _, isNode := nodeLinkUUID(c.Value); !isNode && browser.IsURL(c.Value) {
		c.Value = browser.Normalize(c.Value)
	}
	m.chips[c.ID] = c
	if m.ctx.DB != nil {
		_ = database.UpsertChip(m.ctx.DB, c)
	}
	m.unsaved = true
}

func (m *Model) handleLinkEditKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "esc":
		m.mode = modeOutline
		return m, nil
	case "tab", "shift+tab", "up", "down":
		m.linkEditField = 1 - m.linkEditField
		return m, nil
	case "enter":
		m.saveLinkEdit()
		m.mode = modeOutline
		m.refreshRows()
		return m, nil
	case "backspace":
		if m.linkEditField == 0 {
			if r := []rune(m.linkEditName); len(r) > 0 {
				m.linkEditName = string(r[:len(r)-1])
			}
		} else {
			if r := []rune(m.linkEditTarget); len(r) > 0 {
				m.linkEditTarget = string(r[:len(r)-1])
			}
		}
		return m, nil
	}
	if k.Type == tea.KeySpace && !k.Alt {
		k.Type, k.Runes = tea.KeyRunes, []rune{' '}
	}
	if k.Type == tea.KeyRunes && !k.Alt {
		if m.linkEditField == 0 {
			m.linkEditName += string(k.Runes)
		} else {
			m.linkEditTarget += string(k.Runes)
		}
	}
	return m, nil
}

func (m *Model) viewLinkEdit(maxLine int) []string {
	name := m.linkEditName
	target := m.linkEditTarget
	nameLbl, targetLbl := cDim, cDim
	if m.linkEditField == 0 {
		name = withCaret(name, len([]rune(name)))
		nameLbl = cAccent
	} else {
		target = withCaret(target, len([]rune(target)))
		targetLbl = cAccent
	}
	var lines []string
	lines = append(lines, clip(cDim+" edit link"+cReset, maxLine))
	lines = append(lines, clip(nameLbl+" name   "+cReset+cFG+name+cReset, maxLine))
	lines = append(lines, clip(targetLbl+" target "+cReset+cFG+target+cReset, maxLine))
	lines = append(lines, "")
	lines = append(lines, clip(cDim+" tab switch field · enter save · esc cancel"+cReset, maxLine))
	return lines
}
