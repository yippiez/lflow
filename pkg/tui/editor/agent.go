package editor

import (
	"encoding/json"
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

// section is a red section header ("Tool calls 3", "Final"). Content sits on the
// lines below it, indented, never red.
func section(label string) string { return " " + cBold + cRed + label + cReset }

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

// Bands renders the current sub-view as bands beneath the node, self-windowed to
// [scroll, scroll+winH); in steer it keeps the caret line visible.
func (v agentView) Bands(m *Model, it *item, rail string, width, scroll, winH int, focused bool) []string {
	var content []string
	caretContentLine := -1
	if v.sub(m, it) == subSteer {
		content, caretContentLine = v.steerContent(m, it, rail, width, focused)
	} else {
		content = v.observeContent(m, it, rail, width)
	}
	if focused && caretContentLine >= 0 {
		if caretContentLine < scroll {
			scroll = caretContentLine
		}
		if caretContentLine >= scroll+winH {
			scroll = caretContentLine - winH + 1
		}
	}
	if scroll < 0 {
		scroll = 0
	}
	if scroll > len(content) {
		scroll = len(content)
	}
	if focused {
		m.focusScroll = scroll
	}
	end := scroll + winH
	if end > len(content) {
		end = len(content)
	}
	return content[scroll:end]
}

// observeContent builds the sectioned observe pane lines (rail-prefixed).
func (v agentView) observeContent(m *Model, it *item, rail string, width int) []string {
	name := m.tree.displayName(it)
	if strings.TrimSpace(name) == "" {
		name = "untitled"
	}
	_, running := m.runCancel[it.uuid]
	status := statusWord(m.workerStatus[it.uuid], running)

	var c []string
	add := func(s string) { c = append(c, clip(rail+cReset+s, width)) }

	add(section("Agent"))
	for _, w := range wrapPlain(name, width-6) {
		add("   " + cFG + w + cReset)
	}
	add("")
	add(section("Status"))
	add("   " + statusColor(m.workerStatus[it.uuid]) + status + cReset)
	if u, ok := m.workerUsage[it.uuid]; ok {
		add("   " + cDim + fmt.Sprintf("↑%s ↓%s · $%.4f", ktok(u.in), ktok(u.out), u.cost) + cReset)
	}
	calls := m.workerActions[it.uuid]
	add("")
	add(section(fmt.Sprintf("Tool calls %d", len(calls))))
	if len(calls) == 0 {
		add("   " + cDim + "(no tool calls yet)" + cReset)
	} else {
		for _, a := range calls {
			line := "   " + toolColor(a.tool) + toolLabel(a.tool) + cReset
			if a.text != "" {
				line += cDim + " " + a.text + cReset
			}
			add(line)
		}
	}
	if running {
		if a, ok := m.workerAction[it.uuid]; ok && a.tool != "" {
			add("   " + toolColor(a.tool) + toolLabel(a.tool) + cReset + cDim + " " + a.text + "…" + cReset)
		}
	}
	add("")
	add(section("Final"))
	if md := m.workerDeliverable[it.uuid]; strings.TrimSpace(md) != "" {
		for _, l := range outlinePreview(md, width-2) {
			c = append(c, clip(rail+cReset+"  "+l, width))
		}
	} else if running {
		add("   " + cDim + "(running…)" + cReset)
	} else {
		add("   " + cDim + "(no result yet)" + cReset)
	}
	add("")
	add(" " + cDim + "j/k scroll · s steer · x stop · esc close" + cReset)
	return c
}

// steerContent builds the outline composer lines (rail-prefixed) and returns the
// content-line index of the caret (for scroll-follow).
func (v agentView) steerContent(m *Model, it *item, rail string, width int, focused bool) ([]string, int) {
	buf, caret := v.steerBuf(m, it)
	var c []string
	add := func(s string) { c = append(c, clip(rail+cReset+s, width)) }

	add(" " + cBold + cRed + "Steer" + cReset + cDim + " · " + cReset + cFG + "compose a follow-up" + cReset)
	caretLine, caretCol := jsonCaretLC(buf, caret)
	for i, raw := range strings.Split(buf, "\n") {
		trimmed := strings.TrimLeft(raw, " ")
		depth := (len([]rune(raw)) - len([]rune(trimmed))) / 2
		indent := strings.Repeat("  ", depth)
		glyph := cDim + "○ " + cReset
		if focused && i == caretLine {
			col := caretCol - (len([]rune(raw)) - len([]rune(trimmed)))
			if col < 0 {
				col = 0
			}
			add(" " + indent + glyph + cFG + withCaret(trimmed, col) + cReset)
		} else {
			add(" " + indent + glyph + cFG + trimmed + cReset)
		}
	}
	add(" " + cDim + "alt+s send · enter new node · tab indent · esc back" + cReset)
	return c, caretLine + 1 // +1 for the title line
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
			out = append(out, clip(" "+strings.Repeat("  ", depth)+cDim+"○ "+cReset+cFG+text+cReset, maxLine))
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
