package editor

import (
	"encoding/json"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// The agent UI (modeAgent) is a full-panel, inline view for observing one worker:
// a sectioned layout — status/tokens, the Tool calls list, and the Final result —
// modeled on pchain's agent details view (but inline; lflow never uses the alt
// screen). Steering is a separate one-line box ('s'), so this view is read-only.

// hrule is a full-width horizontal rule, like pchain's alternate-screen border.
func hrule(maxLine int) string {
	n := maxLine
	if n < 1 {
		n = 1
	}
	return cDim + strings.Repeat("─", n) + cReset
}

// section is a colored section header ("Tool calls 3", "Final").
func section(label string) string {
	return " " + cBold + cRed + label + cReset
}

// openAgent enters the observe-only agent UI for a worker.
func (m *Model) openAgent(it *item) {
	m.mode = modeAgent
	m.agentNode = it
	m.agentScroll = 0
	m.lastAgent = it.uuid
}

// handleAgentKey drives the observe-only agent UI: scroll, s to steer, x to stop,
// esc/q to close.
func (m *Model) handleAgentKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	it := m.agentNode
	switch k.String() {
	case "esc", "q":
		m.mode = modeOutline
		m.agentNode = nil
		return m, nil
	case "ctrl+c":
		return m.quit()
	case "s":
		if it != nil {
			m.openSteer(it, modeAgent)
		}
		return m, nil
	case "x":
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
	case "down", "j", "pgdown":
		step := 1
		if k.String() == "pgdown" {
			step = 8
		}
		m.agentScroll += step
		return m, nil
	case "up", "k", "pgup":
		step := 1
		if k.String() == "pgup" {
			step = 8
		}
		m.agentScroll -= step
		if m.agentScroll < 0 {
			m.agentScroll = 0
		}
		return m, nil
	}
	return m, nil
}

// viewAgent renders the sectioned, scrollable agent detail view.
func (m *Model) viewAgent(maxLine int) []string {
	it := m.agentNode
	if it == nil {
		m.mode = modeOutline
		return m.viewOutline(maxLine)
	}
	name := m.tree.displayName(it)
	if strings.TrimSpace(name) == "" {
		name = "untitled"
	}
	_, running := m.runCancel[it.uuid]
	status := statusWord(m.workerStatus[it.uuid], running)

	// build the scrollable body
	var body []string
	body = append(body, " "+cBold+cRed+"Agent "+cReset+cFG+clipStr(name, maxLine-8)+cReset, "")
	body = append(body, " "+cDim+"status "+cReset+statusColor(m.workerStatus[it.uuid])+status+cReset)
	if u, ok := m.workerUsage[it.uuid]; ok {
		body = append(body, " "+cDim+fmt.Sprintf("tokens ↑%s ↓%s · $%.4f", ktok(u.in), ktok(u.out), u.cost)+cReset)
	}

	// Tool calls section
	calls := m.workerActions[it.uuid]
	body = append(body, "", section(fmt.Sprintf("Tool calls %d", len(calls))))
	if len(calls) == 0 {
		body = append(body, "   "+cDim+"(no tool calls yet)"+cReset)
	} else {
		for _, a := range calls {
			line := "  " + toolColor(a.tool) + toolLabel(a.tool) + cReset
			if a.text != "" {
				line += cDim + " " + a.text + cReset
			}
			body = append(body, clip(line, maxLine))
		}
	}
	// the live activity, while running
	if running {
		if a, ok := m.workerAction[it.uuid]; ok && a.tool != "" {
			body = append(body, "  "+toolColor(a.tool)+toolLabel(a.tool)+cReset+cDim+" "+a.text+"…"+cReset)
		}
	}

	// Final section — the deliverable rendered as an outline (the shape Enter will
	// harvest into the notebook)
	body = append(body, "", section("Final"), "")
	if md := m.workerDeliverable[it.uuid]; strings.TrimSpace(md) != "" {
		body = append(body, outlinePreview(md, maxLine)...)
	} else if running {
		body = append(body, " "+cDim+"(running…)"+cReset)
	} else {
		body = append(body, " "+cDim+"(no result yet)"+cReset)
	}

	// window the body between the top rule and the footer/bottom rule
	winH := m.height - 3
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

	lines := []string{hrule(maxLine)}
	for i := m.agentScroll; i < end; i++ {
		lines = append(lines, clip(body[i], maxLine))
	}
	for len(lines) < m.height-2 {
		lines = append(lines, "")
	}
	lines = append(lines, clip(" "+cDim+"j/k scroll · s steer · x stop · esc close"+cReset, maxLine))
	lines = append(lines, hrule(maxLine))
	return lines
}

// --- steer (modeSteer) -------------------------------------------------------

// openSteer opens the outline steer composer for a worker (each line is a node),
// returning to prev on exit.
func (m *Model) openSteer(it *item, prev mode) {
	m.mode = modeSteer
	m.steerNode = it
	m.steerInput = ""
	m.steerCaret = 0
	m.steerPrev = prev
	m.lastAgent = it.uuid
}

func (m *Model) steerInsert(s string) {
	r := []rune(m.steerInput)
	c := m.steerCaret
	if c > len(r) {
		c = len(r)
	}
	m.steerInput = string(r[:c]) + s + string(r[c:])
	m.steerCaret = c + len([]rune(s))
}

// handleSteerKey drives the outline steer composer: enter starts a new node, tab
// indents, alt+s (or ctrl+s) sends the composed outline to the agent, esc cancels.
func (m *Model) handleSteerKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	it := m.steerNode
	switch k.String() {
	case "esc":
		m.mode = m.steerPrev
		m.steerNode = nil
		return m, nil
	case "ctrl+c":
		return m.quit()
	case "alt+s", "alt+S", "ctrl+s":
		msg := strings.TrimSpace(m.steerInput)
		m.steerInput = ""
		m.steerCaret = 0
		m.mode = m.steerPrev
		if it == nil || msg == "" {
			m.steerNode = nil
			return m, nil
		}
		m.flash = "steered"
		if ch := m.liveSteer(it.uuid); ch != nil {
			ch <- msg // same conversation, as composed
			// reflect the new turn immediately so the worker never reads "idle" while
			// it is actually working (and isn't stuck idle if pi stalls)
			if m.workerStatus != nil {
				m.workerStatus[it.uuid] = "running"
			}
			if m.workerAction != nil {
				m.workerAction[it.uuid] = workerActivity{text: "thinking…"}
			}
			return m, nil
		}
		// exited → stage the composed outline as context children and (re)run
		for _, child := range parseOutlineText(m.tempTree, msg) {
			child.parent = it
			it.children = append(it.children, child)
		}
		return m, runWorker(m, it)
	case "enter":
		m.steerInsert("\n")
		return m, nil
	case "tab":
		m.steerInsert("  ")
		return m, nil
	case "left":
		if m.steerCaret > 0 {
			m.steerCaret--
		}
		return m, nil
	case "right":
		if m.steerCaret < len([]rune(m.steerInput)) {
			m.steerCaret++
		}
		return m, nil
	case "up":
		m.steerCaret = jsonCaretLineMove(m.steerInput, m.steerCaret, -1)
		return m, nil
	case "down":
		m.steerCaret = jsonCaretLineMove(m.steerInput, m.steerCaret, +1)
		return m, nil
	case "backspace":
		r := []rune(m.steerInput)
		if m.steerCaret > 0 {
			m.steerInput = string(r[:m.steerCaret-1]) + string(r[m.steerCaret:])
			m.steerCaret--
		}
		return m, nil
	default:
		if k.Type == tea.KeySpace && !k.Alt {
			m.steerInsert(" ")
			return m, nil
		}
		if k.Type == tea.KeyRunes && !k.Alt {
			m.steerInsert(string(k.Runes))
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

// viewSteer renders the outline steer composer: each buffer line is shown as a
// node (○), with a block caret on the active line.
func (m *Model) viewSteer(maxLine int) []string {
	name := "agent"
	if m.steerNode != nil {
		name = m.tree.displayName(m.steerNode)
	}
	lines := []string{
		hrule(maxLine),
		" " + cBold + cAccent + "Steer" + cReset + cDim + " · " + cReset + cFG + clipStr(name, maxLine-12) + cReset,
		"",
	}
	caretLine, caretCol := jsonCaretLC(m.steerInput, m.steerCaret)
	for i, raw := range strings.Split(m.steerInput, "\n") {
		// leading spaces nest the node, matching how the outline will be parsed
		trimmed := strings.TrimLeft(raw, " ")
		depth := (len([]rune(raw)) - len([]rune(trimmed))) / 2
		indent := strings.Repeat("  ", depth)
		glyph := cDim + "○ " + cReset
		if i == caretLine {
			col := caretCol - (len([]rune(raw)) - len([]rune(trimmed)))
			if col < 0 {
				col = 0
			}
			lines = append(lines, clip(" "+indent+glyph+cFG+withCaret(trimmed, col)+cReset, maxLine))
		} else {
			lines = append(lines, clip(" "+indent+glyph+cFG+trimmed+cReset, maxLine))
		}
	}
	for len(lines) < m.height-1 {
		lines = append(lines, "")
	}
	lines = append(lines, clip(" "+cDim+"alt+s send · enter new node · tab indent · esc cancel"+cReset, maxLine))
	lines = append(lines, hrule(maxLine))
	return lines
}

// outlinePreview renders the deliverable outline (nodes JSON) as outline rows
// (○ + indentation), matching the shape Enter will harvest. Pure: mutates nothing.
func outlinePreview(nodesJSON string, maxLine int) []string {
	nodesJSON = strings.TrimSpace(nodesJSON)
	if nodesJSON == "" {
		return nil
	}
	var nodes []deliverNode
	if json.Unmarshal([]byte(nodesJSON), &nodes) != nil {
		var one deliverNode
		if json.Unmarshal([]byte(nodesJSON), &one) != nil {
			return []string{" " + cDim + clipStr(nodesJSON, maxLine-2) + cReset}
		}
		nodes = []deliverNode{one}
	}
	var out []string
	var walk func(ns []deliverNode, depth int)
	walk = func(ns []deliverNode, depth int) {
		for _, n := range ns {
			text := strings.TrimSpace(n.Text)
			if text == "" && len(n.Children) == 0 {
				continue
			}
			out = append(out, clip(" "+strings.Repeat("  ", depth)+cRed+"○ "+text+cReset, maxLine))
			if note := strings.TrimSpace(n.Note); note != "" {
				for _, w := range wrapPlain(note, maxLine-(depth*2)-5) {
					out = append(out, clip(" "+strings.Repeat("  ", depth)+"   "+cDim+w+cReset, maxLine))
				}
			}
			walk(n.Children, depth+1)
		}
	}
	walk(nodes, 0)
	return out
}
