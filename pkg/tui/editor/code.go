package editor

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// The Code node is a multi-line code block: a fully gray background, dim line
// numbers, and a thin white vertical rule to the RIGHT of the numbers separating
// them from the code. It carries no border and no header — the block REPLACES the
// node's row (there is no separate "code" text line above it, see viewRenderRows
// / blockGroupLines). The code lives in it.name (multi-line, like the json node's
// document) so it syncs and greps as plain text. alt+e focuses the block for
// editing; Enter makes a newline INSIDE the block, and two spaces in a row at the
// end exit to a fresh sibling (the outline gesture — the trailing spaces are
// trimmed). esc leaves in place.
//
// codeBlockLines is the shared content renderer — the nlpcompute node's code face
// draws through it too, so both wear the same borderless gray block.

// cWhite is the code block's vertical rule — white is white, never themed (like
// the painter bar).
const cWhite = "\x1b[38;2;255;255;255m"

// codeBlockLines renders code as the borderless block CONTENT (no tree rail — the
// caller prefixes that): each line is the dim right-padded line number, a space,
// a white vertical rule, a space, then the syntax-highlighted code, all on the
// gray background, padded to inner columns. caret is the rune index of the block
// cursor when focused (< 0 draws none) — its line is drawn raw so the cell reads
// cleanly.
func codeBlockLines(code string, caret, inner int) []string {
	if inner < 8 {
		inner = 8
	}
	caretLine, caretCol := -1, -1
	if caret >= 0 {
		caretLine, caretCol = jsonCaretLC(code, caret)
	}
	lines := strings.Split(code, "\n")
	numW := len(fmt.Sprintf("%d", len(lines)))
	out := make([]string, len(lines))
	for i, l := range lines {
		num := cDim + fmt.Sprintf("%*d", numW, i+1) + cReset + bgCode
		body := HLCodeLine(l)
		if i == caretLine {
			body = codeCaretLine(l, caretCol)
		}
		gutter := num + " " + cWhite + "│" + cReset + bgCode + " "
		out[i] = cReset + bgCode + padGray(gutter+body, inner)
	}
	return out
}

// CodeBlockBands renders code as the borderless gray block, windowed as a band
// (the focused nodeView path). caret is the block cursor's rune index (drawn only
// when focused); rail is the tree hanging indent, width the render width. When
// winH > 0 the block is windowed to [scroll, scroll+winH) with the caret line kept
// in view; winH <= 0 returns the whole block.
func CodeBlockBands(code string, caret int, focused bool, rail string, width, scroll, winH int) []string {
	c := -1
	if focused {
		c = caret
	}
	content := codeBlockLines(code, c, width-visibleWidth(rail))
	all := make([]string, len(content))
	for i, l := range content {
		all[i] = rail + l
	}

	if winH <= 0 {
		return all
	}
	caretLine := -1
	if c >= 0 {
		caretLine, _ = jsonCaretLC(code, c)
	}
	if caretLine >= 0 { // keep the caret line in view
		if caretLine < scroll {
			scroll = caretLine
		}
		if caretLine >= scroll+winH {
			scroll = caretLine - winH + 1
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

// codeBlockCode is the Code node's blockCode hook: the node always renders AS the
// block (replacing its row). While focused the live edit buffer + caret drive it;
// otherwise the persisted body with no caret.
func codeBlockCode(m *Model, it *item, focused bool) (string, int, bool) {
	if focused {
		buf, caret := codeView{}.get(m, it)
		return buf, caret, true
	}
	return it.name, -1, true
}

// blockGroupLines wraps borderless block content into a node's group lines: the
// tree connector AND the node's bullet glyph sit on the first line (the ○ stays
// visible so a code block still reads as a node), the hanging rail on every
// continuation, so the block hangs at the node's indent. glyph is the pre-styled
// glyph cell (color + glyph + reset) built by the caller like a normal row.
func (m *Model) blockGroupLines(r row, content []string, below bool, glyph string) []string {
	first := " " + cDim + connector(r) + glyph + " "
	cont := continuationPrefix(r, below)
	out := make([]string, len(content))
	for i, c := range content {
		p := cont
		if i == 0 {
			p = first
		}
		out[i] = p + c
	}
	return out
}

// padGray pads a styled inner string (drawn under an active bgCode) with gray
// spaces out to cols visible columns, then resets; over-long input is clipped.
func padGray(inner string, cols int) string {
	if w := visibleWidth(inner); w < cols {
		return inner + strings.Repeat(" ", cols-w) + cReset
	}
	return clip(inner, cols) + cReset
}

// cCaret is the code block's thin cursor: an underline on the caret cell rather
// than a full inverted block, so it reads as a slim caret and keeps the code
// character beneath it legible.
const cCaret = "\x1b[4m"

// codeCaretLine draws one raw code line with a thin (underline) block cursor at
// caret; the caret line is not syntax-colored so the cursor cell reads cleanly.
func codeCaretLine(line string, caret int) string {
	r := []rune(line)
	if caret < 0 {
		caret = 0
	}
	var b strings.Builder
	b.WriteString(cFG)
	for i, c := range r {
		if i == caret {
			b.WriteString(cCaret + string(c) + cReset + bgCode + cFG)
		} else {
			b.WriteString(string(c))
		}
	}
	if caret >= len(r) {
		b.WriteString(cCaret + " " + cReset + bgCode + cFG)
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
		// at the first line, decline so the outline crosses to the previous row
		if line, _ := jsonCaretLC(buf, caret); line == 0 {
			return nil, false
		}
		caret = jsonCaretLineMove(buf, caret, -1)
	case "down":
		// at the last line, decline so the outline crosses to the next row
		if line, _ := jsonCaretLC(buf, caret); line == strings.Count(buf, "\n") {
			return nil, false
		}
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
	return CodeBlockBands(buf, caret, focused, rail, width, scroll, winH)
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
