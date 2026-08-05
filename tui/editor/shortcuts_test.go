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
	if m.mode != modeShortcuts {
		t.Fatalf("/shortcuts mode=%v", m.mode)
	}
	page := stripSGR(m.View())
	if !strings.HasPrefix(page, "Move and select\n") || !strings.Contains(page, "shift+↑") {
		t.Fatalf("shortcut page should start directly with reference rows: %q", page)
	}
	if strings.Contains(page, "Keyboard shortcuts") || strings.Contains(page, "All editor chords") {
		t.Fatalf("shortcut page kept the redundant heading: %q", page)
	}
	start := m.focusScroll
	m.press("pgdown")
	if m.focusScroll <= start {
		t.Fatal("pgdown did not scroll the shortcut page")
	}
	m.press("esc")
	if m.mode != modeOutline {
		t.Fatalf("esc mode=%v", m.mode)
	}
}
