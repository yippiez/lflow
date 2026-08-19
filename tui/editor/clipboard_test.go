package editor

import (
	"bytes"
	"encoding/base64"
	"reflect"
	"strings"
	"testing"

	"github.com/lflow/lflow/tui/database"
)

// clipCapture points the OSC 52 escape at a buffer and takes the host tools out
// of the probe, so a test never depends on what is installed on the machine.
func clipCapture(t *testing.T) func() string {
	t.Helper()
	var buf bytes.Buffer
	oldOut, oldTools := clipOut, clipTools
	clipOut, clipTools = &buf, nil
	t.Cleanup(func() { clipOut, clipTools = oldOut, oldTools })
	return func() string {
		s := buf.String()
		i := strings.LastIndex(s, "\x1b]52;c;")
		if i < 0 {
			return ""
		}
		payload, _, _ := strings.Cut(s[i+len("\x1b]52;c;"):], "\a")
		out, err := base64.StdEncoding.DecodeString(payload)
		if err != nil {
			t.Fatalf("clipboard payload is not base64: %q", payload)
		}
		return string(out)
	}
}

// TestCopyCutTextSelection: with a horizontal selection live, alt+y yanks the
// cursor node's whole branch — a run is a pointer at its node, and the
// clipboard carries the tree — and releases the selection; alt+x takes exactly
// the run and splices it out.
func TestCopyCutTextSelection(t *testing.T) {
	clip := clipCapture(t)
	m, n := spanModel(t) // "paint some words here"

	m.caret = len("paint ")
	m.press("ctrl+shift+right") // "some"
	m.press("alt+y")
	if got, want := clip(), " ○ paint some words here\n"; got != want {
		t.Fatalf("yanked %q, want the whole branch %q", got, want)
	}
	if n.name != "paint some words here" {
		t.Fatalf("copy must not touch the text, got %q", n.name)
	}
	if m.textSelOn {
		t.Fatal("copy must release the selection")
	}
	if strings.Contains(m.flash, "terminal") || strings.Contains(m.flash, "clip.exe") {
		t.Fatalf("copy flash exposed the clipboard transport: %q", m.flash)
	}

	m.caret = len("paint ")
	m.press("ctrl+shift+right")
	m.press("alt+x")
	if got := clip(); got != "some" {
		t.Fatalf("cut %q, want %q", got, "some")
	}
	if n.name != "paint  words here" {
		t.Fatalf("after cut: %q", n.name)
	}
	if m.caret != 6 {
		t.Fatalf("caret = %d, want the cut point 6", m.caret)
	}
	if m.textSelOn {
		t.Fatal("cut must release the selection")
	}

	// ctrl+x is the cut twin, for terminals that swallow the alt chord
	m.caret = 0
	m.press("ctrl+shift+right")
	m.press("ctrl+x")
	if n.name != "  words here" { // "paint" gone; both cuts left their separators
		t.Fatalf("ctrl+x must cut like alt+x, got %q", n.name)
	}
}

// TestYankRunWithChildrenCopiesTheSubtree: yanking with a text run live on a
// node that has children carries the whole subtree, not just the run.
func TestYankRunWithChildrenCopiesTheSubtree(t *testing.T) {
	clip := clipCapture(t)
	root := &item{uuid: "root"}
	a := &item{uuid: "a", name: "alpha beta", parent: root}
	b := &item{uuid: "b", name: "beta", parent: a}
	root.children = []*item{a}
	a.children = []*item{b}
	tr := &tree{root: root, byUUID: map[string]*item{"root": root, "a": a, "b": b},
		externalNames: map[string]string{}, snapshots: map[string]snapshot{}}
	m := &Model{tree: tr, viewStack: []*item{root}, width: 80, height: 24}
	m.refreshRows()

	m.caret = len("alpha ")
	m.press("ctrl+shift+right") // "beta"
	m.press("alt+y")
	if got, want := clip(), " ○ alpha beta\n ╰─ ○ beta\n"; got != want {
		t.Fatalf("yanked %q, want the branch %q", got, want)
	}
	if !strings.Contains(m.flash, "2 nodes copied to clipboard") {
		t.Fatalf("flash = %q, want the node count and clipboard destination", m.flash)
	}
}

// TestCutRunKeepsStyledRunsAligned: cutting text before a styled run slides the
// run back by what was removed, like any other edit.
func TestCutRunKeepsStyledRunsAligned(t *testing.T) {
	clipCapture(t)
	m, n := spanModel(t)
	m.setSpanStyle(n, 11, 16, "color:red") // "words"

	m.caret = 6
	m.press("ctrl+shift+right") // "some"
	m.press("alt+x")
	sp := nodeSpans["p1"]
	if len(sp) != 1 || sp[0].Start != 7 || sp[0].End != 12 {
		t.Fatalf("styled run after cut = %+v, want [7,12)", sp)
	}
	if got := string([]rune(n.name)[sp[0].Start:sp[0].End]); got != "words" {
		t.Fatalf("styled run now covers %q", got)
	}
}

// TestCopyNodesIndentsSubtree: with no selection, yank takes the cursor node and
// everything under it, drawn as the outline draws it.
func TestCopyNodesIndentsSubtree(t *testing.T) {
	clip := clipCapture(t)
	root := &item{uuid: "root"}
	a := &item{uuid: "a", name: "alpha", parent: root}
	b := &item{uuid: "b", name: "beta", parent: a}
	c := &item{uuid: "c", name: "gamma", parent: b}
	d := &item{uuid: "d", name: "delta", parent: root}
	root.children = []*item{a, d}
	a.children = []*item{b}
	b.children = []*item{c}
	tr := &tree{root: root, byUUID: map[string]*item{"root": root, "a": a, "b": b, "c": c, "d": d},
		externalNames: map[string]string{}, snapshots: map[string]snapshot{}}
	m := &Model{tree: tr, viewStack: []*item{root}, width: 80, height: 24}
	m.refreshRows()

	m.press("alt+y")
	want := " ○ alpha\n ╰─ ○ beta\n    ╰─ ○ gamma\n"
	if got := clip(); got != want {
		t.Fatalf("copied %q, want %q", got, want)
	}
	if !strings.Contains(m.flash, "3 nodes copied to clipboard") {
		t.Fatalf("flash = %q, want the node count and clipboard destination", m.flash)
	}
}

// TestPlainYAndXTypeInsteadOfCopying: with a selection live, plain y/x are
// ordinary letters — the clipboard commands live on alt+y / alt+x only, so
// typing is never hijacked into a copy or a cut.
func TestPlainYAndXTypeInsteadOfCopying(t *testing.T) {
	clip := clipCapture(t)
	m := newTestModel(80, "alpha", "beta", "gamma")
	m.press("shift+down")
	m.press("y")
	if got := clip(); got != "" {
		t.Fatalf("plain y must not copy, got %q", got)
	}
	if names := namesOf(m.tree.root.children); !reflect.DeepEqual(names, []string{"alpha", "ybeta", "gamma"}) {
		t.Fatalf("plain y must type into the node, not cut anything: %v", names)
	}
	if m.selOn {
		t.Fatal("typing must release the row selection")
	}
	if got := m.rows[m.cursor].it.name; got != "ybeta" {
		t.Fatalf("plain y typed %q into the cursor node, want %q", got, "ybeta")
	}
	m.rows[m.cursor].it.name = "beta" // undo the typed letter before the clipboard checks

	// alt+y copies the selected rows as branches and leaves them in place
	m.cursor = 0
	m.press("shift+down")
	m.press("alt+y")
	if got := clip(); got != " ○ alpha\n ○ beta\n" {
		t.Fatalf("alt+y copied %q, want the branches", got)
	}
	if !strings.Contains(m.flash, "2 nodes copied to clipboard") {
		t.Fatalf("copy flash = %q", m.flash)
	}
	if m.selOn {
		t.Fatal("copy must release the row selection")
	}
	if names := namesOf(m.tree.root.children); !reflect.DeepEqual(names, []string{"alpha", "beta", "gamma"}) {
		t.Fatalf("copy changed nodes: %v", names)
	}

	// alt+x cuts the selection; a plain x would just type
	m.cursor = 0
	m.press("shift+down")
	m.press("alt+x")
	if names := namesOf(m.tree.root.children); !reflect.DeepEqual(names, []string{"gamma"}) {
		t.Fatalf("alt+x left nodes: %v", names)
	}
	if !strings.Contains(m.flash, "2 nodes cut to clipboard") {
		t.Fatalf("cut flash = %q", m.flash)
	}
}

// TestCutRowSelectionRemovesNodes: a row selection cuts every selected root,
// subtrees included, and the outline loses them.
func TestCutRowSelectionRemovesNodes(t *testing.T) {
	clip := clipCapture(t)
	db := database.InitTestMemoryDB(t)
	root := &item{uuid: "root"}
	a := &item{uuid: "a", name: "alpha", parent: root}
	b := &item{uuid: "b", name: "beta", parent: root}
	b1 := &item{uuid: "b1", name: "beta one", parent: b}
	c := &item{uuid: "c", name: "gamma", parent: root}
	root.children = []*item{a, b, c}
	b.children = []*item{b1}
	tr := &tree{db: db, root: root, externalNames: map[string]string{}, snapshots: map[string]snapshot{},
		byUUID: map[string]*item{"root": root, "a": a, "b": b, "b1": b1, "c": c}}
	m := &Model{db: db, tree: tr, viewStack: []*item{root}, width: 80, height: 24}
	m.refreshRows()

	m.press("shift+down") // alpha..beta
	m.press("alt+x")
	if got, want := clip(), " ○ alpha\n ○ beta\n ╰─ ○ beta one\n"; got != want {
		t.Fatalf("cut %q, want %q", got, want)
	}
	if len(root.children) != 1 || root.children[0] != c {
		names := []string{}
		for _, ch := range root.children {
			names = append(names, ch.name)
		}
		t.Fatalf("outline still holds %v, want only gamma", names)
	}
	if m.selOn {
		t.Fatal("cut must release the row selection")
	}
}

// TestCopyFallsBackWhenNoClipboard: with neither a tool nor a writable
// terminal, nothing is copied and the cut leaves the text alone.
func TestCopyFallsBackWhenNoClipboard(t *testing.T) {
	clipCapture(t)
	clipOut = failWriter{}
	m, n := spanModel(t)
	m.caret = 6
	m.press("ctrl+shift+right")
	m.press("alt+x")
	if n.name != "paint some words here" {
		t.Fatalf("a failed copy must not cut: %q", n.name)
	}
	if !strings.Contains(m.flash, "no clipboard") {
		t.Fatalf("flash = %q, want the no-clipboard notice", m.flash)
	}
}

type failWriter struct{}

func (failWriter) Write([]byte) (int, error) { return 0, errNoClip }

var errNoClip = errClip("no clipboard")

type errClip string

func (e errClip) Error() string { return string(e) }

// TestCutAndPasteRoundTripsSubtree: a subtree cut with alt+x leaves the
// clipboard carrying the tree as it was DRAWN, and pasting it back lands a tree
// again — the fan-out reads depth off the rail instead of leaving connectors
// and bullets in the names.
func TestCutAndPasteRoundTripsSubtree(t *testing.T) {
	clip := clipCapture(t)
	root := &item{uuid: "root"}
	a := &item{uuid: "a", name: "alpha", parent: root}
	b := &item{uuid: "b", name: "beta", parent: a}
	c := &item{uuid: "c", name: "gamma", parent: b}
	d := &item{uuid: "d", name: "delta", parent: a}
	keep := &item{uuid: "k", name: "keep", parent: root}
	root.children = []*item{a, keep}
	a.children = []*item{b, d}
	b.children = []*item{c}
	tr := &tree{root: root, externalNames: map[string]string{}, snapshots: map[string]snapshot{},
		byUUID: map[string]*item{"root": root, "a": a, "b": b, "c": c, "d": d, "k": keep}}
	m := &Model{tree: tr, viewStack: []*item{root}, width: 80, height: 24}
	m.refreshRows()

	m.press("alt+x") // the whole alpha subtree
	if len(root.children) != 1 {
		t.Fatalf("cut left %d roots, want only keep", len(root.children))
	}
	text := clip()
	if want := " ○ alpha\n ├─ ○ beta\n │  ╰─ ○ gamma\n ╰─ ○ delta\n"; text != want {
		t.Fatalf("cut %q, want %q", text, want)
	}

	// open a fresh row and paste it back there (the first line continues the
	// row you paste into, so an empty one keeps the tree's shape)
	m.cursor = m.rowIndexOf(keep)
	m.caret = len([]rune(keep.name))
	m.press("enter")
	m.pasteFanOut(m.cursorItem(), pasteLines(text))

	got := map[string]int{} // name → depth
	m.walkForest([]*item{root}, func(it *item, r row, _ bool) { got[it.name] = r.depth })
	for name, want := range map[string]int{"alpha": 1, "beta": 2, "gamma": 3, "delta": 2} {
		if got[name] != want {
			t.Fatalf("%q landed at depth %d, want %d (tree: %v)", name, got[name], want, got)
		}
	}
}

// TestCopyDrawsTypeGlyphs: the copy is what the outline shows, so a todo keeps
// its box, a done todo its filled box, and a collapsed parent opens — its
// children are right there under it in the copy.
func TestCopyDrawsTypeGlyphs(t *testing.T) {
	clip := clipCapture(t)
	root := &item{uuid: "root"}
	a := &item{uuid: "a", name: "chores", typ: database.TypeTodo, parent: root, collapsed: true}
	b := &item{uuid: "b", name: "dishes", typ: database.TypeTodo, parent: a, completedAt: 1}
	root.children = []*item{a}
	a.children = []*item{b}
	tr := &tree{root: root, byUUID: map[string]*item{"root": root, "a": a, "b": b},
		externalNames: map[string]string{}, snapshots: map[string]snapshot{}}
	m := &Model{tree: tr, viewStack: []*item{root}, width: 80, height: 24}
	m.refreshRows()

	m.press("alt+y")
	if got, want := clip(), " □ chores\n ╰─ ■ dishes\n"; got != want {
		t.Fatalf("copied %q, want %q", got, want)
	}
}

// TestPasteMarkdownListIsNotReadAsARail: an indented list from somewhere else
// is text, not a drawn outline. Its dashes belong to the node names and its
// depth still comes from the leading spaces.
func TestPasteMarkdownListIsNotReadAsARail(t *testing.T) {
	got := readPasteLines([]string{"- one", "  - two", "    - three"})
	want := []pasteLine{{0, "- one"}, {2, "- two"}, {4, "- three"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("markdown list read as %v, want %v", got, want)
	}
}

// TestPasteProseIsNotReadAsARail: a line of prose can accidentally look like a
// drawn row (" a b" reads as glyph "a"), so a block only parses as an outline
// when something in it could only be one.
func TestPasteProseIsNotReadAsARail(t *testing.T) {
	got := readPasteLines([]string{" a short thought", " another one"})
	want := []pasteLine{{1, "a short thought"}, {1, "another one"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("prose read as %v, want %v", got, want)
	}
}

// TestPasteRailReadsDepthFromTheConnectors: the rail a copy carries is what the
// fan-out measures depth with — the glyphs never reach the node names.
func TestPasteRailReadsDepthFromTheConnectors(t *testing.T) {
	got := readPasteLines([]string{" ○ alpha", " ├─ ○ beta", " │  ╰─ ◆ gamma", " ╰─ □ delta"})
	want := []pasteLine{{1, "alpha"}, {4, "beta"}, {7, "gamma"}, {4, "delta"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("rail read as %v, want %v", got, want)
	}
}
