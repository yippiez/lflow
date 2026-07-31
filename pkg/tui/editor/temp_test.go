package editor

import (
	"strings"
	"testing"

	"github.com/lflow/lflow/pkg/tui/database"
)

// seedTemp adds n bullet children to the scratch tree's root and returns it.
func seedTemp(m *Model, n int) *Model {
	m.ensureTempTree()
	for i := 0; i < n; i++ {
		it := &item{name: "t" + string(rune('a'+i)), typ: database.TypeBullets, parent: m.tempTree.root}
		it.uuid = it.name // enough for the read-only render paths
		m.tempTree.root.children = append(m.tempTree.root.children, it)
		m.tempTree.byUUID[it.uuid] = it
	}
	return m
}

// TestTempPanelBudgetCapped: the always-visible temp panel is limited to
// maxTempPanelLines even when the scratch outline holds far more nodes.
func TestTempPanelBudgetCapped(t *testing.T) {
	m := seedTemp(newTestModel(120, "main"), 20)
	if got := m.tempPanelBudget(24); got != maxTempPanelLines {
		t.Fatalf("tempPanelBudget(24) = %d, want cap %d", got, maxTempPanelLines)
	}
	m = seedTemp(newTestModel(120, "main"), 3)
	if got := m.tempPanelBudget(24); got != 3 {
		t.Fatalf("tempPanelBudget(24) with 3 nodes = %d, want 3", got)
	}
}

// TestReadonlyTempPanelScrolls: the read-only temp panel is a fixed-height window
// that starts at the top and can be pinned to a later offset (the wheel's
// tempScroll); a stale offset past the content clamps back to the last window.
func TestReadonlyTempPanelScrolls(t *testing.T) {
	m := seedTemp(newTestModel(120, "main"), 12)

	top := m.readonlyRegionLines(m.tempTree, m.tempTree.root, 0, maxTempPanelLines, 119, true, 0)
	if len(top) != maxTempPanelLines {
		t.Fatalf("windowed panel len = %d, want %d", len(top), maxTempPanelLines)
	}
	if !strings.Contains(top[0], "ta") || !strings.Contains(top[maxTempPanelLines-1], "te") {
		t.Fatalf("scroll 0 should show the first rows:\n%s", strings.Join(top, "\n"))
	}

	scrolled := m.readonlyRegionLines(m.tempTree, m.tempTree.root, 0, maxTempPanelLines, 119, true, 3)
	if !strings.Contains(scrolled[0], "td") {
		t.Fatalf("scroll 3 should start at row 4 (td):\n%s", strings.Join(scrolled, "\n"))
	}
	if m.tempScroll != 3 {
		t.Fatalf("m.tempScroll = %d after a pinned render, want 3", m.tempScroll)
	}

	// a scroll past the content clamps to the last window and writes it back
	over := m.readonlyRegionLines(m.tempTree, m.tempTree.root, 0, maxTempPanelLines, 119, true, 999)
	if len(over) != maxTempPanelLines {
		t.Fatalf("clamped panel len = %d, want %d", len(over), maxTempPanelLines)
	}
	if !strings.Contains(over[len(over)-1], "tl") {
		t.Fatalf("clamped scroll should end at the last row (tl):\n%s", strings.Join(over, "\n"))
	}
	if m.tempScroll != 7 {
		t.Fatalf("m.tempScroll must be clamped back to the last window start (7), got %d", m.tempScroll)
	}
}

// TestScrollTempPanelHitTest: the wheel scrolls the read-only temp panel only
// when the event lands on the panel's screen region, and never while the panel
// is focused (tempActive) — there the body window scrolls instead.
func TestScrollTempPanelHitTest(t *testing.T) {
	m := seedTemp(newTestModel(120, "main"), 12)
	m.tempTop, m.tempHeight = 20, maxTempPanelLines

	if !m.scrollTempPanel(wheelStep, 21) {
		t.Fatal("wheel over the panel region should scroll the temp panel")
	}
	if m.tempScroll != wheelStep {
		t.Fatalf("tempScroll = %d, want %d", m.tempScroll, wheelStep)
	}
	if m.scrollTempPanel(-wheelStep, 20) { // bottom edge is exclusive
		t.Fatal("wheel just above the panel must not scroll it")
	}
	if m.scrollTempPanel(wheelStep, 40) {
		t.Fatal("wheel below the panel must not scroll it")
	}

	m.enterTemp()
	if m.scrollTempPanel(wheelStep, 21) {
		t.Fatal("a focused temp panel scrolls via the body window, not tempScroll")
	}
}
