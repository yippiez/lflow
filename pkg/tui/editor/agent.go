package editor

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// agentView is the worker's inline expanded view (alt+e): a sectioned, scrollable
// observe pane (Agent / Status / Tool calls / Final) that renders as bands beneath
// the node — never a separate screen. Steering is a SUB-STATE of this view (press
// 's'), an outline composer, not its own mode. State is ephemeral (per-node store);
// the worker's run state stays in the existing run/worker maps.
type agentView struct{}

const (
	subObserve = 0
	subSteer   = 1
)

func (agentView) sub(m *Model, it *item) int {
	s, _ := m.nodeStore(it.uuid)["agentSub"].(int)
	return s
}
func (agentView) setSub(m *Model, it *item, s int) { m.nodeStore(it.uuid)["agentSub"] = s }

func (agentView) steerBuf(m *Model, it *item) (string, int) {
	d := m.nodeStore(it.uuid)
	b, _ := d["steerBuf"].(string)
	c, _ := d["steerCaret"].(int)
	return b, c
}
func (agentView) setSteerBuf(m *Model, it *item, b string, c int) {
	d := m.nodeStore(it.uuid)
	d["steerBuf"] = b
	d["steerCaret"] = c
}

// redRule is a red horizontal divider (bracketing the expanded view), sized to
// fill exactly the room after the tree rail so it never overflows into an ellipsis.
func redRule(rail string, width int) string {
	n := width - visibleWidth(rail)
	if n < 1 {
		n = 1
	}
	return rail + cReset + cRed + strings.Repeat("─", n) + cReset
}

// agentNode renders one outline row inside the expanded view: rail + indent + ○ +
// pre-styled text. The whole view is made of these so it reads like an outline.
func agentNode(rail string, depth int, styled string) string {
	return rail + cReset + strings.Repeat("  ", depth) + cDim + "○ " + cReset + styled
}

func (v agentView) Enter(m *Model, it *item) bool {
	m.lastAgent = it.uuid
	v.setSub(m, it, subObserve)
	return true
}

func (v agentView) Leave(m *Model, it *item) {
	d := m.nodeStore(it.uuid)
	delete(d, "agentSub")
	delete(d, "steerBuf")
	delete(d, "steerCaret")
}

func (v agentView) Lines(m *Model, it *item, width int) int {
	if v.sub(m, it) == subSteer {
		c, _ := v.steerContent(m, it, "", width, false)
		return len(c)
	}
	return len(v.observeContent(m, it, "", width))
}

// Key routes to the steer composer when in the steer sub-state, else handles the
// observe pane (scroll, s→steer, x→stop). Unhandled keys (esc/ctrl+c) fall through.
func (v agentView) Key(m *Model, it *item, k tea.KeyMsg) (tea.Cmd, bool) {
	if v.sub(m, it) == subSteer {
		return v.steerKey(m, it, k)
	}
	switch k.String() {
	case "s":
		v.setSub(m, it, subSteer)
		v.setSteerBuf(m, it, "", 0)
		m.focusScroll = 0
		return nil, true
	case "x":
		if m.runCancel != nil {
			if cancel, running := m.runCancel[it.uuid]; running {
				cancel()
				delete(m.runCancel, it.uuid)
				if m.workerStatus != nil {
					m.workerStatus[it.uuid] = "done"
				}
			}
		}
		return nil, true
	case "down", "j", "pgdown":
		step := 1
		if k.String() == "pgdown" {
			step = 8
		}
		m.focusScroll += step
		return nil, true
	case "up", "k", "pgup":
		step := 1
		if k.String() == "pgup" {
			step = 8
		}
		m.focusScroll -= step
		if m.focusScroll < 0 {
			m.focusScroll = 0
		}
		return nil, true
	}
	return nil, false // esc/ctrl+c → central
}

// steerKey drives the inline outline composer: enter=new node, tab=indent, alt+s
// sends to the agent (same conversation if live, else stage+rerun), esc=back.
func (v agentView) steerKey(m *Model, it *item, k tea.KeyMsg) (tea.Cmd, bool) {
	buf, caret := v.steerBuf(m, it)
	ins := func(s string) {
		r := []rune(buf)
		if caret > len(r) {
			caret = len(r)
		}
		buf = string(r[:caret]) + s + string(r[caret:])
		caret += len([]rune(s))
	}
	switch k.String() {
	case "esc":
		v.setSub(m, it, subObserve)
		m.focusScroll = 0
		return nil, true // handled → central esc won't defocus the whole view
	case "alt+s", "alt+S", "ctrl+s":
		msg := strings.TrimSpace(buf)
		v.setSteerBuf(m, it, "", 0)
		v.setSub(m, it, subObserve)
		m.focusScroll = 0
		if msg == "" {
			return nil, true
		}
		m.flash = "steered"
		if ch := m.liveSteer(it.uuid); ch != nil {
			ch <- msg // same conversation, as composed
			// reflect the new turn immediately so it never reads "idle" while working
			if m.workerStatus != nil {
				m.workerStatus[it.uuid] = "running"
			}
			if m.workerAction != nil {
				m.workerAction[it.uuid] = workerActivity{text: "thinking…"}
			}
			return nil, true
		}
		for _, child := range parseOutlineText(m.tempTree, msg) {
			child.parent = it
			it.children = append(it.children, child)
		}
		return runWorker(m, it), true
	case "enter":
		ins("\n")
	case "tab":
		ins("  ")
	case "left":
		if caret > 0 {
			caret--
		}
	case "right":
		if caret < len([]rune(buf)) {
			caret++
		}
	case "up":
		caret = jsonCaretLineMove(buf, caret, -1)
	case "down":
		caret = jsonCaretLineMove(buf, caret, +1)
	case "backspace":
		r := []rune(buf)
		if caret > 0 {
			buf = string(r[:caret-1]) + string(r[caret:])
			caret--
		}
	default:
		switch {
		case k.Type == tea.KeySpace && !k.Alt:
			ins(" ")
		case k.Type == tea.KeyRunes && !k.Alt:
			ins(string(k.Runes))
		default:
			return nil, false
		}
	}
	v.setSteerBuf(m, it, buf, caret)
	return nil, true
}

// Bands renders the expanded view bracketed by red dividers: a top rule below the
// node, the scrollable content (an outline of nodes), a bottom rule, then a footer
// hint. Only the content scrolls; the rules + footer stay fixed.
func (v agentView) Bands(m *Model, it *item, rail string, width, scroll, winH int, focused bool) []string {
	var content []string
	caretContentLine := -1
	footer := " j/k scroll · s steer · x stop · esc close"
	if v.sub(m, it) == subSteer {
		content, caretContentLine = v.steerContent(m, it, rail, width, focused)
		footer = " alt+s send · enter new node · tab indent · esc back"
	} else {
		content = v.observeContent(m, it, rail, width)
	}
	inner := winH - 3 // top rule + bottom rule + footer
	if inner < 1 {
		inner = 1
	}
	if focused && caretContentLine >= 0 {
		if caretContentLine < scroll {
			scroll = caretContentLine
		}
		if caretContentLine >= scroll+inner {
			scroll = caretContentLine - inner + 1
		}
	}
	if scroll > len(content)-inner {
		scroll = len(content) - inner
	}
	if scroll < 0 {
		scroll = 0
	}
	if focused {
		m.focusScroll = scroll
	}
	end := scroll + inner
	if end > len(content) {
		end = len(content)
	}
	out := []string{redRule(rail, width)}
	out = append(out, content[scroll:end]...)
	out = append(out, redRule(rail, width))
	out = append(out, clip(rail+cReset+cDim+footer+cReset, width))
	return out
}

// observeContent builds the observe pane as a compact outline: section nodes
// (Agent / Status / Tool calls / Final) each with their content as child nodes.
func (v agentView) observeContent(m *Model, it *item, rail string, width int) []string {
	name := m.tree.displayName(it)
	if strings.TrimSpace(name) == "" {
		name = "untitled"
	}
	_, running := m.runCancel[it.uuid]
	status := statusWord(m.workerStatus[it.uuid], running)

	var c []string
	node := func(depth int, styled string) { c = append(c, clip(agentNode(rail, depth, styled), width)) }
	sub := func(depth int, styled string) { c = append(c, clip(rail+cReset+strings.Repeat("  ", depth+1)+"  "+styled, width)) }

	// Agent → query
	node(0, cFG+"Agent"+cReset)
	for _, w := range wrapPlain(name, width-8) {
		node(1, cFG+w+cReset)
	}
	// Status → one compact line: status, usage, elapsed, then model
	node(0, cFG+"Status"+cReset)
	line := statusColor(m.workerStatus[it.uuid]) + status + cReset
	if u, ok := m.workerUsage[it.uuid]; ok {
		line += cDim + fmt.Sprintf("  ↑%s ↓%s $%.4f", ktok(u.in), ktok(u.out), u.cost) + cReset
	}
	if el := m.workerElapsed(it.uuid); el != "" {
		line += cDim + "  " + el + cReset
	}
	node(1, line)
	if mdl := m.workerModel[it.uuid]; mdl != "" {
		node(1, cDim+mdl+cReset)
	}
	// Tool calls → one node per call
	calls := m.workerActions[it.uuid]
	node(0, cFG+fmt.Sprintf("Tool calls (%d)", len(calls))+cReset)
	for _, a := range calls {
		t := toolColor(a.tool) + toolLabel(a.tool) + cReset
		if a.text != "" {
			t += cDim + " " + a.text + cReset
		}
		node(1, t)
	}
	if running {
		if a, ok := m.workerAction[it.uuid]; ok && a.tool != "" {
			node(1, toolColor(a.tool)+toolLabel(a.tool)+cReset+cDim+" "+a.text+"…"+cReset)
		}
	} else if len(calls) == 0 {
		node(1, cDim+"none"+cReset)
	}
	// Final → the deliverable as nested nodes (with their custom types)
	node(0, cFG+"Final"+cReset)
	if nodes := parseDeliverNodes(m.workerDeliverable[it.uuid]); len(nodes) > 0 {
		var walk func(ns []deliverNode, depth int)
		walk = func(ns []deliverNode, depth int) {
			for _, n := range ns {
				txt := strings.TrimSpace(n.Text)
				if txt == "" && len(n.Children) == 0 {
					continue
				}
				node(depth, cFG+typeOf(deliverType(n.Type)).sign+txt+cReset)
				if note := strings.TrimSpace(n.Note); note != "" {
					for _, w := range wrapPlain(note, width-(depth+1)*2-6) {
						sub(depth, cDim+w+cReset)
					}
				}
				walk(n.Children, depth+1)
			}
		}
		walk(nodes, 1)
	} else if running {
		node(1, cDim+"running…"+cReset)
	} else {
		node(1, cDim+"no result yet"+cReset)
	}
	return c
}

// steerContent builds the outline composer as nodes (a "Steer" node + one ○ per
// line) and returns the content-line index of the caret (for scroll-follow).
func (v agentView) steerContent(m *Model, it *item, rail string, width int, focused bool) ([]string, int) {
	buf, caret := v.steerBuf(m, it)
	var c []string
	c = append(c, clip(agentNode(rail, 0, cFG+"Steer"+cReset), width))
	caretLine, caretCol := jsonCaretLC(buf, caret)
	for i, raw := range strings.Split(buf, "\n") {
		trimmed := strings.TrimLeft(raw, " ")
		depth := 1 + (len([]rune(raw))-len([]rune(trimmed)))/2
		if focused && i == caretLine {
			col := caretCol - (len([]rune(raw)) - len([]rune(trimmed)))
			if col < 0 {
				col = 0
			}
			c = append(c, clip(agentNode(rail, depth, cFG+withCaret(trimmed, col)+cReset), width))
		} else {
			c = append(c, clip(agentNode(rail, depth, cFG+trimmed+cReset), width))
		}
	}
	return c, caretLine + 1 // +1 for the "Steer" node
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
