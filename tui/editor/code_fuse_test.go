package editor

import (
	"testing"
)

// TestCodePasteKeepsTheWholeSnippet: pasting into a code block lands the text
// verbatim in the block — every line, its indentation intact — instead of
// fanning out into a row per line or smuggling \r bytes into the buffer.
func TestCodePasteKeepsTheWholeSnippet(t *testing.T) {
	m, code := codeAutoModel()
	m.step("home") // rest on the block: it auto-focuses

	// tmux ONLCR line breaks, a tab indent, and a trailing newline — the shapes a
	// real terminal paste arrives in
	m.handleKey(pasteMsg("def f():\r\r\n\treturn 1\r\n"))

	buf, _ := (codeView{}).get(m, code)
	if buf != "def f():\n    return 1" {
		t.Fatalf("pasted block = %q", buf)
	}
	if len(m.tree.root.children) != 3 {
		t.Fatalf("a paste into a code block must not fan out into rows, %d nodes",
			len(m.tree.root.children))
	}
}

// TestCodePasteMidBlockKeepsItsBreak: pasted text landing before existing text
// keeps its trailing newline — only a paste at the very end drops it (that one
// would be a blank numbered line and nothing else).
func TestCodePasteMidBlockKeepsItsBreak(t *testing.T) {
	m, code := codeAutoModel()
	code.name = "tail"
	m.step("home") // enter the block, caret at the top

	m.handleKey(pasteMsg("head\n"))

	if buf, _ := (codeView{}).get(m, code); buf != "head\ntail" {
		t.Fatalf("mid-block paste = %q, want head\\ntail", buf)
	}
}

// TestPasteOutsideABlockStillFansOut: the per-type hook is opt-in — an ordinary
// bullet keeps the outline's row-per-line paste.
func TestPasteOutsideABlockStillFansOut(t *testing.T) {
	m := newTestModel(80, "")
	m.cursor, m.caret = 0, 0

	m.handleKey(pasteMsg("one\ntwo"))

	if got := len(m.tree.root.children); got != 2 {
		t.Fatalf("a bullet's multi-line paste must fan out into rows, got %d", got)
	}
}

// TestPasteBlockTextNormalizes covers the shapes a terminal delivers: every
// break form folds to \n (blank lines survive), tabs expand, bracketed-paste
// markers and stray control bytes are dropped.
func TestPasteBlockTextNormalizes(t *testing.T) {
	got := pasteBlockText("\x1b[200~a\r\r\nb\r\n\r\nc\rd\te\x07\x1b[201~")
	want := "a\nb\n\nc\nd" + pasteTab + "e"
	if got != want {
		t.Fatalf("pasteBlockText = %q, want %q", got, want)
	}
}

// TestCodeBlockEnteredOnTheEdgeWalkedIntoFrom: walking DOWN onto a block parks
// the caret on its FIRST line and the next Down keeps travelling inside it;
// walking UP onto it lands on the last line. Landing on the far edge is what
// made Down skip a whole block in one press.
func TestCodeBlockEnteredOnTheEdgeWalkedIntoFrom(t *testing.T) {
	m, code := codeAutoModel()
	code.name = "a\nb\nc"
	m.cursor = m.rowIndexOf(m.tree.byUUID["top"])

	m.step("down")
	if m.cursorItem() != code || !m.focused {
		t.Fatalf("Down must land in the block, cur=%v focused=%v", m.cursorItem(), m.focused)
	}
	if _, caret := (codeView{}).get(m, code); caret != 0 {
		t.Fatalf("arriving from above must park the caret on the first line, caret=%d", caret)
	}
	m.step("down")
	if m.cursorItem() != code {
		t.Fatal("the second Down must walk the block's own lines, not leave it")
	}
	if _, caret := (codeView{}).get(m, code); caret != 2 {
		t.Fatalf("caret = %d, want the start of line 2", caret)
	}

	// and from below: the last line
	m2, code2 := codeAutoModel()
	code2.name = "a\nb\nc"
	m2.cursor = m2.rowIndexOf(m2.tree.byUUID["bot"])
	m2.step("up")
	if m2.cursorItem() != code2 {
		t.Fatalf("Up must land in the block, cur=%v", m2.cursorItem())
	}
	if _, caret := (codeView{}).get(m2, code2); caret != len([]rune(code2.name)) {
		t.Fatalf("arriving from below must park the caret on the last line, caret=%d", caret)
	}
}
