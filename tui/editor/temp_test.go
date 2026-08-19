package editor

import (
	"strings"
	"testing"

	"github.com/lflow/lflow/tui/database"
)

// seedTemp adds n bullet children to the scratch tree's root and returns it. It
// first drops the seeded empty child ensureTempTree plants so the root holds
// exactly the n named nodes the test asks for.
func seedTemp(m *Model, n int) *Model {
	m.ensureTempTree()
	for _, c := range m.tempTree.root.children {
		delete(m.tempTree.byUUID, c.uuid)
	}
	m.tempTree.root.children = nil
	for i := 0; i < n; i++ {
		it := &item{name: "t" + string(rune('a'+i)), typ: database.TypeBullets, parent: m.tempTree.root}
		it.uuid = it.name // enough for the read-only render paths
		m.tempTree.root.children = append(m.tempTree.root.children, it)
		m.tempTree.byUUID[it.uuid] = it
	}
	return m
}

// TestEmptyTempPanelShowsNode: the scratch tree is seeded with one empty node on
// first use so the persistent panel has an editable row, and a tree with no
// visible rows at all renders a literal blank line.
func TestEmptyTempPanelShowsNode(t *testing.T) {
	m := newTestModel(120, "main")
	m.ensureTempTree()
	if got := len(m.tempTree.visibleRows(m.tempTree.root, false, nil)); got != 1 {
		t.Fatalf("seeded temp tree visible rows = %d, want 1", got)
	}
	lines := m.readonlyRegionLines(m.tempTree, m.tempTree.root, 0, maxTempPanelLines, 119, true, 0)
	if len(lines) != 1 || strings.TrimSpace(lines[0]) == "" {
		t.Fatalf("empty temp panel should show the one seeded node line, got %q", lines)
	}

	// a tree with no rows at all keeps its footprint with no placeholder text
	m.tempTree.root.children = nil
	lines = m.readonlyRegionLines(m.tempTree, m.tempTree.root, 0, maxTempPanelLines, 119, true, 0)
	if lines[0] != "" {
		t.Fatalf("blank temp tree should render a literal blank line, got %q", lines[0])
	}
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

// TestReadonlyMainKeepsWholeWrappedCursorRow: after Down moves focus into the
// Temporary Domain, the main outline becomes this read-only region. Its window
// must follow the end of the stashed row, not just its first visual line, so a
// wrapped last node does not appear to collapse while temp has focus.
func TestReadonlyMainKeepsWholeWrappedCursorRow(t *testing.T) {
	m := seedTemp(newTestModel(30, "main"), 8)
	last := m.tempTree.root.children[7]
	last.name = "wrapped cursor row keeps its distinctive final words visible"

	lines := m.readonlyRegionLines(m.tempTree, m.tempTree.root, 7, 5, 29, false, -1)
	plain := stripSGR(strings.Join(lines, "\n"))
	flowed := strings.Join(strings.Fields(plain), " ")
	if !strings.Contains(flowed, "final words visible") {
		t.Fatalf("read-only window clipped the wrapped cursor row to its first line:\n%s", plain)
	}
}

// TestReadonlyMainKeepsTheCursorRowsBands: a node whose real body hangs BENEATH
// its row — an image thumbnail, a bar plot, a note — must stay visible when
// focus steps into the Temporary Domain. The window used to follow the row's
// own lines only, so the panel cut the frame off right under the node and the
// picture it stands for was never on screen.
func TestReadonlyMainKeepsTheCursorRowsBands(t *testing.T) {
	m := seedTemp(newTestModel(60, "main"), 8)
	last := m.tempTree.root.children[7]
	last.name = "chart"
	last.note = "band one\nband two\nband three"

	lines := m.readonlyRegionLines(m.tempTree, m.tempTree.root, 7, 5, 59, false, -1)
	plain := stripSGR(strings.Join(lines, "\n"))
	if !strings.Contains(plain, "chart") {
		t.Fatalf("the cursor row itself must stay in view:\n%s", plain)
	}
	if !strings.Contains(plain, "band three") {
		t.Fatalf("the cursor row's bands must stay in view:\n%s", plain)
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
	if m.scrollTempPanel(-wheelStep, 19) { // bottom edge is exclusive
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

// TestTempKeepsOneNodeAfterMoves: moving the last node out of the Temporary
// Domain — alt+shift+up (crossToNotes) or the finder's /move:here
// (bringFromTemp) — must reseed the empty placeholder, exactly like deleting the
// last node does, so the panel always keeps an editable row.
func TestTempKeepsOneNodeAfterMoves(t *testing.T) {
	// crossToNotes: the last temp node crosses into the notes, the domain reseeds
	m := newTestModel(80, "main")
	m.ensureTempTree()
	m.enterTemp()
	last := m.tempTree.root.children[0]
	m.crossToNotes(last)
	if m.tempActive {
		t.Fatal("crossToNotes must return to the main outline")
	}
	if got := len(m.tempTree.root.children); got != 1 {
		t.Fatalf("temp children after moving the last node out = %d, want the reseeded 1", got)
	}
	if m.tempTree.root.children[0].name != "" {
		t.Fatalf("reseeded temp node must be empty, got %q", m.tempTree.root.children[0].name)
	}
	if len(m.mainStash.tree.root.children) != 2 || last.parent != m.mainStash.tree.root {
		t.Fatalf("the moved node must land in the main notes, got %d children", len(m.mainStash.tree.root.children))
	}

	// bringFromTemp: the finder's /move:here pulls the last temp node into the
	// notes — same guarantee
	m = newTestModel(80, "main")
	m.ensureTempTree()
	src := m.tempTree.root.children[0]
	cur := m.tree.root.children[0]
	m.bringFromTemp(src, cur)
	if got := len(m.tempTree.root.children); got != 1 {
		t.Fatalf("temp children after bringing the last node out = %d, want the reseeded 1", got)
	}
	if m.tempTree.root.children[0].name != "" {
		t.Fatalf("reseeded temp node must be empty, got %q", m.tempTree.root.children[0].name)
	}
	if len(m.tree.root.children) != 2 || src.parent != m.tree.root {
		t.Fatalf("the brought node must land in the main tree, got %d children", len(m.tree.root.children))
	}
}

// TestTempPanelSurvivesPickers: the always-visible Temporary Domain panel must
// stay on screen while a Group-A picker (/type, slash menu, /style) is open —
// the picker window carves its rows out of the main outline, not out of the
// temp panel. It used to vanish entirely: picker modes failed the showTemp
// mode check, so opening /type blanked the panel (and the frame even lost its
// reservation for it).
func TestTempPanelSurvivesPickers(t *testing.T) {
	m := seedTemp(newTestModel(80, "main"), 1)
	m.press("alt+t") // modeType, type picker open
	if m.mode != modeType {
		t.Fatalf("precondition: alt+t should open the type picker, mode=%v", m.mode)
	}
	frame := stripSGR(strings.Join(strings.Split(m.View(), "\n"), "\n"))
	if !strings.Contains(frame, "ta") {
		t.Fatalf("temp node vanished while the type picker is open:\n%s", frame)
	}

	// the slash menu (alt+P) behaves the same
	m = seedTemp(newTestModel(80, "main"), 1)
	m.press("alt+P")
	if m.mode != modeSlash {
		t.Fatalf("precondition: alt+P should open the slash menu, mode=%v", m.mode)
	}
	frame = stripSGR(strings.Join(strings.Split(m.View(), "\n"), "\n"))
	if !strings.Contains(frame, "ta") {
		t.Fatalf("temp node vanished while the slash menu is open:\n%s", frame)
	}
}

// TestTempSpaceRendersBlockNodesAsBlocks: the Temporary Domain is a SPACE, not a
// second renderer. A Code node parked there must draw the same gray, gutter-
// numbered block it draws in the outline — it used to collapse to the word
// "code", which reads as the node having been damaged by the move.
func TestTempSpaceRendersBlockNodesAsBlocks(t *testing.T) {
	m := newTestModel(80, "main")
	m.ensureTempTree()
	for _, c := range m.tempTree.root.children {
		delete(m.tempTree.byUUID, c.uuid)
	}
	code := &item{name: "print('hi')\nprint('there')", typ: database.TypeCode, parent: m.tempTree.root}
	code.uuid = "code1"
	m.tempTree.root.children = []*item{code}
	m.tempTree.byUUID[code.uuid] = code

	lines := m.readonlyRegionLines(m.tempTree, m.tempTree.root, 0, 8, 80, true, 0)
	joined := stripSGR(strings.Join(lines, "\n"))

	if strings.Contains(joined, "code · 2 lines") || strings.Contains(joined, "○ code") {
		t.Errorf("the block collapsed to its compact label:\n%s", joined)
	}
	for _, want := range []string{"1 │ print('hi')", "2 │ print('there')"} {
		if !strings.Contains(joined, want) {
			t.Errorf("temp region is missing %q:\n%s", want, joined)
		}
	}
}
