package nodes

import (
	"bytes"
	"encoding/json"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lflow/lflow/packages/database"
	"github.com/lflow/lflow/packages/utils"
)

// The JSON node: a document (it stores JSON text as its name) shown inline as
// a compact one-line preview and edited full-screen-in-band through alt+e
// (jsonView) — never inline, since a document has no sane single-line edit
// surface. Invalid JSON turns the {} marker red with a "parsing failed" tail;
// alt+e always seeds a pretty-printed buffer regardless.
func init() {
	Register(Plugin{
		Key:            database.TypeJSON,
		Label:          "JSON",
		InlineEditable: false,
		RenderPlain:    func(n Ref, _ string) string { return renderJSONPreview(n) },
		View:           jsonView{},
	})
}

// renderJSONPreview renders a json node as a one-line entry: a {} marker plus a
// whitespace-collapsed, truncated preview of the JSON. Invalid JSON turns the {}
// marker red and appends a red " · JSON parsing failed". Editing happens only in
// the alt+e editor, so this is never an inline edit surface.
func renderJSONPreview(n Ref) string {
	th := Theme()
	trimmed := strings.TrimSpace(n.Text())
	if trimmed == "" {
		return th.Dim + "{}" + th.Reset + " " + th.Dim + "empty" + th.Reset
	}
	if json.Valid([]byte(trimmed)) {
		return th.Dim + "{}" + th.Reset + " " + th.FG + jsonPreview(n.Text(), 50) + th.Reset
	}
	return th.Red + "{}" + th.Reset + " " + th.FG + jsonPreview(n.Text(), 50) + th.Reset +
		th.Red + " · JSON parsing failed" + th.Reset
}

// jsonPreview collapses whitespace and truncates to n display runes.
func jsonPreview(s string, n int) string {
	one := strings.Join(strings.Fields(s), " ")
	r := []rune(one)
	if len(r) > n {
		return string(r[:n]) + "…"
	}
	return one
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

// jsonIns inserts s into buf at the rune index caret, returning the new buffer
// and the caret position after the insert. Shared with the Code node's editor.
func jsonIns(buf string, caret int, s string) (string, int) {
	r := []rune(buf)
	if caret > len(r) {
		caret = len(r)
	}
	return string(r[:caret]) + s + string(r[caret:]), caret + len([]rune(s))
}

// colorJSONLine is a tolerant per-line JSON colorizer (works on invalid/partial
// JSON while editing): keys accent, string values yellow (the palette's closest
// to the editor's dedicated orange, which Theme doesn't carry), numbers green,
// true/false/null cyan, punctuation dim.
func colorJSONLine(line string) string {
	th := Theme()
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
			col := th.Yellow // string value
			if k < len(r) && r[k] == ':' {
				col = th.Accent // key
			}
			b.WriteString(col + str + th.Reset)
			i = endQuote
		case c == '-' || (c >= '0' && c <= '9'):
			j := i
			for j < len(r) && (r[j] == '-' || r[j] == '+' || r[j] == '.' ||
				r[j] == 'e' || r[j] == 'E' || (r[j] >= '0' && r[j] <= '9')) {
				j++
			}
			b.WriteString(th.Green + string(r[i:j]) + th.Reset)
			i = j
		case c == '{' || c == '}' || c == '[' || c == ']' || c == ':' || c == ',':
			b.WriteString(th.Dim + string(c) + th.Reset)
			i++
		default:
			if w := jsonWordAt(r, i); w != "" {
				b.WriteString(th.Cyan + w + th.Reset)
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

// ── the JSON node's inline expanded editor (a PluginView) ──────────────────

// jsonView is the JSON node's inline expanded view (alt+e): a multiline buffer
// edited in bands beneath the node, never a separate screen. The live buffer is
// kept in the ephemeral per-node store (NodeStore) and flushed to the node's
// text on Leave (esc).
type jsonView struct{}

func jsonViewGet(h Host, n Ref) (string, int) {
	d := h.NodeStore(n.UUID())
	buf, _ := d["jsonBuf"].(string)
	caret, _ := d["jsonCaret"].(int)
	return buf, caret
}

func jsonViewSet(h Host, n Ref, buf string, caret int) {
	d := h.NodeStore(n.UUID())
	d["jsonBuf"] = buf
	d["jsonCaret"] = caret
}

// Enter seeds the edit buffer (pretty-printed) and places the caret at the end.
func (jsonView) Enter(h Host, n Ref) bool {
	buf := prettyJSON(n.Text())
	jsonViewSet(h, n, buf, len([]rune(buf)))
	return true
}

// Leave saves the buffer back to the node (pretty-printed if valid) and clears
// the ephemeral edit state.
func (jsonView) Leave(h Host, n Ref) {
	buf, _ := jsonViewGet(h, n)
	if json.Valid([]byte(strings.TrimSpace(buf))) {
		buf = prettyJSON(buf)
	}
	n.SetText(buf)
	d := h.NodeStore(n.UUID())
	delete(d, "jsonBuf")
	delete(d, "jsonCaret")
}

// Lines is header (1) + one per buffer line.
func (jsonView) Lines(h Host, n Ref, width int) int {
	buf, _ := jsonViewGet(h, n)
	return 1 + len(strings.Split(buf, "\n"))
}

// Key edits the buffer; esc/ctrl+c fall through to central handling.
func (jsonView) Key(h Host, n Ref, k tea.KeyMsg) (tea.Cmd, bool) {
	buf, caret := jsonViewGet(h, n)
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
		caret = utils.CaretVMove(buf, caret, -1)
	case "down":
		caret = utils.CaretVMove(buf, caret, +1)
	case "home":
		line, _ := utils.CaretLineCol(buf, caret)
		caret = utils.CaretAt(buf, line, 0)
	case "end":
		line, _ := utils.CaretLineCol(buf, caret)
		caret = utils.CaretAt(buf, line, 1<<30)
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
	jsonViewSet(h, n, buf, caret)
	return nil, true
}

// Bands renders the editor body as bands: a header line, then the buffer lines
// (colorized, caret on the active line), windowed to [scroll, scroll+winH).
// While focused, the computed scroll is persisted through SetFocusScroll so
// the caret-follow auto-scroll carries into the next frame.
func (jsonView) Bands(h Host, n Ref, rail string, width, scroll, winH int, focused bool) []string {
	th := Theme()
	buf, caret := jsonViewGet(h, n)
	valid := json.Valid([]byte(strings.TrimSpace(buf)))
	status := th.Dim + "valid" + th.Reset
	if !valid {
		status = th.Red + "invalid" + th.Reset
	}
	var content []string
	content = append(content, rail+th.Reset+th.Dim+"  json · "+th.Reset+status+th.Dim+" · enter newline · tab indent · esc save"+th.Reset)
	caretLine, caretCol := utils.CaretLineCol(buf, caret)
	for i, bl := range strings.Split(buf, "\n") {
		if focused && i == caretLine {
			content = append(content, rail+th.Reset+"  "+th.FG+jsonWithCaret(bl, caretCol)+th.Reset)
		} else {
			content = append(content, rail+th.Reset+"  "+colorJSONLine(bl))
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
		SetFocusScroll(h, scroll)
	}
	return WindowBands(content, scroll, winH)
}

// jsonWithCaret draws the caret as an inverted cell over the rune it sits on
// — the editor's own caret style. Reverse-video is a structural SGR code, not
// a palette color, so it is applied directly rather than through Theme().
func jsonWithCaret(text string, caret int) string {
	const cInvert = "\x1b[7m"
	th := Theme()
	runes := []rune(text)
	if caret < 0 {
		caret = 0
	}
	if caret >= len(runes) {
		return string(runes) + cInvert + " " + th.Reset + th.FG
	}
	return string(runes[:caret]) + cInvert + string(runes[caret]) + th.Reset + th.FG + string(runes[caret+1:])
}
