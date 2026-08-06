package editor

import (
	"encoding/json"
	"os"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mattn/go-runewidth"

	"github.com/lflow/lflow/tui/database"
)

// alt+e on a session chip or Agent node opens the same local panel. Its header
// identifies the session; beneath it, the CLI transcript is rendered as fixed,
// read-only virtual rows. The records are read on demand and never copied into
// the outline or sync state.

type agentChipView struct{}

type agentHandle struct {
	id   string
	v    agentVariant
	sess agentSession
}

type agentTrace struct {
	kind string
	text string
}

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

func (m *Model) focusAgentChip(c database.Chip) {
	m.focusChip = c.ID
	m.focused = true
	m.focusScroll = 0
	if h, ok := agentChipHandle(m); ok {
		m.refreshAgentTrace(h)
	}
}

func (agentChipView) enter(m *Model, it *item) bool { return m.focusChip != "" }

func (agentChipView) leave(m *Model, it *item) {
	if m.focusChip != "" {
		delete(m.nodeStore(m.focusChip), "agentRename")
	}
	m.focusChip = ""
}

func (agentChipView) lines(m *Model, it *item, width int) int {
	h, ok := agentChipHandle(m)
	if !ok {
		return 0
	}
	return len(m.agentBandContent(h, "", width))
}

func (agentChipView) key(m *Model, it *item, k tea.KeyMsg) (tea.Cmd, bool) {
	h, ok := agentChipHandle(m)
	if !ok {
		return nil, false
	}
	return m.agentViewKey(h, k)
}

func (agentChipView) bands(m *Model, it *item, rail string, width, scroll, winH int, focused bool) []string {
	h, ok := agentChipHandle(m)
	if !ok {
		return nil
	}
	return WindowBands(m.agentBandContent(h, rail, width), scroll, winH)
}

func (m *Model) agentRenameState(id string) *textField {
	f, _ := m.nodeStore(id)["agentRename"].(*textField)
	return f
}

// agentViewKey drives both session surfaces. Plain navigation explores the
// trace; r re-reads records that moved while the panel was open.
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
		f.handleKey(k)
		return nil, true
	}
	switch k.String() {
	case "down", "j":
		m.focusScroll++
		return nil, true
	case "up", "k":
		m.focusScroll = max(0, m.focusScroll-1)
		return nil, true
	case "pgdown":
		m.focusScroll += 10
		return nil, true
	case "pgup":
		m.focusScroll = max(0, m.focusScroll-10)
		return nil, true
	case "home", "g":
		m.focusScroll = 0
		return nil, true
	case "end", "G":
		m.focusScroll = 1 << 30
		return nil, true
	case "r":
		m.refreshAgentTrace(h)
		m.flash = "session trace refreshed"
		return nil, true
	case "n", "alt+n":
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
	case "alt+c":
		m.openAgentColorID(h.id)
		return nil, true
	}
	return nil, false
}

func (m *Model) agentBandContent(h agentHandle, rail string, width int) []string {
	if width <= 0 {
		width = 1 << 20
	}
	color := m.agentColorFor(h.id, h.v, h.sess)
	line := func(s string) string { return clip(rail+cReset+s, width) }

	if f := m.agentRenameState(h.id); f != nil {
		hint := " · enter save · esc cancel"
		head := "  " + cDim + "name  " + cReset + withCaret(f.value, f.caret) + cDim + hint + cReset
		return []string{line(head), line(cDim + "  " + tildePath(h.sess.Cwd) + cReset), line(cDim + agentPanelKeys + cReset)}
	}

	ink := bgOf(color) + contrastInk(color)
	body := width - 6
	if body < 8 {
		body = 8
	}
	var content []string
	for i, chunk := range wrapPlain(h.v.glyph+" "+m.agentTitle(h.id, h.v, h.sess), body) {
		pad := ""
		if i > 0 {
			pad = strings.Repeat(" ", runewidth.StringWidth(h.v.glyph)+1)
		}
		content = append(content, line("  "+ink+" "+pad+chunk+" "+cReset))
	}
	if h.sess.Cwd != "" {
		content = append(content, line(cDim+"  "+tildePath(h.sess.Cwd)+cReset))
	}

	traces := m.agentTraces(h)
	if len(traces) == 0 {
		content = append(content, line(cDim+"  traces · no readable records"+cReset))
	} else {
		content = append(content, line(cDim+"  traces · "+strconv.Itoa(len(traces))+" events · fixed local view"+cReset))
		for i, tr := range traces {
			branch := "├─"
			if i == len(traces)-1 {
				branch = "└─"
			}
			mark, label, style := agentTraceLook(tr.kind)
			prefix := "  " + branch + " " + style + mark + " " + label + cReset + " "
			continuation := "     " + strings.Repeat(" ", runewidth.StringWidth(mark+" "+label+" "))
			traceWidth := max(8, width-runewidth.StringWidth(stripSGR(rail+prefix)))
			chunks := wrapPlain(tr.text, traceWidth)
			if len(chunks) == 0 {
				chunks = []string{"·"}
			}
			for j, chunk := range chunks {
				p := prefix
				if j > 0 {
					p = continuation
				}
				content = append(content, line(p+chunk+cReset))
			}
		}
	}
	return append(content, line(cDim+agentPanelKeys+cReset))
}

const agentPanelKeys = "  ↑↓ trace · r refresh · alt+r open · alt+n rename · alt+c color · esc close"
const agentTraceCap = 4 << 20

func agentTraceLook(kind string) (mark, label, style string) {
	switch kind {
	case "user":
		return "◆", "you", cYellow
	case "assistant":
		return "→", "agent", cFG
	case "tool":
		return "$", "tool", cMagenta
	case "result":
		return "·", "result", cDim
	case "thinking":
		return "·", "thinking", cDim
	default:
		return "·", kind, cDim
	}
}

func (m *Model) refreshAgentTrace(h agentHandle) {
	m.nodeStore(h.id)["agentTrace"] = readAgentTrace(h.v, h.sess)
}

func (m *Model) agentTraces(h agentHandle) []agentTrace {
	if traces, ok := m.nodeStore(h.id)["agentTrace"].([]agentTrace); ok {
		return traces
	}
	m.refreshAgentTrace(h)
	traces, _ := m.nodeStore(h.id)["agentTrace"].([]agentTrace)
	return traces
}

func readAgentTrace(v agentVariant, s agentSession) []agentTrace {
	if db := v.openAgentDB(); db != nil {
		defer db.Close()
		return db.trace(s.SessionID)
	}
	path := agentSessionPath(v.sessionDirs(), v.exts, s.SessionID)
	st, err := os.Stat(path)
	if err != nil || st.IsDir() {
		return nil
	}
	var records []map[string]any
	if st.Size() <= agentTraceCap {
		records = agentReadRecords(path, 0)
	} else {
		records = agentReadTail(path, agentTraceCap)
	}
	var out []agentTrace
	for _, rec := range records {
		out = append(out, agentRecordTrace(rec)...)
	}
	return out
}

func agentRecordTrace(rec map[string]any) []agentTrace {
	role := strings.ToLower(agentString(rec, "role", "type"))
	content := rec["content"]
	if msg, ok := rec["message"].(map[string]any); ok {
		if r := strings.ToLower(agentString(msg, "role")); r != "" {
			role = r
		}
		content = msg["content"]
	}
	if content == nil {
		content = rec["parts"]
	}
	kind := role
	switch role {
	case "human":
		kind = "user"
	case "message":
		kind = strings.ToLower(agentString(rec, "role"))
	case "summary", "session", "session_info", "custom-title", "agent-name", "agent-color":
		return nil
	}
	if kind != "user" && kind != "assistant" {
		if strings.Contains(role, "tool") {
			out := agentContentTrace("result", content)
			if len(out) > 0 {
				return out
			}
			text := agentString(rec, "name", "tool", "toolName", "command", "output", "result")
			if text != "" {
				return []agentTrace{{kind: "tool", text: oneLine(text)}}
			}
		}
		return nil
	}
	return agentContentTrace(kind, content)
}

func agentContentTrace(role string, content any) []agentTrace {
	switch value := content.(type) {
	case string:
		text := strings.TrimSpace(value)
		if role == "user" {
			text = agentPromptText(text)
		}
		if text == "" {
			return nil
		}
		return []agentTrace{{kind: role, text: oneLine(text)}}
	case []any:
		var out []agentTrace
		for _, raw := range value {
			part, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			typ := strings.ToLower(agentString(part, "type"))
			switch typ {
			case "text", "input_text", "output_text":
				out = append(out, agentContentTrace(role, agentString(part, "text"))...)
			case "thinking", "reasoning":
				if text := oneLine(agentString(part, "thinking", "text")); text != "" {
					out = append(out, agentTrace{kind: "thinking", text: text})
				}
			case "tool_use", "tool_call", "toolcall", "function_call", "functioncall":
				text := agentString(part, "name", "tool")
				if args := part["input"]; args != nil {
					text = strings.TrimSpace(text + " " + compactAgentValue(args))
				} else if args := part["arguments"]; args != nil {
					text = strings.TrimSpace(text + " " + compactAgentValue(args))
				}
				out = append(out, agentTrace{kind: "tool", text: text})
			case "tool_result", "toolresult", "function_result", "functionresult":
				result := part["content"]
				if result == nil {
					result = part["result"]
				}
				text := compactAgentValue(result)
				if text != "" {
					out = append(out, agentTrace{kind: "result", text: text})
				}
			}
		}
		return out
	}
	return nil
}

func compactAgentValue(v any) string {
	if s, ok := v.(string); ok {
		return clipStr(oneLine(s), 400)
	}
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return clipStr(string(b), 400)
}
