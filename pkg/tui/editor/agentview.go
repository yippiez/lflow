package editor

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mattn/go-runewidth"

	"github.com/lflow/lflow/pkg/tui/database"
)

// ⌥e on a session chip: a SMALL panel beneath the row — the session's name, the
// directory it runs in, what it is doing, and the keys that act on it. Nothing
// more. The conversation belongs to the CLI: lflow points at a session, it does
// not mirror it, so there is no transcript, no token count, no tool log here.
//
// The one thing the panel edits is the session's NAME (n), the way a link chip's
// name is edited — the rest is read-only.

// agentChipView is the focused chip's inline view, reached through m.activeView
// like the cmd chip's output band.
type agentChipView struct{}

// agentHandle is the session being viewed: its storage id, its variant, its data.
type agentHandle struct {
	id   string
	v    agentVariant
	sess agentSession
}

// agentChipHandle resolves the focused chip's handle.
func agentChipHandle(m *Model) (agentHandle, bool) {
	c, ok := m.chips[m.focusChip]
	if !ok || c.Kind != chipKindAgent {
		return agentHandle{}, false
	}
	v, ok := agentVariantByID(c.Value)
	if !ok {
		return agentHandle{}, false
	}
	return agentHandle{id: c.ID, v: v, sess: m.agentLoad(c.ID)}, true
}

// focusAgentChip opens the panel (⌥e on the chip).
func (m *Model) focusAgentChip(c database.Chip) {
	m.focusChip = c.ID
	m.focused = true
	m.focusScroll = 0
}

func (agentChipView) Enter(m *Model, it *item) bool { return m.focusChip != "" }

// Leave closes the panel, dropping any half-typed rename with it.
func (agentChipView) Leave(m *Model, it *item) {
	if m.focusChip != "" {
		delete(m.nodeStore(m.focusChip), "agentRename")
	}
	m.focusChip = ""
}

func (agentChipView) Lines(m *Model, it *item, width int) int {
	h, ok := agentChipHandle(m)
	if !ok {
		return 0
	}
	return len(m.agentBandContent(h, "", width))
}

func (agentChipView) Key(m *Model, it *item, k tea.KeyMsg) (tea.Cmd, bool) {
	h, ok := agentChipHandle(m)
	if !ok {
		return nil, false
	}
	return m.agentViewKey(h, k)
}

func (agentChipView) Bands(m *Model, it *item, rail string, width, scroll, winH int, focused bool) []string {
	h, ok := agentChipHandle(m)
	if !ok {
		return nil
	}
	return NodeWindowBands(m.agentBandContent(h, rail, width), scroll, winH)
}

// agentRenameState returns the open rename field for a handle, or nil. The name
// is edited IN the panel — the session's own line becomes the field — so
// renaming never leaves the outline.
func (m *Model) agentRenameState(id string) *textField {
	f, _ := m.nodeStore(id)["agentRename"].(*textField)
	return f
}

// agentViewKey drives the panel: n renames the session, ⌥r opens it, ⌥o opens a
// hosted one in the browser. esc falls through to the central defocus — unless a
// rename is open, which takes esc for itself.
func (m *Model) agentViewKey(h agentHandle, k tea.KeyMsg) (tea.Cmd, bool) {
	if f := m.agentRenameState(h.id); f != nil {
		switch k.String() {
		case "enter":
			m.agentRename(h.id, f.value)
			delete(m.nodeStore(h.id), "agentRename")
			m.flash = "session renamed"
			return nil, true
		case "esc":
			delete(m.nodeStore(h.id), "agentRename")
			return nil, true
		}
		f.handleKey(k) // the shared field vocabulary: runes, backspace, caret moves
		return nil, true
	}
	switch k.String() {
	case "n", "alt+n":
		// a session still wearing the CLI's own name opens an EMPTY field: typing
		// replaces rather than appends, and an empty field already means "follow
		// the CLI". A session you named before opens with that name to edit.
		f := &textField{value: h.sess.Name}
		f.caret = len([]rune(f.value))
		m.nodeStore(h.id)["agentRename"] = f
		return nil, true
	case "alt+r":
		cwd := h.sess.Cwd
		if cwd == "" {
			cwd = processCWD()
		}
		return m.agentOpen(h.v, h.id, cwd), true
	case "alt+c":
		m.openAgentColor(m.chips[h.id])
		return nil, true
	}
	return nil, false
}

// agentBandContent renders the panel: the session's full name, wrapped, and the
// directory it is pinned to. Nothing else — every other thing lflow could say
// about a session it would be GUESSING at (see agent.go on what the three stores
// actually expose), and a panel that guesses is worse than a short one.
func (m *Model) agentBandContent(h agentHandle, rail string, width int) []string {
	if width <= 0 {
		width = 1 << 20
	}
	color := m.agentColorFor(h.id, h.v, h.sess)
	line := func(s string) string { return clip(rail+cReset+s, width) }

	// the rename field takes the session's line while it is open
	if f := m.agentRenameState(h.id); f != nil {
		hint := "   enter save · esc cancel · empty = the CLI's own name"
		head := "  " + cDim + "name  " + cReset + withCaret(f.value, f.caret) + cDim + hint + cReset
		return []string{line(head), line(cDim + "  " + tildePath(h.sess.Cwd) + cReset), line(cDim + agentPanelKeys + cReset)}
	}

	// the name in full: a session named by its first prompt is a sentence, and
	// clipping it to the pill's width is what makes two sessions look alike
	ink := bgOf(color) + contrastInk(color)
	body := width - 6 // the rail, the indent, and the pill's own two spaces
	if body < 8 {
		body = 8
	}
	var content []string
	for i, chunk := range wrapPlain(h.v.glyph+" "+m.agentTitle(h.id, h.v, h.sess), body) {
		pad := ""
		if i > 0 {
			pad = strings.Repeat(" ", runewidth.StringWidth(h.v.glyph)+1)
		}
		content = append(content, line("  "+ink+" "+pad+chunk+" "+cReset))
	}
	if h.sess.Cwd != "" {
		content = append(content, line(cDim+"  "+tildePath(h.sess.Cwd)+cReset))
	}
	return append(content, line(cDim+agentPanelKeys+cReset))
}

// agentPanelKeys is the panel's last line: everything you can do to a session.
const agentPanelKeys = "  ⌥r open · ⌥n rename · ⌥c color · esc close"
