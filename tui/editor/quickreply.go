package editor

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lflow/lflow/tui/database"
	"github.com/lflow/lflow/tui/multiplexer"
)

// ⌥m on a session chip: the QUICK REPLY. The row grows a rounded box in the
// session's own color — the agent's last word on one line, a reply line under
// it — so answering a background agent is glance, type, enter, gone. The send
// goes straight to the session's PTY (SendText: paste + delayed Enter); the
// box stays up, and the status in its border shows the agent taking it.
//
//	╭─ ✽ name · working… 2m10s ─────────────╮
//	│ → the agent's last response, one line  │
//	│ ❯ your reply▌                          │
//	╰─ enter send · esc close ───────────────╯

type quickReplyView struct{}

// openQuickReply raises the box on a session chip.
func (m *Model) openQuickReply(c database.Chip) {
	v, ok := agentVariantByID(c.Value)
	if !ok {
		m.errorFlash("unknown agent: " + c.Value)
		return
	}
	m.focusChip = c.ID
	m.quickReply = true
	m.focused = true
	m.focusScroll = 0
	m.nodeStore(c.ID)["quickReply"] = &textField{}
	// the last comment must be TODAY's: re-read the CLI's store on open
	m.refreshAgentTrace(agentHandle{id: c.ID, v: v, sess: m.agentLoad(c.ID)})
}

func (m *Model) quickReplyField(id string) *textField {
	f, _ := m.nodeStore(id)["quickReply"].(*textField)
	return f
}

// agentLastResponse is the one-line "what it last said": the live terminal
// title while a session runs (Claude keeps a summary there), else the last
// assistant message from the CLI's own store.
func (m *Model) agentLastResponse(id string) string {
	if m.muxm != nil {
		if s := m.muxm.Get(id); s != nil && s.Live() {
			if t := s.TitleLine(); t != "" {
				return t
			}
		}
	}
	c, ok := m.chips[id]
	if !ok {
		return ""
	}
	v, ok := agentVariantByID(c.Value)
	if !ok {
		return ""
	}
	traces := m.agentTraces(agentHandle{id: id, v: v, sess: m.agentLoad(id)})
	for i := len(traces) - 1; i >= 0; i-- {
		if traces[i].kind == "assistant" {
			return traces[i].text
		}
	}
	return ""
}

// sendQuickReply ships the field to the session's PTY.
func (m *Model) sendQuickReply(id string, f *textField) {
	text := strings.TrimSpace(f.value)
	if text == "" {
		return
	}
	s := m.mux().Get(id)
	if s == nil || !s.Live() {
		m.errorFlash("not running · ⌥r starts it")
		return
	}
	if err := s.SendText(text); err != nil {
		m.errorFlash("send: " + err.Error())
		return
	}
	f.value, f.caret = "", 0
	m.flash = "sent"
}

func (quickReplyView) enter(m *Model, it *item) bool { return m.focusChip != "" }

func (quickReplyView) leave(m *Model, it *item) {
	if m.focusChip != "" {
		delete(m.nodeStore(m.focusChip), "quickReply")
	}
	m.focusChip = ""
	m.quickReply = false
}

func (quickReplyView) lines(m *Model, it *item, width int) int { return 4 }

func (quickReplyView) key(m *Model, it *item, k tea.KeyMsg) (tea.Cmd, bool) {
	f := m.quickReplyField(m.focusChip)
	if f == nil {
		return nil, false
	}
	switch k.String() {
	case "enter":
		m.sendQuickReply(m.focusChip, f)
		return nil, true
	case "esc":
		return nil, false // central defocus closes the box
	}
	f.handleKey(k)
	return nil, true
}

func (quickReplyView) bands(m *Model, it *item, rail string, width, scroll, winH int, focused bool) []string {
	id := m.focusChip
	c, ok := m.chips[id]
	if !ok {
		return nil
	}
	v, _ := agentVariantByID(c.Value)
	sess := m.agentLoad(id)
	col := m.agentColorFor(id, v, sess)
	f := m.quickReplyField(id)
	if f == nil {
		return nil
	}

	inner := width - visibleWidth(rail)
	w := clampInt(inner-2, 24, 100) // the box's full width, borders included

	// what the border says the agent is doing right now
	status := ""
	if m.muxm != nil {
		if s := m.muxm.Get(id); s != nil && s.Live() {
			switch s.Status() {
			case multiplexer.StatusWorking:
				status = shimmerLabel("working…") + cDim + " " + elapsedShort(s.Elapsed())
			case multiplexer.StatusBlocked:
				status = cRed + "waiting on you"
			default:
				status = cDim + "idle"
			}
		}
	}
	if status == "" {
		status = cDim + "not running"
	}

	pad := func(s string, n int) string {
		if d := n - visibleWidth(s); d > 0 {
			return s + strings.Repeat(" ", d)
		}
		return s
	}
	line := func(s string) string { return clip(rail+cReset+s+cReset, width) }

	head := col + "╭─ " + cReset + v.glyph + " " + m.agentTitle(id, v, sess) + " " + cDim + "· " + status + cReset + " "
	if fill := w - visibleWidth(head) - 1; fill > 0 {
		head += col + strings.Repeat("─", fill)
	}
	head += col + "╮"

	last := m.agentLastResponse(id)
	if last == "" {
		last = "no response yet"
	}
	lastRow := col + "│ " + cReset + cDim + "→ " + clipStr(oneLine(last), w-6) + cReset
	lastRow = pad(lastRow, w-1) + col + "│"

	replyRow := col + "│ " + cReset + cRed + "❯ " + cReset + cFG + withCaret(f.value, f.caret) + cReset
	replyRow = pad(replyRow, w-1) + col + "│"

	foot := col + "╰─ " + cDim + "enter send · esc close" + cReset + " "
	if fill := w - visibleWidth(foot) - 1; fill > 0 {
		foot += col + strings.Repeat("─", fill)
	}
	foot += col + "╯"

	return []string{line(head), line(lastRow), line(replyRow), line(foot)}
}
