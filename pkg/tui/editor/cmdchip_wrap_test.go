package editor

import (
	"strings"
	"testing"

	"github.com/lflow/lflow/pkg/tui/database"
)

// TestCmdChipWrapKeepsColor: a cmd chip wider than the line wraps, and every
// continuation line must re-open the chip's colors (the dark terminal tint) —
// including when the caret sits on the chip, where the cursor's reverse video
// is dropped at the break but the colors must carry.
func TestCmdChipWrapKeepsColor(t *testing.T) {
	m, _ := dbModel(t, database.Node{UUID: "edit", Name: ""})
	cursorOn(m, "edit")
	m.caret = 0
	m.press("$echo aaaa bbbb cccc dddd eeee ffff gggg hhhh iiii jjjj kkkk llll")
	m.press(" ")
	m.press(" ")
	m.width = 40

	for _, caret := range []int{0, 2} { // 0 = cursor ON the chip, 2 = past it
		m.caret = caret
		lines := m.viewOutline(m.width - 1)
		if len(lines) < 2 || !strings.Contains(lines[0], bgTerm) {
			t.Fatalf("caret=%d: chip line missing or untinted: %q", caret, lines[0])
		}
		if !strings.Contains(lines[1], bgTerm) {
			t.Errorf("caret=%d: continuation lost the chip tint: %q", caret, lines[1])
		}
	}
}
