package editor

import (
	"bytes"
	"encoding/base64"
	"reflect"
	"strings"
	"testing"

	"github.com/lflow/lflow/pkg/tui/database"
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

// TestCopyCutTextSelection: with a horizontal selection live, alt+y yanks just
// that run and leaves the node alone; alt+x takes it and splices it out.
func TestCopyCutTextSelection(t *testing.T) {
	clip := clipCapture(t)
	m, n := spanModel(t) // "paint some words here"

	m.caret = len("paint ")
	m.press("shift+right") // "some"
	m.press("alt+y")
	if got := clip(); got != "some" {
		t.Fatalf("copied %q, want %q", got, "some")
	}
	if n.name != "paint some words here" {
		t.Fatalf("copy must not touch the text, got %q", n.name)
	}
	if !m.textSelOn {
		t.Fatal("copy must keep the selection alive")
	}

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
	m.press("shift+right")
	m.press("ctrl+x")
	if n.name != "  words here" { // "paint" gone; both cuts left their separators
		t.Fatalf("ctrl+x must cut like alt+x, got %q", n.name)
	}
}

// TestCutRunKeepsStyledRunsAligned: cutting text before a styled run slides the
// run back by what was removed, like any other edit.
func TestCutRunKeepsStyledRunsAligned(t *testing.T) {
	clipCapture(t)
	m, n := spanModel(t)
	m.setSpanStyle(n, 11, 16, "color:red") // "words"

	m.caret = 6
	m.press("shift+right") // "some"
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
// everything under it, two spaces per level.
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
	want := "alpha\n  beta\n    gamma\n"
	if got := clip(); got != want {
		t.Fatalf("copied %q, want %q", got, want)
	}
	if !strings.Contains(m.flash, "3 nodes copied to clipboard") {
		t.Fatalf("flash = %q, want the node count and clipboard destination", m.flash)
	}
}

// TestPlainYAndXActOnASelection: after shift selects rows, y/x are clipboard
// commands rather than text inserted into the cursor node.
func TestPlainYAndXActOnASelection(t *testing.T) {
	clip := clipCapture(t)
	m := newTestModel(80, "alpha", "beta", "gamma")
	m.press("shift+down")
	m.press("y")
	if got := clip(); got != "alpha\nbeta\n" {
		t.Fatalf("plain y copied %q", got)
	}
	if !strings.Contains(m.flash, "2 nodes copied to clipboard") {
		t.Fatalf("copy flash = %q", m.flash)
	}
	if names := namesOf(m.tree.root.children); !reflect.DeepEqual(names, []string{"alpha", "beta", "gamma"}) {
		t.Fatalf("copy changed nodes: %v", names)
	}

	m.press("x")
	if names := namesOf(m.tree.root.children); !reflect.DeepEqual(names, []string{"gamma"}) {
		t.Fatalf("plain x left nodes: %v", names)
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
	if got, want := clip(), "alpha\nbeta\n  beta one\n"; got != want {
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
	m.press("shift+right")
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

// TestCutAndPasteRoundTripsSubtree: a subtree cut with alt+x and pasted back
// lands as a tree again — the paste fan-out reads the copy's indentation as
// depth instead of leaving spaces in the names.
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
	if want := "alpha\n  beta\n    gamma\n  delta\n"; text != want {
		t.Fatalf("cut %q, want %q", text, want)
	}

	// open a fresh row and paste it back there (the first line continues the
	// row you paste into, so an empty one keeps the tree's shape)
	m.cursor = m.rowIndexOf(keep)
	m.caret = len([]rune(keep.name))
	m.press("enter")
	m.pasteFanOut(m.cursorItem(), pasteLines(text))

	got := map[string]int{} // name → depth
	m.walkSubtrees([]*item{root}, func(it *item, depth int) { got[it.name] = depth })
	for name, want := range map[string]int{"alpha": 1, "beta": 2, "gamma": 3, "delta": 2} {
		if got[name] != want {
			t.Fatalf("%q landed at depth %d, want %d (tree: %v)", name, got[name], want, got)
		}
	}
}
