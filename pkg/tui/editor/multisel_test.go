package editor

import (
	"testing"

	"github.com/lflow/lflow/pkg/tui/database"
)

// selModel builds root → [a, b(с b1), c, d] for range-op tests.
func selModel() (*Model, *item, *item, *item, *item) {
	root := &item{uuid: "root"}
	a := &item{uuid: "a", name: "a", parent: root}
	b := &item{uuid: "b", name: "b", parent: root}
	b1 := &item{uuid: "b1", name: "b1", parent: b}
	b.children = []*item{b1}
	c := &item{uuid: "c", name: "c", parent: root}
	d := &item{uuid: "d", name: "d", parent: root}
	root.children = []*item{a, b, c, d}
	tr := &tree{root: root,
		byUUID:        map[string]*item{"root": root, "a": a, "b": b, "b1": b1, "c": c, "d": d},
		externalNames: map[string]string{}, snapshots: map[string]snapshot{}}
	m := &Model{tree: tr, viewStack: []*item{root}, width: 80, height: 24}
	m.refreshRows()
	return m, a, b, c, d
}

func names(items []*item) []string {
	var out []string
	for _, it := range items {
		out = append(out, it.name)
	}
	return out
}

func TestSelectionGrowsAndClears(t *testing.T) {
	m, _, _, _, _ := selModel()
	// rows: a(0) b(1) b1(2) c(3) d(4)
	m.cursor = 1
	m.press("shift+down") // select b..b1
	m.press("shift+down") // select b..c
	if !m.selOn {
		t.Fatal("selection must be active")
	}
	lo, hi := m.selectionBounds()
	if lo != 1 || hi != 3 {
		t.Fatalf("bounds = %d..%d, want 1..3", lo, hi)
	}
	// roots: b and c (b1 rides inside b)
	roots := names(m.selectionRoots())
	if len(roots) != 2 || roots[0] != "b" || roots[1] != "c" {
		t.Fatalf("roots = %v, want [b c]", roots)
	}
	// esc clears
	m.press("esc")
	if m.selOn {
		t.Fatal("esc must clear the selection")
	}
	// plain movement clears too
	m.cursor = 1
	m.press("shift+down")
	m.press("down")
	if m.selOn {
		t.Fatal("plain down must clear the selection")
	}
}

func TestSelectionIndentOutdent(t *testing.T) {
	m, a, b, c, _ := selModel()
	m.cursor = 1 // on b
	m.press("shift+down")
	m.press("shift+down") // b..c selected

	m.press("tab") // group-indent under a
	if b.parent != a || c.parent != a {
		t.Fatalf("b,c must indent under a (got %s, %s)", b.parent.uuid, c.parent.uuid)
	}
	if names(a.children)[0] != "b" || names(a.children)[1] != "c" {
		t.Fatalf("indented order = %v, want [b c]", names(a.children))
	}
	if !m.selOn {
		t.Fatal("selection must survive the indent")
	}

	m.press("shift+tab") // and back out
	if b.parent != m.tree.root || c.parent != m.tree.root {
		t.Fatal("b,c must outdent back to root")
	}
	got := names(m.tree.root.children)
	want := []string{"a", "b", "c", "d"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("root order = %v, want %v", got, want)
		}
	}
}

func TestSelectionMoveBlock(t *testing.T) {
	m, _, b, c, _ := selModel()
	m.cursor = 1
	m.press("shift+down")
	m.press("shift+down") // b..c

	m.press("alt+shift+down") // block moves below d
	got := names(m.tree.root.children)
	want := []string{"a", "d", "b", "c"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("after move down: %v, want %v", got, want)
		}
	}
	m.press("alt+shift+up") // and back
	got = names(m.tree.root.children)
	want = []string{"a", "b", "c", "d"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("after move up: %v, want %v", got, want)
		}
	}
	_ = b
	_ = c
}

func TestSelectionTypeAndDelete(t *testing.T) {
	m, a, b, c, _ := selModel()
	m.cursor = 0
	m.press("shift+down") // a..b

	// /type applies to every selected node
	typeSource{}.onSelect(m, pickerItem{value: database.TypeTodo})
	if a.typ != database.TypeTodo || b.typ != database.TypeTodo {
		t.Fatalf("selection retype failed: %q, %q", a.typ, b.typ)
	}
	if c.typ == database.TypeTodo {
		t.Fatal("unselected node must not retype")
	}

	// delete: b has a child → confirm first, then both roots go
	m.cursor = 0
	m.press("shift+down")
	m.press("ctrl+d")
	if m.mode != modeConfirm {
		t.Fatal("selection with a subtree must confirm before deleting")
	}
	m.handleConfirmKey(tkey("y"))
	got := names(m.tree.root.children)
	if len(got) != 2 || got[0] != "c" || got[1] != "d" {
		t.Fatalf("after delete: %v, want [c d]", got)
	}
	if m.selOn {
		t.Fatal("delete must clear the selection")
	}
}
