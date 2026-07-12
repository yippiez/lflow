package editor

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// The Code node is a multi-line code block: a fully gray background, a thin
// white left rule with box corners, and dim line numbers. The code lives in
// it.name (multi-line, like the json node's document) so it syncs and greps as
// plain text. alt+e focuses the block for editing; Enter makes a newline INSIDE
// the block, and two spaces in a row at the end exit to a fresh sibling (the
// outline gesture — the trailing spaces are trimmed). esc leaves in place.
//
// CodeBlockBands is the shared renderer — the nlpcompute node's code face draws
// through it too, so both wear the same gray block.

// cWhite is the code block's left rule and corners — white is white, never
// themed (like the painter bar).
const cWhite = "\x1b[38;2;255;255;255m"

// CodeBlockBands renders code as the standard gray code block. header labels the
// top rule (e.g. "code" or "python · esc"); caret is the rune index of the block
// cursor when focused (< 0 draws none). rail is the tree hanging indent, width
// the render width. When winH > 0 the block is windowed to [scroll, scroll+winH)
// with the caret line kept in view (the focused editor); winH <= 0 returns the
// whole block (the always-on band).
func CodeBlockBands(code, header string, caret int, focused bool, rail string, width, scroll, winH int) []string {
	inner := width - visibleWidth(rail) - 1
	if inner < 8 {
		inner = 8
	}
	caretLine, caretCol := -1, -1
	if focused && caret >= 0 {
		caretLine, caretCol = jsonCaretLC(code, caret)
	}
	lines := strings.Split(code, "\n")
	numW := len(fmt.Sprintf("%d", len(lines)))

	all := make([]string, 0, len(lines)+2)
	all = append(all, rail+cReset+bgCode+codeRule("┌", header, inner))
	for i, l := range lines {
		num := cDim + fmt.Sprintf("%*d", numW, i+1) + cReset + bgCode
		body := HLCodeLine(l)
		if i == caretLine {
			body = codeCaretLine(l, caretCol)
		}
		gutter := cWhite + "│" + cReset + bgCode + " " + num + " "
		all = append(all, rail+cReset+bgCode+padGray(gutter+body, inner))
	}
	all = append(all, rail+cReset+bgCode+codeRule("└", "", inner))

	if winH <= 0 {
		return all
	}
	if caretLine >= 0 { // keep the caret line (offset +1 for the header) in view
		cl := caretLine + 1
		if cl < scroll {
			scroll = cl
		}
		if cl >= scroll+winH {
			scroll = cl - winH + 1
		}
	}
	if scroll > len(all)-winH {
		scroll = len(all) - winH
	}
	if scroll < 0 {
		scroll = 0
	}
	end := scroll + winH
	if end > len(all) {
		end = len(all)
	}
	return all[scroll:end]
}

// codeRule builds a top/bottom rule: a white corner, an optional dim label, and
// a white dashed fill out to cols, all over the gray background.
func codeRule(corner, label string, cols int) string {
	head := cWhite + corner
	used := 1
	if label != "" {
		head += cReset + bgCode + " " + cDim + label + cReset + bgCode + " " + cWhite
		used += 2 + visibleWidth(label)
	}
	rem := cols - used
	if rem < 0 {
		rem = 0
	}
	return head + strings.Repeat("─", rem) + cReset
}

// padGray pads a styled inner string (drawn under an active bgCode) with gray
// spaces out to cols visible columns, then resets; over-long input is clipped.
func padGray(inner string, cols int) string {
	if w := visibleWidth(inner); w < cols {
		return inner + strings.Repeat(" ", cols-w) + cReset
	}
	return clip(inner, cols) + cReset
}

// codeCaretLine draws one raw code line with the block cursor inverted at caret;
// the caret line is not syntax-colored so the cursor cell reads cleanly.
func codeCaretLine(line string, caret int) string {
	r := []rune(line)
	if caret < 0 {
		caret = 0
	}
	var b strings.Builder
	b.WriteString(cFG)
	for i, c := range r {
		if i == caret {
			b.WriteString(cInvert + string(c) + cReset + bgCode + cFG)
		} else {
			b.WriteString(string(c))
		}
	}
	if caret >= len(r) {
		b.WriteString(cInvert + " " + cReset + bgCode + cFG)
	}
	return b.String()
}

// codeKeywords is the small, cross-language keyword set for HLCodeLine —
// python/go/js flavored, nothing token-perfect.
var codeKeywords = map[string]bool{
	"def": true, "return": true, "if": true, "else": true, "elif": true, "for": true,
	"while": true, "import": true, "from": true, "class": true, "func": true,
	"var": true, "let": true, "const": true, "type": true, "struct": true,
	"range": true, "in": true, "not": true, "and": true, "or": true, "with": true,
	"try": true, "except": true, "finally": true, "raise": true, "lambda": true,
	"go": true, "defer": true, "switch": true, "case": true, "break": true,
	"continue": true, "package": true, "function": true, "async": true, "await": true,
	"True": true, "False": true, "None": true, "true": true, "false": true,
	"nil": true, "null": true, "pass": true, "yield": true, "print": true,
}

// HLCodeLine is the shared simple highlighter: comments dim, strings orange,
// numbers green, keywords accent — tolerant, line-local, nothing clever.
func HLCodeLine(line string) string {
	trimmed := strings.TrimSpace(line)
	if strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, "//") {
		return cDim + line + cReset + bgCode
	}
	r := []rune(line)
	var b strings.Builder
	i := 0
	for i < len(r) {
		c := r[i]
		switch {
		case c == '"' || c == '\'':
			q := c
			j := i + 1
			for j < len(r) && r[j] != q {
				if r[j] == '\\' {
					j++
				}
				j++
			}
			if j < len(r) {
				j++
			}
			b.WriteString(styleColorCode["orange"] + string(r[i:j]) + cReset + bgCode)
			i = j
		case c == '#' || (c == '/' && i+1 < len(r) && r[i+1] == '/'):
			b.WriteString(cDim + string(r[i:]) + cReset + bgCode)
			i = len(r)
		case c >= '0' && c <= '9':
			j := i
			for j < len(r) && ((r[j] >= '0' && r[j] <= '9') || r[j] == '.' || r[j] == '_') {
				j++
			}
			b.WriteString(cGreen + string(r[i:j]) + cReset + bgCode)
			i = j
		case isCodeWord(c):
			j := i
			for j < len(r) && isCodeWord(r[j]) {
				j++
			}
			word := string(r[i:j])
			if codeKeywords[word] {
				b.WriteString(cAccent + word + cReset + bgCode)
			} else {
				b.WriteString(cFG + word + cReset + bgCode)
			}
			i = j
		default:
			b.WriteRune(c)
			i++
		}
	}
	return b.String()
}

func isCodeWord(c rune) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
}

// ── the Code node's inline editor (a nodeView) ─────────────────────────────

// codeView edits the code node's multi-line body (it.name). The live buffer is
// kept in the ephemeral per-node store and flushed on Leave, like jsonView.
type codeView struct{}

func (codeView) get(m *Model, it *item) (string, int) {
	d := m.nodeStore(it.uuid)
	buf, _ := d["codeBuf"].(string)
	caret, _ := d["codeCaret"].(int)
	return buf, caret
}

func (codeView) set(m *Model, it *item, buf string, caret int) {
	d := m.nodeStore(it.uuid)
	d["codeBuf"] = buf
	d["codeCaret"] = caret
}

// Enter seeds the buffer from the node and parks the caret at the end.
func (v codeView) Enter(m *Model, it *item) bool {
	v.set(m, it, it.name, len([]rune(it.name)))
	return true
}

// Leave flushes the buffer back to the node and clears the edit state.
func (v codeView) Leave(m *Model, it *item) {
	buf, _ := v.get(m, it)
	if buf != it.name {
		it.name = buf
		m.unsaved = true
	}
	d := m.nodeStore(it.uuid)
	delete(d, "codeBuf")
	delete(d, "codeCaret")
}

// Lines is the header + one per buffer line + the footer rule.
func (v codeView) Lines(m *Model, it *item, width int) int {
	buf, _ := v.get(m, it)
	return 2 + len(strings.Split(buf, "\n"))
}

// Key edits the buffer. Enter is a newline inside the block; two spaces at the
// end exit to a fresh sibling (the trailing spaces trimmed); esc/ctrl+c fall
// through to central handling.
func (v codeView) Key(m *Model, it *item, k tea.KeyMsg) (tea.Cmd, bool) {
	buf, caret := v.get(m, it)
	rl := []rune(buf)
	switch k.String() {
	case "left":
		if caret > 0 {
			caret--
		}
	case "right":
		if caret < len(rl) {
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
		if caret > 0 {
			buf = string(rl[:caret-1]) + string(rl[caret:])
			caret--
		}
	default:
		switch {
		case k.Type == tea.KeySpace && !k.Alt:
			// two spaces after a content char at the buffer's end exit to a new
			// sibling (the "done editing" gesture) — the pending pair is trimmed.
			// A space that follows a newline or another space stays literal, so
			// leading and nested indentation type through; indent with Tab to be safe.
			if caret == len(rl) && caret >= 2 && rl[caret-1] == ' ' &&
				rl[caret-2] != ' ' && rl[caret-2] != '\n' {
				v.set(m, it, string(rl[:caret-1]), caret-1)
				return m.exitCodeToSibling(it), true
			}
			buf, caret = jsonIns(buf, caret, " ")
		case k.Type == tea.KeyRunes && !k.Alt:
			buf, caret = jsonIns(buf, caret, string(k.Runes))
		default:
			return nil, false // esc, ctrl+c … → central
		}
	}
	v.set(m, it, buf, caret)
	return nil, true
}

// Bands renders the focused editor through the shared block.
func (v codeView) Bands(m *Model, it *item, rail string, width, scroll, winH int, focused bool) []string {
	buf, caret := v.get(m, it)
	if !focused {
		caret = -1
	}
	return CodeBlockBands(buf, "code · enter newline · ␣␣ exit · esc done", caret, focused, rail, width, scroll, winH)
}

// exitCodeToSibling flushes the code buffer, drops focus, and opens a fresh
// sibling after the node with the cursor on it — the two-space exit gesture.
func (m *Model) exitCodeToSibling(it *item) tea.Cmd {
	codeView{}.Leave(m, it)
	m.focused = false
	sib, err := m.tree.insertSiblingAfter(it)
	if err != nil {
		m.err = err
		return nil
	}
	m.unsaved = true
	m.refreshRows()
	m.cursor = m.rowIndexOf(sib)
	m.caret = 0
	return nil
}

// codeInlineRender is the code node's one-line row body: a dim "code" tag (the
// block itself hangs beneath as a band), plus a line count when it is multi-line.
func codeInlineRender(it *item, name string) string {
	// count from it.name: the passed name has had its newlines stripped as control
	// bytes by renderBody, so it can't tell a one-liner from a block.
	n := strings.Count(it.name, "\n")
	if it.name == "" {
		return cDim + "code" + cReset + " " + cDim + "empty" + cReset
	}
	if n == 0 {
		return cDim + "code" + cReset
	}
	return cDim + fmt.Sprintf("code · %d lines", n+1) + cReset
}

// codeToContext ships the code as the element's multi-line body — a code node's
// name IS its source, and flattening the newlines to one <code> line would
// mangle it for the agent.
func codeToContext(it *item) contextXML {
	return contextXML{tag: "code", body: strings.TrimRight(it.name, "\n")}
}

// codeBands hangs the always-on gray block beneath a code node (skipped for the
// focused node, whose editor draws its own windowed block — see viewRenderRows).
func (m *Model) codeBands(r row, subtreeBelow bool, maxLine int) []string {
	rail := continuationPrefix(r, subtreeBelow)
	return CodeBlockBands(r.it.name, "code", -1, false, rail, maxLine, 0, 0)
}
