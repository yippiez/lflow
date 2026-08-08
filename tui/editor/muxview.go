package editor

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/lflow/lflow/tui/multiplexer"
)

// The ATTACH VIEW (modeMux): the multiplexer session full-screen inside lflow,
// fully interactive — keystrokes encode to terminal bytes and forward to the
// PTY, the session's screen renders with its own block cursor. ctrl+q detaches
// back to the outline with the agent still running; everything else, esc
// included, belongs to the agent (Claude cancels with esc — a view that stole
// it would be unusable).

// muxAttach opens the attach view on a chip's live session, resizing the PTY
// to the view's exact pane.
func (m *Model) muxAttach(id string) {
	s := m.mux().Get(id)
	if s == nil || !s.Live() {
		m.flash = "no live session · ⌥r starts one"
		return
	}
	m.muxID = id
	m.mode = modeMux
	cols, rows := m.muxViewSize()
	s.Resize(cols, rows)
}

// muxDetach leaves the view; the session keeps running behind the outline.
func (m *Model) muxDetach() {
	m.mode = modeOutline
	m.muxID = ""
}

// muxViewSize is the attached pane: the full frame minus the header row, and
// never the last column (deferred-wrap desync).
func (m *Model) muxViewSize() (cols, rows int) {
	w, h := m.width, m.height
	if w <= 0 {
		w = 80
	}
	if h <= 0 {
		h = 24
	}
	return clampInt(w-1, 20, 500), max(h-1, 5)
}

// handleMuxKey forwards the keyboard to the session. Only ctrl+q is lflow's.
func (m *Model) handleMuxKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	if k.String() == "ctrl+q" {
		m.muxDetach()
		m.flash = "detached · the agent keeps running"
		return m, nil
	}
	s := m.mux().Get(m.muxID)
	if s == nil || !s.Live() {
		// the session ended under the view: any key returns to the outline
		m.muxDetach()
		return m, nil
	}
	if k.Paste {
		payload := []byte(string(k.Runes))
		if s.Scr.BracketedPaste() {
			payload = append(append([]byte("\x1b[200~"), payload...), "\x1b[201~"...)
		}
		_ = s.Write(payload)
		return m, nil
	}
	if b := multiplexer.EncodeKey(k, s.Scr.AppCursor()); len(b) > 0 {
		_ = s.Write(b)
	}
	return m, nil
}

// viewMux paints the attached session: one header line — the agent's pill,
// its live status, the detach key — then the session's screen, cursor and all.
func (m *Model) viewMux(maxLine int) []string {
	s := m.mux().Get(m.muxID)
	if s == nil {
		return []string{cDim + " session gone · any key returns" + cReset}
	}
	glyph, col, title := "◈", cDim, s.Label
	if v, ok := agentVariantByID(s.Agent); ok {
		glyph = v.glyph
		col = m.publishAgentLook(m.muxID, v)
		title = m.agentTitle(m.muxID, v, m.agentLoad(m.muxID))
	}
	status := ""
	switch {
	case !s.Live():
		status = cRed + "exited" + cReset
	case s.Status() == multiplexer.StatusWorking:
		status = shimmerLabel("working…") + cDim + " " + elapsedShort(s.Elapsed())
	case s.Status() == multiplexer.StatusBlocked:
		status = cRed + "waiting on you" + cReset
	default:
		status = cDim + "idle" + cReset
	}
	head := cReset + bgOf(col) + contrastInk(col) + " " + glyph + " " + title + " " + cReset +
		" " + status + cDim + " · ctrl+q detach" + cReset
	if line := s.TitleLine(); line != "" && line != title {
		head += cDim + " · " + line + cReset
	}
	lines := []string{clip(head, maxLine)}
	for _, row := range s.Scr.GridLines(true) {
		lines = append(lines, clip(row, maxLine))
	}
	m.pageRows = len(lines) // no status bar — the whole frame is the terminal
	return lines
}

// muxResized keeps an attached session glued to the frame across terminal
// resizes; called from the WindowSizeMsg path.
func (m *Model) muxResized() {
	if m.mode != modeMux {
		return
	}
	if s := m.mux().Get(m.muxID); s != nil && s.Live() {
		cols, rows := m.muxViewSize()
		s.Resize(cols, rows)
	}
}
