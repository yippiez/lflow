package editor

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"

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
	case "n":
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
	case "alt+o":
		if url := m.agentWebURL(h.v, h.sess); url != "" {
			return m.agentOpenURL(h.id, url), true
		}
		m.flash = "local session · no browser view"
		return nil, true
	}
	return nil, false
}

// agentBandContent renders the panel: the session, where it runs, and its keys.
func (m *Model) agentBandContent(h agentHandle, rail string, width int) []string {
	if width <= 0 {
		width = 1 << 20
	}
	color := m.agentColorFor(h.id, h.v, h.sess)
	line := func(s string) string { return clip(rail+cReset+s, width) }

	// the session's line: its pill, or the rename field in its place
	var head string
	if f := m.agentRenameState(h.id); f != nil {
		hint := "   enter save · esc cancel · empty = the CLI's own name"
		head = "  " + cDim + "name  " + cReset + withCaret(f.value, f.caret) + cDim + hint + cReset
	} else {
		head = "  " + bgOf(color) + contrastInk(color) + " " + h.v.glyph + " " +
			m.agentTitle(h.id, h.v, h.sess) + " " + cReset
	}
	content := []string{line(head)}

	var meta []string
	if h.sess.Cwd != "" {
		meta = append(meta, tildePath(h.sess.Cwd))
	}
	meta = append(meta, m.agentStateWord(h))
	if h.sess.OpenedAt > 0 {
		meta = append(meta, "opened "+relTime(h.sess.OpenedAt))
	}
	content = append(content, line("  "+cDim+strings.Join(meta, " · ")+cReset))

	keys := "  ⌥r open · n rename · esc close"
	if h.v.webOnly() {
		keys = "  ⌥r open in browser · n rename · esc close"
	} else if h.v.webURL != nil || h.sess.Remote != "" {
		keys = "  ⌥r open · ⌥o browser · n rename · esc close"
	}
	return append(content, line(cDim+keys+cReset))
}

// agentStateWord is what the session is doing right now, in one word.
func (m *Model) agentStateWord(h agentHandle) string {
	switch {
	case m.agentLive(h.v, h.sess):
		return "live"
	case h.v.webOnly():
		return glyphCloud + " web"
	case m.agentCloud(h.v, h.sess):
		return glyphCloud + " hosted"
	case h.sess.SessionID == "":
		return "not started"
	default:
		return "idle"
	}
}
