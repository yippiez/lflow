package editor

import (
	"testing"

	"github.com/lflow/lflow/tui/database"
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

// TestSelectionBackspaceDeletes: backspace on a shift-selection deletes the
// whole selected range (same as alt+d/ctrl+d), not just the node under the
// cursor — a selection is content, not a cursor to back over. A selection that
// spans a subtree asks for confirmation first, like ctrl+d.
func TestSelectionBackspaceDeletes(t *testing.T) {
	root := &item{}
	a := &item{uuid: "a", name: "a", parent: root}
	b := &item{uuid: "b", name: "b", parent: root}
	c := &item{uuid: "c", name: "c", parent: root}
	root.children = []*item{a, b, c}
	tr := &tree{root: root, byUUID: map[string]*item{"a": a, "b": b, "c": c}, externalNames: map[string]string{}}
	m := &Model{tree: tr, viewStack: []*item{root}, width: 80, height: 24}
	m.refreshRows()
	m.cursor = 0
	m.press("shift+down") // a..b selected, no subtrees

	m.press("backspace")

	got := names(m.tree.root.children)
	if len(got) != 1 || got[0] != "c" {
		t.Fatalf("backspace on selection: %v, want [c]", got)
	}
	if m.selOn {
		t.Fatal("backspace delete must clear the selection")
	}
}

// TestBackspaceOnDividerNoop: backspace at the start of a divider (and backspace
// on the node just below one) must not merge rows — a divider is a rule, not
// content, so it never absorbs text and is never absorbed.
func TestBackspaceOnDividerNoop(t *testing.T) {
	root := &item{}
	div := &item{uuid: "div", name: "", typ: database.TypeDivider, parent: root}
	line := &item{uuid: "line", name: "Hello", typ: database.TypeLine, parent: root}
	root.children = []*item{div, line}
	tr := &tree{root: root, byUUID: map[string]*item{"div": div, "line": line}, externalNames: map[string]string{}}
	m := &Model{tree: tr, viewStack: []*item{root}, width: 80, height: 24}
	m.refreshRows()

	// backspace on the divider itself: nothing happens
	m.cursor = m.rowIndexOf(div)
	m.caret = 0
	m.press("backspace")
	if len(root.children) != 2 || root.children[0] != div {
		t.Fatalf("divider must survive backspace, children=%v", names(root.children))
	}
	if div.typ != database.TypeDivider {
		t.Fatalf("divider must stay a divider after backspace, got %q", div.typ)
	}

	// backspace at the start of the line below: nothing merges into the divider
	m.cursor = m.rowIndexOf(line)
	m.caret = 0
	m.press("backspace")
	if len(root.children) != 2 || root.children[1] != line {
		t.Fatalf("line below a divider must not merge into it, children=%v", names(root.children))
	}
	if div.name != "" || line.name != "Hello" {
		t.Fatalf("divider/line text must be untouched: div=%q line=%q", div.name, line.name)
	}
}
