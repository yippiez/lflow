package editor

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lflow/lflow/pkg/agent"
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

// agentInlineText turns worker/content text into a single safe outline-row
// string. Newlines and tabs become spaces (so raw line breaks never leak into a
// terminal row); other control bytes are stripped as render-boundary defense.
func agentInlineText(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	s = strings.Map(func(r rune) rune {
		switch r {
		case '\n', '\t':
			return ' '
		case 0x7F:
			return -1
		}
		if r < 0x20 {
			return -1
		}
		return r
	}, s)
	return strings.TrimSpace(s)
}

// agentBlockText keeps intentional newlines for note-like blocks while still
// neutralising tabs and terminal control bytes.
func agentBlockText(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	s = strings.Map(func(r rune) rune {
		switch r {
		case '\n':
			return r
		case '\t':
			return ' '
		case 0x7F:
			return -1
		}
		if r < 0x20 {
			return -1
		}
		return r
	}, s)
	return strings.TrimSpace(s)
}

// agentNodeLines renders one outline row inside the expanded view and soft-wraps
// it with a hanging indent under the node text. Wrapped lines are continuations
// of the same node, not new bullet rows, so long Final answers stay readable.
func agentNodeLines(rail string, depth int, styled string, width int) []string {
	indent := strings.Repeat("  ", depth)
	first := rail + cReset + indent + cDim + "○ " + cReset + styled
	cont := rail + cReset + indent + "  "
	return wrapLine(first, width, cont)
}

// agentSubLines renders a bullet-less child/detail row (used for notes) with the
// same hanging-wrap behavior as agentNodeLines.
func agentSubLines(rail string, depth int, styled string, width int) []string {
	indent := strings.Repeat("  ", depth+1) + "  "
	first := rail + cReset + indent + styled
	return wrapLine(first, width, rail+cReset+indent)
}

type agentInputLine struct {
	depth int
	text  string
}

func agentInputOutlineLines(s string) []agentInputLine {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	var out []agentInputLine
	for _, raw := range strings.Split(s, "\n") {
		trimmed := strings.TrimLeft(raw, " ")
		if strings.TrimSpace(trimmed) == "" {
			continue
		}
		lead := len([]rune(raw)) - len([]rune(trimmed))
		depth := 1 + lead/2
		text := strings.TrimSpace(trimmed)
		if strings.HasPrefix(text, "- ") {
			text = strings.TrimSpace(strings.TrimPrefix(text, "- "))
		}
		out = append(out, agentInputLine{depth: depth, text: text})
	}
	if len(out) == 0 {
		return []agentInputLine{{depth: 1, text: "untitled"}}
	}
	return out
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
		m.stopAgent(it)
		return nil, true
	case "down", "j", "pgdown":
		step := 1
		if k.String() == "pgdown" {
			step = 8
		}
		m.focusScroll += step
		return nil, true
	case "up", "k", "pgup":
		// In the Agent Domain, the focused worker detail occupies the whole lower
		// pane. Treat Up at the top as crossing the pane boundary back into the
		// main outline (matching outline Up at the top of the worker list); otherwise
		// it would be swallowed by the detail view and strand focus in the worker.
		if m.focusScroll == 0 && m.tempActive {
			v.Leave(m, it)
			m.focused = false
			m.exitTemp()
			return nil, true
		}
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
		// after sending, leave the agent view entirely → back to the outline (not
		// the detail/observe view)
		v.Leave(m, it)
		m.focused = false
		m.focusScroll = 0
		if msg == "" {
			return nil, true
		}
		m.flash = "steered"
		m.appendXcript(it.uuid, "you", msg)
		if s := m.liveSteer(it.uuid); s != nil {
			_ = s.Steer(msg) // same conversation, as composed
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

	var rows []orow
	node := func(depth int, styled string) { rows = append(rows, orow{depth: depth, styled: styled}) }
	sub := func(depth int, styled string) { rows = append(rows, orow{depth: depth, sub: true, styled: styled}) }

	// Agent → query as an outline. Worker prompts often originate from copied
	// pchain outline markdown ("- parent\n  - child"); strip only the structural
	// bullet marker so the preview reads as "○ child", not "○ - child".
	node(0, cFG+"Agent"+cReset)
	for _, ln := range agentInputOutlineLines(name) {
		node(ln.depth, cFG+agentInlineText(ln.text)+cReset)
	}
	// Status → one compact line: status, usage, elapsed, then model
	node(0, cFG+"Status"+cReset)
	line := statusColor(m.workerStatus[it.uuid]) + status + cReset
	if u, ok := m.workerUsage[it.uuid]; ok {
		line += cDim + fmt.Sprintf("  ↑%s ↓%s %s", ktok(u.in), ktok(u.out), costStr(u)) + cReset
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
			t += cDim + " " + agentInlineText(a.text) + cReset
		}
		node(1, t)
	}
	if running {
		if a, ok := m.workerAction[it.uuid]; ok && a.tool != "" {
			node(1, toolColor(a.tool)+toolLabel(a.tool)+cReset+cDim+" "+agentInlineText(a.text)+"…"+cReset)
		}
	} else if len(calls) == 0 {
		node(1, cDim+"none"+cReset)
	}
	// Transcript → the full conversation (you ↔ agent), restored on reopen so the
	// scrollback survives a quit, not just the last deliverable. Per-CLI agnostic.
	if xs := m.workerTranscript[it.uuid]; len(xs) > 0 {
		node(0, cFG+fmt.Sprintf("Transcript (%d)", len(xs))+cReset)
		for _, x := range xs {
			label, col := "you", cAccent
			if x.role == "agent" {
				label, col = "agent", cGreen
			}
			node(1, col+label+cReset)
			for _, raw := range strings.Split(agentBlockText(x.text), "\n") {
				for _, w := range wrapPlain(raw, width-2*2-6) {
					sub(1, cDim+w+cReset)
				}
			}
		}
	}
	// Final → the deliverable as nested nodes (with their custom types)
	node(0, cFG+"Final"+cReset)
	if nodes := parseDeliverNodes(m.workerDeliverable[it.uuid]); len(nodes) > 0 {
		var walk func(ns []deliverNode, depth int)
		walk = func(ns []deliverNode, depth int) {
			for _, n := range ns {
				txt := agentInlineText(n.Text)
				if txt == "" && len(n.Children) == 0 {
					continue
				}
				node(depth, renderBody(&item{typ: deliverType(n.Type)}, txt, -1, false))
				if note := agentBlockText(n.Note); note != "" {
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
	return renderObserveRows(rail, rows, width)
}

// orow is one collected observe-pane row before tree connectors are drawn: a
// bulleted node, or a bullet-less sub line (a note/continuation under a node).
type orow struct {
	depth  int
	sub    bool
	styled string
}

// renderObserveRows draws the collected rows with the same tree connectors as the
// main outline (│ continuation columns, ├─/╰─ drops), so the expanded agent view
// reads as a branched outline rather than flat indents. Depth-0 rows (the section
// headers) sit at the root with no connector; their children branch off them.
func renderObserveRows(rail string, rows []orow, width int) []string {
	n := len(rows)
	// isLast[i]: node row i is the last among its siblings (no later same-depth
	// node before the depth drops below it). Sub rows are skipped — they are
	// continuations of the preceding node, not tree nodes.
	isLast := make([]bool, n)
	for i := 0; i < n; i++ {
		if rows[i].sub {
			continue
		}
		d := rows[i].depth
		isLast[i] = true
		for j := i + 1; j < n; j++ {
			if rows[j].sub {
				continue
			}
			if rows[j].depth < d {
				break
			}
			if rows[j].depth == d {
				isLast[i] = false
				break
			}
		}
	}

	ancestorMore := map[int]bool{} // depth → current ancestor at that depth has a later sibling
	var out []string
	cont := "  " // continuation/sub prefix of the most recent node (under "○ ")
	for i := 0; i < n; i++ {
		r := rows[i]
		if r.sub {
			first := rail + cReset + cont + r.styled
			out = append(out, wrapLine(first, width, rail+cReset+cont)...)
			continue
		}
		var bars strings.Builder
		for k := 1; k < r.depth; k++ {
			if ancestorMore[k] {
				bars.WriteString("│  ")
			} else {
				bars.WriteString("   ")
			}
		}
		conn := bars.String()
		contBars := bars.String()
		if r.depth >= 1 {
			if isLast[i] {
				conn += "╰─ "
				contBars += "   "
			} else {
				conn += "├─ "
				contBars += "│  "
			}
		}
		ancestorMore[r.depth] = !isLast[i]
		cont = cDim + contBars + cReset + "  " // muted │ columns, aligned under "○ "
		first := rail + cReset + cDim + conn + "○ " + cReset + r.styled
		out = append(out, wrapLine(first, width, rail+cReset+cont)...)
	}
	return out
}

// steerContent builds the outline composer as nodes (a "Steer" node + one ○ per
// line, long lines WRAPPED) and returns the content-line index of the caret (for
// scroll-follow).
func (v agentView) steerContent(m *Model, it *item, rail string, width int, focused bool) ([]string, int) {
	buf, caret := v.steerBuf(m, it)
	var c []string
	c = append(c, agentNodeLines(rail, 0, cFG+"Steer"+cReset, width)...)
	caretLine, caretCol := jsonCaretLC(buf, caret)
	caretContentLine := 0
	railW := visibleWidth(rail)
	for i, raw := range strings.Split(buf, "\n") {
		trimmed := strings.TrimLeft(raw, " ")
		lead := len([]rune(raw)) - len([]rune(trimmed))
		depth := 1 + lead/2
		indent := strings.Repeat("  ", depth)
		textW := width - railW - len([]rune(indent)) - 2 // room after rail+indent+"○ "
		if textW < 8 {
			textW = 8
		}
		runes := []rune(trimmed)
		lineCaret := -1
		if focused && i == caretLine {
			if lineCaret = caretCol - lead; lineCaret < 0 {
				lineCaret = 0
			}
		}
		segs := wrapNoteSegs(runes, textW)
		placed := false
		for si, sg := range segs {
			seg := string(runes[sg.start:sg.end])
			prefix := indent + "  " // continuation aligns under the first line's text
			if si == 0 {
				prefix = indent + cDim + "○ " + cReset
			}
			body := cFG + seg + cReset
			if lineCaret >= 0 && !placed && lineCaret >= sg.start && (lineCaret < sg.end || si == len(segs)-1) {
				caretContentLine = len(c)
				body = cFG + withCaret(seg, lineCaret-sg.start) + cReset
				placed = true
			}
			c = append(c, clip(rail+cReset+prefix+body, width))
		}
	}
	return c, caretContentLine
}

// liveSteer returns the agent.Session for a worker whose process is still alive
// (running or idle-but-open), or nil if it has exited. Callers Steer it to push a
// follow-up into the same conversation.
func (m *Model) liveSteer(uuid string) agent.Session {
	if m.workerSess == nil {
		return nil
	}
	if _, alive := m.runCancel[uuid]; !alive {
		return nil
	}
	return m.workerSess[uuid]
}
