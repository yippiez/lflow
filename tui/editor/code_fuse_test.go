package editor

import (
	"testing"
)

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
