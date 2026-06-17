package editor

import (
	"bytes"
	"encoding/json"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// openJSON enters the full-panel json editor for a node. The buffer starts
// pretty-printed (or raw, if the stored JSON doesn't parse).
func (m *Model) openJSON(it *item) {
	m.mode = modeJSON
	m.jsonNode = it
	m.jsonBuf = prettyJSON(it.name)
	m.jsonCaret = len([]rune(m.jsonBuf))
	m.jsonScroll = 0
}

// prettyJSON indents valid JSON with two spaces; invalid input is returned
// unchanged so it can still be edited.
func prettyJSON(s string) string {
	t := strings.TrimSpace(s)
	if t == "" {
		return ""
	}
	var buf bytes.Buffer
	if err := json.Indent(&buf, []byte(t), "", "  "); err != nil {
		return s
	}
	return buf.String()
}

func (m *Model) jsonInsert(s string) {
	r := []rune(m.jsonBuf)
	c := m.jsonCaret
	if c > len(r) {
		c = len(r)
	}
	m.jsonBuf = string(r[:c]) + s + string(r[c:])
	m.jsonCaret = c + len([]rune(s))
}

// handleJSONKey drives the full-panel json editor: multiline text editing, then
// esc saves (pretty-printed if valid, raw otherwise — the outline marks raw red).
func (m *Model) handleJSONKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "esc":
		out := m.jsonBuf
		if json.Valid([]byte(strings.TrimSpace(out))) {
			out = prettyJSON(out)
		}
		if m.jsonNode != nil {
			m.jsonNode.name = out
			m.unsaved = true
		}
		m.mode = modeOutline
		m.caret = 0
		return m, nil
	case "left":
		if m.jsonCaret > 0 {
			m.jsonCaret--
		}
	case "right":
		if m.jsonCaret < len([]rune(m.jsonBuf)) {
			m.jsonCaret++
		}
	case "up":
		m.jsonCaret = jsonCaretLineMove(m.jsonBuf, m.jsonCaret, -1)
	case "down":
		m.jsonCaret = jsonCaretLineMove(m.jsonBuf, m.jsonCaret, +1)
	case "home":
		line, _ := jsonCaretLC(m.jsonBuf, m.jsonCaret)
		m.jsonCaret = jsonLCCaret(m.jsonBuf, line, 0)
	case "end":
		line, _ := jsonCaretLC(m.jsonBuf, m.jsonCaret)
		m.jsonCaret = jsonLCCaret(m.jsonBuf, line, 1<<30)
	case "enter":
		m.jsonInsert("\n")
	case "tab":
		m.jsonInsert("  ")
	case "backspace":
		r := []rune(m.jsonBuf)
		if m.jsonCaret > 0 {
			m.jsonBuf = string(r[:m.jsonCaret-1]) + string(r[m.jsonCaret:])
			m.jsonCaret--
		}
	default:
		if k.Type == tea.KeySpace && !k.Alt {
			m.jsonInsert(" ")
			return m, nil
		}
		if k.Type == tea.KeyRunes && !k.Alt {
			m.jsonInsert(string(k.Runes))
		}
	}
	return m, nil
}

// ── caret line/column helpers over a multiline buffer ──────────────────────
func jsonCaretLC(s string, caret int) (line, col int) {
	r := []rune(s)
	if caret > len(r) {
		caret = len(r)
	}
	for i := 0; i < caret; i++ {
		if r[i] == '\n' {
			line++
			col = 0
		} else {
			col++
		}
	}
	return
}

func jsonLCCaret(s string, line, col int) int {
	r := []rune(s)
	i, cur := 0, 0
	for i < len(r) && cur < line {
		if r[i] == '\n' {
			cur++
		}
		i++
	}
	c := 0
	for i < len(r) && r[i] != '\n' && c < col {
		i++
		c++
	}
	return i
}

func jsonCaretLineMove(s string, caret, dir int) int {
	line, col := jsonCaretLC(s, caret)
	line += dir
	if line < 0 {
		line = 0
	}
	return jsonLCCaret(s, line, col)
}

// ── view ───────────────────────────────────────────────────────────────────
func (m *Model) viewJSON(maxLine int) []string {
	valid := json.Valid([]byte(strings.TrimSpace(m.jsonBuf)))
	status := cDim + "valid" + cReset
	if !valid {
		status = cRed + "invalid" + cReset
	}
	header := cDim + " json · " + cReset + status + cDim + " · enter newline · tab indent · esc save" + cReset
	lines := []string{clip(header, maxLine), ""}

	bufLines := strings.Split(m.jsonBuf, "\n")
	caretLine, caretCol := jsonCaretLC(m.jsonBuf, m.jsonCaret)

	winH := m.height - 4
	if winH < 3 {
		winH = 3
	}
	if caretLine < m.jsonScroll {
		m.jsonScroll = caretLine
	}
	if caretLine >= m.jsonScroll+winH {
		m.jsonScroll = caretLine - winH + 1
	}
	if m.jsonScroll < 0 {
		m.jsonScroll = 0
	}
	end := min(m.jsonScroll+winH, len(bufLines))
	for i := m.jsonScroll; i < end; i++ {
		if i == caretLine {
			// the edited line shows a block caret; keep it plain so the caret is clear
			lines = append(lines, clip(" "+cFG+withCaret(bufLines[i], caretCol)+cReset, maxLine))
		} else {
			lines = append(lines, clip(" "+colorJSONLine(bufLines[i]), maxLine))
		}
	}
	lines = append(lines, m.bottomBar(maxLine))
	return lines
}

// colorJSONLine is a tolerant per-line JSON colorizer (works on invalid/partial
// JSON while editing): keys blue, string values orange, numbers green,
// true/false/null cyan, punctuation dim.
func colorJSONLine(line string) string {
	r := []rune(line)
	var b strings.Builder
	i := 0
	for i < len(r) {
		switch c := r[i]; {
		case c == '"':
			j := i + 1
			for j < len(r) {
				if r[j] == '\\' {
					j += 2
					continue
				}
				if r[j] == '"' {
					break
				}
				j++
			}
			endQuote := min(j+1, len(r))
			str := string(r[i:endQuote])
			k := endQuote
			for k < len(r) && r[k] == ' ' {
				k++
			}
			col := styleColorCode["orange"] // string value
			if k < len(r) && r[k] == ':' {
				col = cAccent // key
			}
			b.WriteString(col + str + cReset)
			i = endQuote
		case c == '-' || (c >= '0' && c <= '9'):
			j := i
			for j < len(r) && (r[j] == '-' || r[j] == '+' || r[j] == '.' ||
				r[j] == 'e' || r[j] == 'E' || (r[j] >= '0' && r[j] <= '9')) {
				j++
			}
			b.WriteString(styleColorCode["green"] + string(r[i:j]) + cReset)
			i = j
		case c == '{' || c == '}' || c == '[' || c == ']' || c == ':' || c == ',':
			b.WriteString(cDim + string(c) + cReset)
			i++
		default:
			if w := jsonWordAt(r, i); w != "" {
				b.WriteString(styleColorCode["cyan"] + w + cReset)
				i += len([]rune(w))
			} else {
				b.WriteRune(c)
				i++
			}
		}
	}
	return b.String()
}

func jsonWordAt(r []rune, i int) string {
	for _, w := range []string{"true", "false", "null"} {
		wr := []rune(w)
		if i+len(wr) <= len(r) && string(r[i:i+len(wr)]) == w {
			return w
		}
	}
	return ""
}
