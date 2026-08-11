package editor

import (
	"strings"
	"testing"

	"github.com/lflow/lflow/tui/database"
)

// What the parser reads has to LAND correctly: the right node types on the
// right rows, the tree the parser saw, and the caret where the paste ended.
// These tests drive the Model, not the parser.

// dumpTree renders the outline under root the way dumpPaste renders a parsed
// forest, so a landed paste can be compared against the same shape.
func dumpTree(m *Model, root *item) string {
	var b strings.Builder
	var walk func(kids []*item, depth int)
	walk = func(kids []*item, depth int) {
		for _, it := range kids {
			b.WriteString(strings.Repeat("  ", depth))
			typ := it.typ
			if typ == "" {
				typ = database.TypeBullets
			}
			b.WriteString(typ)
			if it.completedAt > 0 {
				b.WriteString("(done)")
			}
			if it.note != "" {
				b.WriteString("(" + it.note + ")")
			}
			if it.name != "" {
				b.WriteString(" " + strings.ReplaceAll(it.name, "\n", "⏎"))
			}
			b.WriteByte('\n')
			walk(it.children, depth+1)
		}
	}
	walk(root.children, 0)
	return b.String()
}

// pasteModel is a model holding one empty row, the state you are in after
// pressing Enter — where a paste normally lands.
func pasteModel(t *testing.T) (*Model, *item) {
	t.Helper()
	m := newTestModel(80, "")
	return m, m.tree.root.children[0]
}

// TestPasteLandsTypedTree: the everyday paste. Types reach the nodes, the
// nesting reaches the tree, and the row pasted into is the paste's first node
// rather than an empty leftover above it.
func TestPasteLandsTypedTree(t *testing.T) {
	m, cur := pasteModel(t)
	m.pasteInto(cur, `## Sprint
- [x] parse it
- [ ] test it
  - the broken cases
> and write it down
`)
	want := `h2 Sprint
  todo(done) parse it
  todo test it
    bullets the broken cases
  quote and write it down
`
	if got := dumpTree(m, m.tree.root); got != want {
		t.Fatalf("outline after paste:\n%s\nwant:\n%s", got, want)
	}
}

// TestPasteEmptyRowTakesTheShape: a fresh row becomes the pasted node — type,
// tick and all — instead of a bullet spelling one out.
func TestPasteEmptyRowTakesTheShape(t *testing.T) {
	m, cur := pasteModel(t)
	m.pasteInto(cur, "- [x] ship it")
	if cur.typ != database.TypeTodo || cur.name != "ship it" || cur.completedAt == 0 {
		t.Fatalf("row = %q type %q completed %d, want a ticked todo named %q",
			cur.name, cur.typ, cur.completedAt, "ship it")
	}
}

// TestPasteIntoTextKeepsTheRow: pasting into a row that already has text is an
// insert at the caret — the row's own type is not up for reinterpretation.
func TestPasteIntoTextKeepsTheRow(t *testing.T) {
	m := newTestModel(80, "hello world")
	cur := m.tree.root.children[0]
	m.caret = 5
	m.pasteInto(cur, "# there")
	if cur.typ != "" {
		t.Fatalf("row took type %q from a paste into existing text", cur.typ)
	}
	if want := "hello# there world"; cur.name != want {
		t.Fatalf("row = %q, want %q", cur.name, want)
	}
}

// TestPasteSingleLineOnlyShapesAnEmptyRow guards the one-line gate in keys.go:
// a marker on an empty row reshapes it, the same text mid-sentence does not,
// and a plain URL never does (the link chip owns that gesture).
func TestPasteSingleLineOnlyShapesAnEmptyRow(t *testing.T) {
	empty := &item{}
	filled := &item{name: "notes"}
	cases := []struct {
		cur  *item
		text string
		want bool
	}{
		{empty, "## Findings", true},
		{empty, "- [ ] call back", true},
		{empty, "$ make test", true},
		{empty, "just a phrase", false},
		{empty, "https://example.test/x", false},
		{filled, "## Findings", false},
	}
	for _, c := range cases {
		if got := pasteShapesRow(c.cur, c.text); got != c.want {
			t.Fatalf("pasteShapesRow(%q on %q) = %v, want %v", c.text, c.cur.name, got, c.want)
		}
	}
}

// TestPasteCaretLandsOnTheLastNode: after a paste the cursor sits at the end of
// the last thing pasted, so typing continues where the text stopped.
func TestPasteCaretLandsOnTheLastNode(t *testing.T) {
	m, cur := pasteModel(t)
	m.pasteInto(cur, "alpha\n  beta\n  gamma\n")
	last := m.cursorItem()
	if last == nil || last.name != "gamma" {
		t.Fatalf("cursor landed on %+v, want gamma", last)
	}
	if m.caret != len("gamma") {
		t.Fatalf("caret = %d, want %d", m.caret, len("gamma"))
	}
}

// TestPasteFlashNamesWhatItRead: the status line says what the paste became, so
// a reading you did not expect is visible immediately.
func TestPasteFlashNamesWhatItRead(t *testing.T) {
	m, cur := pasteModel(t)
	m.pasteInto(cur, "# Plan\n- [ ] one\n- [ ] two\n")
	if !strings.Contains(m.flash, "3 nodes") || !strings.Contains(m.flash, "2 todo") ||
		!strings.Contains(m.flash, "1 h1") {
		t.Fatalf("flash = %q, want the node count and the detected types", m.flash)
	}

	// a plain word dropped into a row is not an event and says nothing
	m2, cur2 := pasteModel(t)
	m2.pasteInto(cur2, "word")
	if m2.flash != "" {
		t.Fatalf("a one-word paste flashed %q", m2.flash)
	}
}

// TestPasteRespectsFileSessionTypes: a .py or .toml session only accepts the
// types its format can spell, so a detected heading degrades to a bullet
// instead of writing markdown into a source file.
func TestPasteRespectsFileSessionTypes(t *testing.T) {
	m, cur := pasteModel(t)
	m.allowedTypes = map[string]bool{database.TypeBullets: true, database.TypeComment: true}
	m.pasteInto(cur, "# Title\n- [ ] a task\n<!-- a comment -->\n")
	// the SHAPE the parser read is kept — the heading still owns what followed
	// it — only the types the session cannot spell fall back to bullets
	want := `bullets Title
  bullets a task
  comment a comment
`
	if got := dumpTree(m, m.tree.root); got != want {
		t.Fatalf("restricted session took:\n%s\nwant:\n%s", got, want)
	}
}

// TestPasteMakesNoGhostNodes is the CRLF regression at the Model level: a blank
// line and a control-only line between two real ones create no nameless rows.
func TestPasteMakesNoGhostNodes(t *testing.T) {
	m, cur := pasteModel(t)
	m.pasteInto(cur, "one\r\n\r\n\x01\x02\r\ntwo\r\n")
	want := "bullets one\nbullets two\n"
	if got := dumpTree(m, m.tree.root); got != want {
		t.Fatalf("outline:\n%s\nwant:\n%s", got, want)
	}
}

// TestCopyPasteRoundTripsTypes is the point of the whole plain-text contract:
// copy a mixed subtree with alt+y, paste it back, and the TYPES survive the
// trip — through nothing but the unicode text any other program would accept.
func TestCopyPasteRoundTripsTypes(t *testing.T) {
	clip := clipCapture(t)
	root := &item{uuid: "root"}
	head := &item{uuid: "h", name: "Sprint", typ: database.TypeH2, parent: root}
	done := &item{uuid: "d", name: "parse it", typ: database.TypeTodo, completedAt: 1, parent: head}
	open := &item{uuid: "o", name: "test it", typ: database.TypeTodo, parent: head}
	quote := &item{uuid: "q", name: "ship on friday", typ: database.TypeQuote, parent: head}
	code := &item{uuid: "c", name: "make test\nmake lint", typ: database.TypeCode, note: "sh", parent: head}
	rule := &item{uuid: "r", typ: database.TypeDivider, parent: head}
	cmd := &item{uuid: "b", name: "go test ./...", typ: database.TypeBash, parent: head}
	head.children = []*item{done, open, quote, code, rule, cmd}
	root.children = []*item{head}
	tr := &tree{root: root, externalNames: map[string]string{}, snapshots: map[string]snapshot{},
		byUUID: map[string]*item{"root": root, "h": head, "d": done, "o": open,
			"q": quote, "c": code, "r": rule, "b": cmd}}
	m := &Model{tree: tr, viewStack: []*item{root}, width: 80, height: 24}
	m.refreshRows()

	m.press("alt+y")
	text := clip()
	want := "## Sprint\n" +
		"  - [x] parse it\n" +
		"  - [ ] test it\n" +
		"  > ship on friday\n" +
		"  ```sh\n  make test\n  make lint\n  ```\n" +
		"  ---\n" +
		"  $ go test ./...\n"
	if text != want {
		t.Fatalf("copied:\n%q\nwant:\n%q", text, want)
	}

	m2, cur := pasteModel(t)
	m2.pasteInto(cur, text)
	got := dumpTree(m2, m2.tree.root)
	wantTree := `h2 Sprint
  todo(done) parse it
  todo test it
  quote ship on friday
  code(sh) make test⏎make lint
  divider
  bash go test ./...
`
	if got != wantTree {
		t.Fatalf("pasted back:\n%s\nwant:\n%s", got, wantTree)
	}
}

// TestCopyPasteRoundTripsPlainOutline: an outline of ordinary bullets copies as
// bare indented text — no markdown dressing — and comes home with its shape.
// This is the format a chat message or a commit body wants.
func TestCopyPasteRoundTripsPlainOutline(t *testing.T) {
	clip := clipCapture(t)
	root := &item{uuid: "root"}
	a := &item{uuid: "a", name: "alpha", parent: root}
	b := &item{uuid: "b", name: "beta", parent: a}
	c := &item{uuid: "c", name: "gamma", parent: b}
	root.children, a.children, b.children = []*item{a}, []*item{b}, []*item{c}
	tr := &tree{root: root, externalNames: map[string]string{}, snapshots: map[string]snapshot{},
		byUUID: map[string]*item{"root": root, "a": a, "b": b, "c": c}}
	m := &Model{tree: tr, viewStack: []*item{root}, width: 80, height: 24}
	m.refreshRows()
	m.press("alt+y")
	if want := "alpha\n  beta\n    gamma\n"; clip() != want {
		t.Fatalf("copied %q, want %q", clip(), want)
	}

	m2, cur := pasteModel(t)
	m2.pasteInto(cur, clip())
	want := "bullets alpha\n  bullets beta\n    bullets gamma\n"
	if got := dumpTree(m2, m2.tree.root); got != want {
		t.Fatalf("pasted back:\n%s\nwant:\n%s", got, want)
	}
}
