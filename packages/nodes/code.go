package nodes

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lflow/lflow/packages/database"
	"github.com/lflow/lflow/packages/utils"
)

// The Code node is a multi-line code block: the block REPLACES the node's row
// (no separate "code" text line above it) — that borderless-gray-background
// RENDERING machinery (line numbers, syntax tint, the vertical rule) stays in
// the editor (CodeBlockBands, rebound onto the nodes.CodeBlockBands bridge var
// — see plugin.go) since it also serves the block-face windowing the editor's
// row/band layout owns. This file is the node's own half: the compact inline
// fallback shown on surfaces that don't run the block layout, the always-block
// BlockCode face, and the alt+e buffer editor built over NodeStore.
//
// AutoFocus makes resting the cursor on the node auto-enter this editor (thin
// caret, type directly, no alt+e); Enter makes a newline INSIDE the block; esc
// leaves in place.
func init() {
	Register(Plugin{
		Key:            database.TypeCode,
		Label:          "Code",
		InlineEditable: false,
		AutoFocus:      true,
		RenderPlain:    func(n Ref, _ string) string { return codeInlineRender(n) },
		View:           codeView{},
		BlockCode:      codeBlockCode,
	})
}

// codeInlineRender is the code node's one-line row body for surfaces that
// don't draw the block face: a dim "code" tag, plus a line count when the
// body is multi-line.
func codeInlineRender(n Ref) string {
	th := Theme()
	text := n.Text()
	if text == "" {
		return th.Dim + "code" + th.Reset + " " + th.Dim + "empty" + th.Reset
	}
	lines := strings.Count(text, "\n")
	if lines == 0 {
		return th.Dim + "code" + th.Reset
	}
	return th.Dim + fmt.Sprintf("code · %d lines", lines+1) + th.Reset
}

// codeBlockCode is the Code node's BlockCode hook: the node always renders AS
// the block (replacing its row). While focused the live edit buffer + caret
// drive it; otherwise the persisted body with no caret.
func codeBlockCode(h Host, n Ref, focused bool) (string, int, bool) {
	if focused {
		buf, caret := codeViewGet(h, n)
		return buf, caret, true
	}
	return n.Text(), -1, true
}

// ── the Code node's inline editor (a PluginView) ────────────────────────────

// codeView edits the code node's multi-line body. The live buffer is kept in
// the ephemeral per-node store (NodeStore) and flushed on Leave, like jsonView.
type codeView struct{}

func codeViewGet(h Host, n Ref) (string, int) {
	d := h.NodeStore(n.UUID())
	buf, _ := d["codeBuf"].(string)
	caret, _ := d["codeCaret"].(int)
	return buf, caret
}

func codeViewSet(h Host, n Ref, buf string, caret int) {
	d := h.NodeStore(n.UUID())
	d["codeBuf"] = buf
	d["codeCaret"] = caret
}

// Enter seeds the buffer from the node and parks the caret at the end.
func (codeView) Enter(h Host, n Ref) bool {
	text := n.Text()
	codeViewSet(h, n, text, len([]rune(text)))
	return true
}

// Leave flushes the buffer back to the node and clears the edit state.
func (codeView) Leave(h Host, n Ref) {
	buf, _ := codeViewGet(h, n)
	if buf != n.Text() {
		n.SetText(buf)
	}
	d := h.NodeStore(n.UUID())
	delete(d, "codeBuf")
	delete(d, "codeCaret")
}

// Lines is the header + one per buffer line + the footer rule.
func (codeView) Lines(h Host, n Ref, width int) int {
	buf, _ := codeViewGet(h, n)
	return 2 + len(strings.Split(buf, "\n"))
}

// Key edits the buffer. Enter is a newline inside the block; esc/ctrl+c fall
// through to central handling.
//
// LEFT BEHIND: the editor original also treats two trailing spaces at the
// buffer's end as an "exit to a fresh sibling" gesture — it flushes the
// buffer, drops focus, and INSERTS A NEW SIBLING NODE after this one
// (m.exitCodeToSibling, via m.tree.insertSiblingAfter). Ref/Host have no
// "create a node" operation — that is tree-mutation, not a per-node render or
// edit concern — so that gesture cannot be expressed over the plugin contract
// as it stands. Here two trailing spaces are just typed literally; the
// sibling-exit gesture stays exclusive to the editor's own codeView until (if
// ever) Host grows a node-creation hook.
func (codeView) Key(h Host, n Ref, k tea.KeyMsg) (tea.Cmd, bool) {
	buf, caret := codeViewGet(h, n)
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
		if line, _ := utils.CaretLineCol(buf, caret); line == 0 {
			return nil, false
		}
		caret = utils.CaretVMove(buf, caret, -1)
	case "down":
		// at the last line, decline so the outline crosses to the next row
		if line, _ := utils.CaretLineCol(buf, caret); line == strings.Count(buf, "\n") {
			return nil, false
		}
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
				codeViewSet(h, n, string(rl[:caret-1]), caret-1)
				return ExitCodeToSibling(h, n), true
			}
			buf, caret = jsonIns(buf, caret, " ")
		case k.Type == tea.KeyRunes && !k.Alt:
			buf, caret = jsonIns(buf, caret, string(k.Runes))
		default:
			return nil, false // esc, ctrl+c … → central
		}
	}
	codeViewSet(h, n, buf, caret)
	return nil, true
}

// Bands renders the focused editor through the shared borderless block — the
// nodes.CodeBlockBands bridge var, rebound by the editor at init onto its own
// CodeBlockBands (block RENDERING stays in the editor; see the file doc).
func (codeView) Bands(h Host, n Ref, rail string, width, scroll, winH int, focused bool) []string {
	buf, caret := codeViewGet(h, n)
	if !focused {
		caret = -1
	}
	return CodeBlockBands(buf, caret, focused, rail, width, scroll, winH)
}
