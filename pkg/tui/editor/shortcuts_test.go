package editor

import (
	"strings"
	"testing"
)

func TestShortcutsCommandOpensSeparateScrollablePage(t *testing.T) {
	m := newTestModel(60, "task")
	m.height = 12
	mm, _ := m.runSlash("/shortcuts")
	m = mm.(*Model)
	if m.mode != modeShortcuts || !m.clearOnFrame {
		t.Fatalf("/shortcuts mode=%v clear=%v", m.mode, m.clearOnFrame)
	}
	page := stripSGR(m.View())
	if !strings.Contains(page, "Keyboard shortcuts") || !strings.Contains(page, "shift+↑") {
		t.Fatalf("shortcut page missing reference rows: %q", page)
	}
	start := m.focusScroll
	m.press("pgdown")
	if m.focusScroll <= start {
		t.Fatal("pgdown did not scroll the shortcut page")
	}
	m.press("esc")
	if m.mode != modeOutline || !m.clearOnFrame {
		t.Fatalf("esc mode=%v clear=%v", m.mode, m.clearOnFrame)
	}
}
