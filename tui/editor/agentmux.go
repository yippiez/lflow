package editor

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/lflow/lflow/tui/multiplexer"
)

// An agent session runs ON the multiplexer now: ⌥r resumes the CLI on a
// background PTY instead of handing it the terminal, the chip shimmers while
// the agent works, and lflow never leaves the screen. The editor owns the
// session — closing lflow ends the process, and nothing is lost because every
// CLI resumes its own conversation natively on the next ⌥r.

// agentMuxMsg carries one event from an agent session's stream — the agent
// twin of bashBytesMsg/bashDoneMsg, keyed by chip id.
type agentMuxMsg struct {
	id string
	ev multiplexer.Event
}

// waitAgentMux is the stream pump: one event per Update, re-armed until Done.
// A closed channel reads as Done so the kill path lands the same way.
func waitAgentMux(s *multiplexer.Session) tea.Cmd {
	return func() tea.Msg {
		ev, ok := <-s.Events()
		if !ok {
			return agentMuxMsg{id: s.ID, ev: multiplexer.Event{ID: s.ID, Done: true}}
		}
		return agentMuxMsg{id: s.ID, ev: ev}
	}
}

// handleAgentMux lands one session event: output bytes go to the session's
// screen (and re-read its status), Done closes the session out exactly the way
// the old terminal-handoff landing did — identity snapshot, cache invalidation,
// chip refresh.
func (m *Model) handleAgentMux(msg agentMuxMsg) tea.Cmd {
	s := m.mux().Get(msg.id)
	if s == nil {
		return nil
	}
	if msg.ev.Err != nil {
		m.errorFlash(s.Agent + ": " + msg.ev.Err.Error())
	}
	if len(msg.ev.Data) > 0 {
		s.Scr.Write(msg.ev.Data)
		s.UpdateStatus()
	}
	if msg.ev.Done {
		if m.mode == modeMux && m.muxID == msg.id {
			m.muxDetach() // the view's session ended under it
		}
		m.handleAgentClosed(agentClosedMsg{id: msg.id, variant: s.Agent})
		m.mux().Remove(msg.id)
		m.refreshRows()
		return nil
	}
	return m.startAnim(waitAgentMux(s))
}

// muxSize is the PTY size an agent session starts at: the editor's own body,
// clamped to something a TUI can lay out in. The attach view resizes the live
// session to its exact pane later.
func (m *Model) muxSize() (cols, rows int) {
	return clampInt(m.width, 60, 250), clampInt(m.height-2, 20, 100)
}

// runningAgentCount is how many agent sessions are live — the toolbar's red
// tally and the quit guard's count. Bash sessions are counted by runningCount.
func (m *Model) runningAgentCount() int {
	if m.muxm == nil {
		return 0
	}
	n := 0
	for _, s := range m.muxm.Live() {
		if s.Agent != "" {
			n++
		}
	}
	return n
}

// tickMuxStatus advances every live agent session's status machine on the
// animation tick: the working→idle debounce needs a clock even when the agent
// is silent, and the tick is already the frame clock.
func (m *Model) tickMuxStatus() {
	if m.muxm == nil {
		return
	}
	for _, s := range m.muxm.Live() {
		if s.Agent != "" {
			s.UpdateStatus()
		}
	}
}

// agentMuxStates is the render-time status of each live agent session — the
// same render-global seam as agentLooks and liveCmdRuns: renderAgentChip is a
// pure function reached from every surface, and this is the one bit of session
// state it needs. syncAgentMux refreshes it once per frame.
var agentMuxStates = map[string]multiplexer.Status{}

// syncAgentMux snapshots live agent session statuses for this frame's render.
func (m *Model) syncAgentMux() {
	st := map[string]multiplexer.Status{}
	if m.muxm != nil {
		for _, s := range m.muxm.Live() {
			if s.Agent != "" {
				st[s.ID] = s.Status()
			}
		}
	}
	agentMuxStates = st
}

// agentMuxWorking reports whether a chip's session is mid-work this frame —
// what slides the shimmer across its pill.
func agentMuxWorking(id string) bool {
	return agentMuxStates[id] == multiplexer.StatusWorking
}

// agentMuxLive reports whether a chip has a live session this frame.
func agentMuxLive(id string) bool {
	_, ok := agentMuxStates[id]
	return ok
}
