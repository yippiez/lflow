package editor

import (
	"strings"
	"testing"
)

// TestTextSelectionGrowsByWordAndRune: shift+←/→ nudges one rune and
// ctrl+shift+←/→ takes whole words, matching ordinary text editors.
func TestTextSelectionGrowsByWordAndRune(t *testing.T) {
	m := newTestModel(100, "paint some words here")
	t.Cleanup(func() { textSelUUID = "" })
	n := m.rows[0].it
	n.uuid = "p1"
	m.tree.byUUID["p1"] = n

	m.caret = len("paint ")
	m.press("shift+right") // "s"
	if _, lo, hi, ok := m.textSelection(); !ok || lo != 6 || hi != 7 {
		t.Fatalf("after shift+right: [%d,%d) ok=%v, want [6,7)", lo, hi, ok)
	}
	m.press("ctrl+shift+right") // finish "some"
	if _, lo, hi, _ := m.textSelection(); lo != 6 || hi != 10 {
		t.Fatalf("ctrl+shift+right: [%d,%d), want [6,10)", lo, hi)
	}
	m.press("ctrl+shift+right") // add " words"
	if _, lo, hi, _ := m.textSelection(); lo != 6 || hi != 16 {
		t.Fatalf("second ctrl+shift+right: [%d,%d), want [6,16)", lo, hi)
	}
	m.press("shift+left") // one rune back off the edge
	if _, lo, hi, _ := m.textSelection(); lo != 6 || hi != 15 {
		t.Fatalf("shift+left: [%d,%d), want [6,15)", lo, hi)
	}

	// the bar renders over the selected runes only
	m.caret = 6
	m.clearTextSel()
	m.press("shift+right")
	body := renderBody(n, n.name, -1, false, nil)
	before, after, found := strings.Cut(body, bgTextSel())
	if !found {
		t.Fatalf("selection must render a bar, got %q", body)
	}
	if strings.Contains(stripSGR(before), "s") || !strings.HasPrefix(stripSGR(after), "s") {
		t.Fatalf("the bar must start at %q, got %q", "s", body)
	}
}

// TestTextSelectionDropsOnMovementAndEsc: the selection is transient — a plain
// arrow or esc releases it, and it never survives onto another node.
func TestTextSelectionDropsOnMovementAndEsc(t *testing.T) {
	m := newTestModel(100, "alpha beta", "second node")
	t.Cleanup(func() { textSelUUID = "" })

	m.caret = 0
	m.press("shift+right")
	if !m.textSelOn {
		t.Fatal("shift+right must start a selection")
	}
	m.press("right")
	if m.textSelOn || textSelUUID != "" {
		t.Fatal("a plain arrow must drop the selection")
	}

	m.caret = 0
	m.press("shift+right")
	m.press("esc")
	if m.textSelOn {
		t.Fatal("esc must drop the selection")
	}

	// shift+down hands over to the row selection: never both at once
	m.caret = 0
	m.press("shift+right")
	m.press("shift+down")
	if m.textSelOn || !m.selOn {
		t.Fatalf("shift+down must swap to a row selection (text=%v rows=%v)", m.textSelOn, m.selOn)
	}
}
