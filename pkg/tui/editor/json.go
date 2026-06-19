package editor

import (
	"bytes"
	"encoding/json"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// jsonView is the JSON node's inline expanded editor (alt+e): a multiline buffer
// edited in bands beneath the node, never a separate screen. The live buffer is
// kept in the ephemeral per-node store and flushed to it.name on Leave (esc).
type jsonView struct{}

func (jsonView) get(m *Model, it *item) (string, int) {
	d := m.nodeStore(it.uuid)
	buf, _ := d["jsonBuf"].(string)
	caret, _ := d["jsonCaret"].(int)
	return buf, caret
}

func (jsonView) set(m *Model, it *item, buf string, caret int) {
	d := m.nodeStore(it.uuid)
	d["jsonBuf"] = buf
	d["jsonCaret"] = caret
}

// Enter seeds the edit buffer (pretty-printed) and places the caret at the end.
func (v jsonView) Enter(m *Model, it *item) bool {
	buf := prettyJSON(it.name)
	v.set(m, it, buf, len([]rune(buf)))
	return true
}

// Leave saves the buffer back to the node (pretty-printed if valid) and clears
// the ephemeral edit state.
func (v jsonView) Leave(m *Model, it *item) {
	buf, _ := v.get(m, it)
	if json.Valid([]byte(strings.TrimSpace(buf))) {
		buf = prettyJSON(buf)
	}
	it.name = buf
	m.unsaved = true
	d := m.nodeStore(it.uuid)
	delete(d, "jsonBuf")
	delete(d, "jsonCaret")
}

// Lines is header (1) + one per buffer line.
func (v jsonView) Lines(m *Model, it *item, width int) int {
	buf, _ := v.get(m, it)
	return 1 + len(strings.Split(buf, "\n"))
}

// Key edits the buffer; esc/ctrl+c fall through to central handling.
func (v jsonView) Key(m *Model, it *item, k tea.KeyMsg) (tea.Cmd, bool) {
	buf, caret := v.get(m, it)
	switch k.String() {
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
	case "home":
		line, _ := jsonCaretLC(buf, caret)
		caret = jsonLCCaret(buf, line, 0)
	case "end":
		line, _ := jsonCaretLC(buf, caret)
		caret = jsonLCCaret(buf, line, 1<<30)
	case "enter":
		buf, caret = jsonIns(buf, caret, "\n")
	case "tab":
		buf, caret = jsonIns(buf, caret, "  ")
	case "backspace":
		r := []rune(buf)
		if caret > 0 {
			buf = string(r[:caret-1]) + string(r[caret:])
			caret--
		}
	default:
		switch {
		case k.Type == tea.KeySpace && !k.Alt:
			buf, caret = jsonIns(buf, caret, " ")
		case k.Type == tea.KeyRunes && !k.Alt:
			buf, caret = jsonIns(buf, caret, string(k.Runes))
		default:
			return nil, false // not ours (esc, ctrl+c, …) → central
		}
	}
	v.set(m, it, buf, caret)
	return nil, true
}

// Bands renders the editor body as bands: a header line, then the buffer lines
// (colorized, caret on the active line), self-windowed to [scroll, scroll+winH)
// with the caret kept visible.
func (v jsonView) Bands(m *Model, it *item, rail string, width, scroll, winH int, focused bool) []string {
	buf, caret := v.get(m, it)
	valid := json.Valid([]byte(strings.TrimSpace(buf)))
	status := cDim + "valid" + cReset
	if !valid {
		status = cRed + "invalid" + cReset
	}
	var content []string
	content = append(content, clip(rail+cReset+cDim+"  json · "+cReset+status+cDim+" · enter newline · tab indent · esc save"+cReset, width))
	caretLine, caretCol := jsonCaretLC(buf, caret)
	for i, bl := range strings.Split(buf, "\n") {
		if focused && i == caretLine {
			content = append(content, clip(rail+cReset+"  "+cFG+withCaret(bl, caretCol)+cReset, width))
		} else {
			content = append(content, clip(rail+cReset+"  "+colorJSONLine(bl), width))
		}
	}
	if focused { // keep the caret line in view (caretLine + 1 for the header)
		cl := caretLine + 1
		if cl < scroll {
			scroll = cl
		}
		if cl >= scroll+winH {
			scroll = cl - winH + 1
		}
		if scroll < 0 {
			scroll = 0
		}
		m.focusScroll = scroll
	}
	if scroll > len(content) {
		scroll = len(content)
	}
	end := scroll + winH
	if end > len(content) {
		end = len(content)
	}
	return content[scroll:end]
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

func jsonIns(buf string, caret int, s string) (string, int) {
	r := []rune(buf)
	if caret > len(r) {
		caret = len(r)
	}
	return string(r[:caret]) + s + string(r[caret:]), caret + len([]rune(s))
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
