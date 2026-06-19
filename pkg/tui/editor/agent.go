package editor

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// The agent UI (modeAgent) is a full-panel, inline view for one worker: a header
// with status + usage, the live transcript (tool calls + assistant text), the
// current activity, and a steering input box. It is the home for everything a
// worker no longer shows on its single notebook line. Modeled on pchain's agent
// details + steering view, but inline (lflow never uses the alt screen).

// openAgent enters the agent UI for a worker and marks it the last-interacted
// agent so a later alt+r delegates here.
func (m *Model) openAgent(it *item) {
	m.mode = modeAgent
	m.agentNode = it
	m.agentInput = ""
	m.agentScroll = 1 << 30 // pinned to the bottom; clamped on render
	m.lastAgent = it.uuid
}

// handleAgentKey drives the agent UI: type a follow-up and Enter to steer (or
// (re)launch) the worker, scroll the transcript, x to stop, esc to close.
func (m *Model) handleAgentKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	it := m.agentNode
	switch k.String() {
	case "esc":
		m.mode = modeOutline
		m.agentNode = nil
		return m, nil
	case "enter":
		msg := strings.TrimSpace(m.agentInput)
		m.agentInput = ""
		m.agentScroll = 1 << 30
		if it == nil || msg == "" {
			return m, nil
		}
		// live process → steer the same conversation; otherwise (re)launch a turn
		if ch := m.liveSteer(it.uuid); ch != nil {
			ch <- msg
			return m, nil
		}
		it.name = msg // a fresh turn's task is the typed message
		m.unsaved = true
		return m, runWorker(m, it)
	case "ctrl+c":
		return m.quit()
	case "alt+r", "x":
		// stop a running agent (toggle), like the notebook gesture
		if it != nil && m.runCancel != nil {
			if cancel, running := m.runCancel[it.uuid]; running {
				cancel()
				delete(m.runCancel, it.uuid)
				if m.workerStatus != nil {
					m.workerStatus[it.uuid] = "done"
				}
			}
		}
		return m, nil
	case "up", "pgup":
		if m.agentScroll > 0 {
			step := 1
			if k.String() == "pgup" {
				step = 8
			}
			m.agentScroll -= step
			if m.agentScroll < 0 {
				m.agentScroll = 0
			}
		}
		return m, nil
	case "down", "pgdown":
		step := 1
		if k.String() == "pgdown" {
			step = 8
		}
		m.agentScroll += step
		return m, nil
	case "backspace":
		r := []rune(m.agentInput)
		if len(r) > 0 {
			m.agentInput = string(r[:len(r)-1])
		}
		return m, nil
	default:
		if k.Type == tea.KeySpace && !k.Alt {
			m.agentInput += " "
			return m, nil
		}
		if k.Type == tea.KeyRunes && !k.Alt {
			m.agentInput += string(k.Runes)
		}
	}
	return m, nil
}

// liveSteer returns the steering channel for a worker whose pi process is still
// alive (running or idle-but-open), or nil if it has exited.
func (m *Model) liveSteer(uuid string) chan string {
	if m.workerSteer == nil {
		return nil
	}
	if _, alive := m.runCancel[uuid]; !alive {
		return nil
	}
	return m.workerSteer[uuid]
}

// viewAgent renders the full-panel agent UI.
func (m *Model) viewAgent(maxLine int) []string {
	it := m.agentNode
	if it == nil {
		m.mode = modeOutline
		return m.viewOutline(maxLine)
	}
	name := m.tree.displayName(it)
	if strings.TrimSpace(name) == "" {
		name = "untitled agent"
	}
	_, running := m.runCancel[it.uuid]
	status := statusWord(m.workerStatus[it.uuid], running)

	header := " " + cYellow + "✦ " + cReset + cFG + clipStr(name, maxLine-24) + cReset +
		cDim + " · " + cReset + statusColor(m.workerStatus[it.uuid]) + status + cReset
	if u, ok := m.workerUsage[it.uuid]; ok {
		header += cDim + fmt.Sprintf(" ↑%s ↓%s $%.4f", ktok(u.in), ktok(u.out), u.cost) + cReset
	}
	hint := cDim + " · enter steer · x stop · esc close" + cReset
	lines := []string{clip(header+hint, maxLine), ""}

	// transcript body (tool calls + assistant text), scrollable
	var body []string
	for _, l := range m.runOut[it.uuid] {
		col := cFG
		if l.err {
			col = cRed
		}
		for _, w := range wrapPlain(l.text, maxLine-2) {
			body = append(body, "  "+col+w+cReset)
		}
	}
	if act, ok := m.workerAction[it.uuid]; ok {
		var a string
		if act.tool != "" {
			a = toolColor(act.tool) + toolLabel(act.tool) + cReset
			if act.text != "" {
				a += cDim + " " + act.text + cReset
			}
		} else {
			a = cDim + act.text + cReset
		}
		body = append(body, "  "+a)
	}
	if len(body) == 0 {
		body = []string{"  " + cDim + "no activity yet — enter a message to start" + cReset}
	}

	winH := m.height - 5
	if winH < 3 {
		winH = 3
	}
	if m.agentScroll > len(body)-winH {
		m.agentScroll = len(body) - winH
	}
	if m.agentScroll < 0 {
		m.agentScroll = 0
	}
	end := min(m.agentScroll+winH, len(body))
	for i := m.agentScroll; i < end; i++ {
		lines = append(lines, clip(body[i], maxLine))
	}

	// steering input box, pinned above the bottom bar
	input := " " + cDim + "› " + cReset + cFG + withCaret(m.agentInput, len([]rune(m.agentInput))) + cReset
	lines = append(lines, "", clip(input, maxLine))
	lines = append(lines, m.bottomBar(maxLine))
	return lines
}
