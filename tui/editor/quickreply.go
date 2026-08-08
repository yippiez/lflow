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

// agentLastResponse is the one-line "what it last said": the last assistant
// MESSAGE from the CLI's own store — that is the box's whole purpose — with
// the live terminal title only as the mid-work progress line (Claude paints a
// summary there while it thinks) or the fallback when the store has nothing.
func (m *Model) agentLastResponse(id string) string {
	title := ""
	working := false
	if m.muxm != nil {
		if s := m.muxm.Get(id); s != nil && s.Live() {
			title = s.TitleLine()
			working = s.Status() == multiplexer.StatusWorking
		}
	}
	if working && title != "" {
		return title
	}
	if c, ok := m.chips[id]; ok {
		if v, ok := agentVariantByID(c.Value); ok {
			traces := m.agentTraces(agentHandle{id: id, v: v, sess: m.agentLoad(id)})
			for i := len(traces) - 1; i >= 0; i-- {
				if traces[i].kind == "assistant" {
					return traces[i].text
				}
			}
		}
	}
	return title
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

	// Every row's content is clipped by DISPLAY width before the right border
	// goes on — a long session title, a wide-rune reply, none of it may push a
	// corner or a border off the pane. The rounded box stays a rounded box.
	edge := func(l, content, r string, dash bool) string {
		s := col + l + cReset + clip(content, w-6) + cReset + " "
		if dash {
			if fill := w - visibleWidth(s) - 1; fill > 0 {
				s += col + strings.Repeat("─", fill)
			}
		}
		return pad(s, w-1) + col + r + cReset
	}

	head := edge("╭─ ", v.glyph+" "+m.agentTitle(id, v, sess)+" "+cDim+"· "+status, "╮", true)

	last := m.agentLastResponse(id)
	if last == "" {
		last = "no response yet"
	}
	lastRow := edge("│ ", cDim+"→ "+oneLine(last), "│", false)

	replyRow := edge("│ ", cRed+"❯ "+cReset+cFG+caretWindow(f.value, f.caret, w-9), "│", false)

	foot := edge("╰─ ", cDim+"enter send · esc close", "╯", true)

	return []string{line(head), line(lastRow), line(replyRow), line(foot)}
}

// caretWindow renders a one-line input clipped to n runes with the CARET always
// in view: a reply longer than the box slides left under it — the tail you are
// typing stays visible — instead of the caret vanishing past the border.
func caretWindow(value string, caret, n int) string {
	r := []rune(value)
	if caret > len(r) {
		caret = len(r)
	}
	if n < 4 || len(r) < n {
		return withCaret(value, caret)
	}
	start := 0
	if caret > n-2 {
		start = caret - (n - 2)
	}
	out := withCaret(string(r[start:]), caret-start)
	if start > 0 {
		out = cDim + "…" + cReset + cFG + out
	}
	return out
}
