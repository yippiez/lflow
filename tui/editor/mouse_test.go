package editor

import (
	"reflect"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lflow/lflow/tui/database"
)

// press is a left-button click at a screen cell; motion is a bare pointer move
// over one (no button held), which is what the breadcrumb hover reads.
func press(x, y int) tea.MouseMsg {
	return tea.MouseMsg{X: x, Y: y, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft}
}

func motion(x, y int) tea.MouseMsg {
	return tea.MouseMsg{X: x, Y: y, Action: tea.MouseActionMotion, Button: tea.MouseButtonNone}
}

// zoneFor finds the one recorded zone of a kind matching pred — the frame must
// have been painted first, since the zones ARE the paint's record.
func zoneFor(t *testing.T, m *Model, kind hitKind, pred func(hitZone) bool) hitZone {
	t.Helper()
	for _, z := range m.mouse.zones {
		if z.kind == kind && (pred == nil || pred(z)) {
			return z
		}
	}
	t.Fatalf("no hit zone of kind %d recorded in %d zones", kind, len(m.mouse.zones))
	return hitZone{}
}

// TestClickGlyphZoomsIntoNode: the outline circle is the node's handle — a click
// on it is alt+right, and the view root becomes that node.
func TestClickGlyphZoomsIntoNode(t *testing.T) {
	m := newTestModelWithChildren(60, "parent", "kid a", "kid b")
	m.View()

	glyph := zoneFor(t, m, hitGlyph, func(z hitZone) bool { return z.idx == 0 })
	if got := glyphColumn(m.rows[0]); glyph.x0 != got {
		t.Fatalf("glyph zone at column %d, renderRow paints it at %d", glyph.x0, got)
	}
	m.handleMouse(press(glyph.x0, glyph.row))

	if m.viewRoot().name != "parent" {
		t.Fatalf("click on the glyph did not zoom: view root is %q", m.viewRoot().name)
	}
	if len(m.rows) != 2 || m.rows[0].it.name != "kid a" {
		t.Fatalf("zoomed view should list the children, got %d rows", len(m.rows))
	}
}

// TestClickRowMovesTheCursorWithoutZooming: off the circle, a click is just a
// cursor placement — the zoom is the glyph's gesture alone.
func TestClickRowMovesTheCursorWithoutZooming(t *testing.T) {
	m := newTestModel(60, "alpha", "beta", "gamma")
	m.View()

	row := zoneFor(t, m, hitRow, func(z hitZone) bool { return z.idx == 2 })
	m.handleMouse(press(row.x1-1, row.row))

	if m.cursor != 2 {
		t.Fatalf("cursor = %d, want the clicked row 2", m.cursor)
	}
	if len(m.viewStack) != 1 {
		t.Fatalf("a row click must not zoom: view stack depth %d", len(m.viewStack))
	}
	if want := len([]rune("gamma")); m.caret != want {
		t.Fatalf("caret = %d, want the end of the clicked row's text (%d)", m.caret, want)
	}
}

// TestClickTagOpensGotoSearchingIt: a tag is a question, so clicking one asks
// it — /goto opens with the tag already in the query, and the results are the
// STRICT tag matches (never the tag that merely starts the same way).
func TestClickTagOpensGotoSearchingIt(t *testing.T) {
	db := database.InitTestMemoryDB(t)
	insert := func(n database.Node) {
		t.Helper()
		if err := n.Insert(db); err != nil {
			t.Fatal(err)
		}
	}
	insert(database.Node{UUID: "root", Name: "root", Type: database.TypeBullets, AddedOn: 1, EditedOn: 1})
	insert(database.Node{UUID: "here", ParentUUID: "root", Name: "ship the mouse #demo", Type: database.TypeBullets, AddedOn: 2, EditedOn: 2})
	insert(database.Node{UUID: "sib", ParentUUID: "root", Name: "record the take #demo", Type: database.TypeBullets, AddedOn: 3, EditedOn: 3})
	insert(database.Node{UUID: "near", ParentUUID: "root", Name: "a longer word #demolition", Type: database.TypeBullets, AddedOn: 4, EditedOn: 4})

	here := &item{uuid: "here", name: "ship the mouse #demo"}
	root := &item{uuid: "root", name: "root", children: []*item{here}}
	here.parent = root
	tr := &tree{
		db: db, root: root,
		byUUID:        map[string]*item{"root": root, "here": here},
		externalNames: map[string]string{}, snapshots: map[string]snapshot{},
	}
	m := &Model{db: db, tree: tr, viewStack: []*item{root}, width: 100, height: 30, chips: map[string]database.Chip{}}
	m.refreshRows()
	m.View()

	tag := zoneFor(t, m, hitTag, nil)
	if tag.tag != "demo" {
		t.Fatalf("tag zone carries %q, want the tag word without its '#'", tag.tag)
	}
	m.handleMouse(press(tag.x0, tag.row))

	if m.mode != modeFinder || m.finder.act != actGoto {
		t.Fatalf("clicking a tag should open /goto, mode=%v act=%v", m.mode, m.finder.act)
	}
	if m.finder.query != "#demo" {
		t.Fatalf("finder query = %q, want %q", m.finder.query, "#demo")
	}
	ids := map[string]bool{}
	for _, r := range m.finder.hits {
		ids[r.node.UUID] = true
	}
	if !ids["sib"] {
		t.Fatalf("the other #demo node should be a hit, got %v", ids)
	}
	if ids["near"] {
		t.Fatalf("#demo must not match #demolition — a tag search is strict: %v", ids)
	}
}

// TestClickBreadcrumbWalksBackToThatNode: the toolbar path is navigation, not
// decoration — clicking a segment pops the zoom stack back to it and leaves the
// cursor on the node we came from.
func TestClickBreadcrumbWalksBackToThatNode(t *testing.T) {
	m := newTestModelWithChildren(100, "parent", "kid")
	kid := m.tree.root.children[0].children[0]
	kid.children = append(kid.children, &item{name: "grandkid", parent: kid})

	m.zoomInto(m.tree.root.children[0]) // → parent
	m.zoomInto(kid)                     // → parent › kid
	m.View()

	crumbs := m.crumbTargets()
	if len(crumbs) != 3 { // root, parent, kid
		t.Fatalf("want 3 crumbs (root › parent › kid), got %d", len(crumbs))
	}
	parent := zoneFor(t, m, hitCrumb, func(z hitZone) bool { return z.idx == 1 })
	m.handleMouse(press(parent.x0, parent.row))

	if m.viewRoot().name != "parent" {
		t.Fatalf("clicking the 'parent' crumb should zoom out to it, view root is %q", m.viewRoot().name)
	}
	if m.cursor != m.rowIndexOf(kid) {
		t.Fatalf("cursor should land on the node we left (kid), got row %d", m.cursor)
	}

	// the LAST crumb is where we already are: clicking it changes nothing
	m.View()
	last := zoneFor(t, m, hitCrumb, func(z hitZone) bool { return z.idx == len(m.crumbTargets())-1 })
	m.handleMouse(press(last.x0, last.row))
	if m.viewRoot().name != "parent" {
		t.Fatalf("clicking the current crumb should be a no-op, view root is %q", m.viewRoot().name)
	}
}

// TestHoverLightsOneBreadcrumbSegment: a crumb has to read as clickable before
// it is clicked, so the segment under the pointer lifts out of the bar's dim
// gray into the ordinary text gray — and only that one does. The lift is the
// WHOLE cue: no underline, no fill.
func TestHoverLightsOneBreadcrumbSegment(t *testing.T) {
	m := newTestModelWithChildren(100, "parent", "kid")
	m.zoomInto(m.tree.root.children[0])
	m.View()

	crumb := zoneFor(t, m, hitCrumb, func(z hitZone) bool { return z.idx == 1 })
	m.handleMouse(motion(crumb.x0, crumb.row))
	if m.mouse.hover != 2 { // one-based: crumb index 1
		t.Fatalf("hover = %d, want the second crumb (2)", m.mouse.hover)
	}
	bar := strings.Join(m.bottomBar(99), "\n")
	if !strings.Contains(bar, cFG+"parent") {
		t.Fatalf("hovered crumb is not lifted to the lighter gray:\n%q", bar)
	}
	if strings.Contains(bar, cFG+"untitled") {
		t.Fatalf("only the hovered crumb may light up:\n%q", bar)
	}
	if strings.Contains(bar, cUnderline) {
		t.Fatalf("the hover is a lighter gray, not an underline:\n%q", bar)
	}

	// off the bar, the highlight goes away
	m.View()
	m.handleMouse(motion(0, 0))
	if m.mouse.hover != 0 {
		t.Fatalf("hover = %d, want none (0) once the pointer leaves the crumbs", m.mouse.hover)
	}
	if strings.Contains(strings.Join(m.bottomBar(99), "\n"), cFG+"parent") {
		t.Fatal("no crumb should be lit with the pointer off the bar")
	}
}

// TestMouseModesGateTheClickZones: only the "click" preference makes the page
// answer clicks. "wheel" still scrolls — capture without clickable cells — and
// "select" leaves the mouse to the terminal entirely.
func TestMouseModesGateTheClickZones(t *testing.T) {
	for _, mode := range []string{mouseSelect, mouseWheel} {
		m := newTestModelWithChildren(60, "parent", "kid")
		m.settings = map[string]string{"mouse": mode}
		m.View()
		if len(m.mouse.zones) != 0 {
			t.Fatalf("mouse=%s recorded %d click zones, want none", mode, len(m.mouse.zones))
		}
		// a wheel notch still scrolls in wheel mode, and is ignored in select mode
		m.handleMouse(tea.MouseMsg{X: 0, Y: 1, Action: tea.MouseActionPress, Button: tea.MouseButtonWheelDown})
		if want := mode == mouseWheel; m.scrolling != want {
			t.Fatalf("mouse=%s: scrolling = %v, want %v", mode, m.scrolling, want)
		}
	}
}

// TestMouseCaptureFollowsThePreference: the hover needs motion reported with NO
// button held, which only all-motion tracking sends — so the click mode must ask
// for it, and the wheel mode must not pay for it.
func TestMouseCaptureFollowsThePreference(t *testing.T) {
	// bubbletea's screen messages are unexported, so each mode is compared
	// against the command it is supposed to be
	cases := []struct {
		mode string
		want tea.Cmd
	}{
		{mouseClick, tea.EnableMouseAllMotion},
		{mouseWheel, tea.EnableMouseCellMotion},
		{mouseSelect, tea.DisableMouse},
	}
	for _, tc := range cases {
		got := mouseCapture(tc.mode)
		if got == nil {
			t.Fatalf("mouse=%s issued no capture command", tc.mode)
		}
		if a, b := reflect.TypeOf(got()), reflect.TypeOf(tc.want()); a != b {
			t.Fatalf("mouse=%s asked for %v, want %v", tc.mode, a, b)
		}
	}
}

// TestVisibleCellsMapsColumnsPastStyling: the column map has to see through SGR
// runs and count a wide rune as the two cells it occupies — that arithmetic is
// what puts a click on the right glyph.
func TestVisibleCellsMapsColumnsPastStyling(t *testing.T) {
	runes, cols := visibleCells(" " + cDim + "├─ " + cRed + "○" + cReset + " 漢a")
	if got := string(runes); got != " ├─ ○ 漢a" {
		t.Fatalf("visible text = %q", got)
	}
	want := []int{0, 1, 2, 3, 4, 5, 6, 8}
	for i := range want {
		if cols[i] != want[i] {
			t.Fatalf("column of rune %d (%q) = %d, want %d", i, runes[i], cols[i], want[i])
		}
	}
}
