package editor

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/lflow/lflow/pkg/tui/context"
	"github.com/lflow/lflow/pkg/tui/database"
)

// markupTree builds a subtree from an indented sketch, so a test reads like the
// outline it describes. Two spaces per level.
func markupTree(typ string, sketch string) *item {
	var root *item
	stack := map[int]*item{}
	for _, line := range strings.Split(strings.TrimRight(sketch, "\n"), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		depth := (len(line) - len(strings.TrimLeft(line, " "))) / 2
		it := &item{typ: typ, name: strings.TrimSpace(line)}
		if depth == 0 {
			root = it
		} else {
			p := stack[depth-1]
			it.parent = p
			p.children = append(p.children, it)
		}
		stack[depth] = it
	}
	return root
}

// TestMarkupSerializesTheOutline: the outline structure IS the document tree —
// a row is a tag with its attributes, its children are its children.
func TestMarkupSerializesTheOutline(t *testing.T) {
	root := markupTree(database.TypeSVG, `
svg viewBox="0 0 24 24" width=24
  circle cx=12 cy=12 r=10 fill=#569cd6
  text x=12 y=20
    hello there
`)
	want := strings.Join([]string{
		`<svg viewBox="0 0 24 24" width="24">`,
		`  <circle cx="12" cy="12" r="10" fill="#569cd6"/>`,
		`  <text x="12" y="20">`,
		`    hello there`,
		`  </text>`,
		`</svg>`,
	}, "\n")
	if got := markupSerialize(root, 0); got != want {
		t.Errorf("serialized:\n%s\n\nwant:\n%s", got, want)
	}
}

// TestMarkupUnknownWordIsText: there is no escaping gesture for text — a word
// the language does not name is simply text, which is what lets you write prose
// in an HTML document without ceremony.
func TestMarkupUnknownWordIsText(t *testing.T) {
	root := markupTree(database.TypeHTML, `
p
  hello <world> & friends
`)
	got := markupSerialize(root, 0)
	if !strings.Contains(got, "hello &lt;world&gt; &amp; friends") {
		t.Errorf("text was not escaped:\n%s", got)
	}
	if strings.Contains(got, "<hello") {
		t.Errorf("a plain sentence was read as an element:\n%s", got)
	}
}

// TestMarkupLanguagesDoNotBorrowTags: <circle> is not an HTML element and <div>
// is not an SVG one, so each type reads only its own vocabulary.
func TestMarkupLanguagesDoNotBorrowTags(t *testing.T) {
	if el := markupParse(database.TypeHTML, "circle cx=1"); el.tag != "" {
		t.Errorf("HTML claimed the SVG tag %q", el.tag)
	}
	if el := markupParse(database.TypeSVG, "div class=x"); el.tag != "" {
		t.Errorf("SVG claimed the HTML tag %q", el.tag)
	}
	// a tag both languages have works in both
	for _, typ := range []string{database.TypeHTML, database.TypeSVG} {
		if el := markupParse(typ, "title"); el.tag != "title" {
			t.Errorf("%s did not recognize <title>", typ)
		}
	}
}

// TestMarkupVoidAndEmptyElements: an element with no children closes itself when
// the language says it is void, and writes an empty pair when it does not —
// <div></div> is a div, <div/> is a browser bug waiting to happen.
func TestMarkupVoidAndEmptyElements(t *testing.T) {
	if got := markupSerialize(markupTree(database.TypeHTML, "br"), 0); got != "<br/>" {
		t.Errorf("void element = %q", got)
	}
	if got := markupSerialize(markupTree(database.TypeHTML, "div class=box"), 0); got != `<div class="box"></div>` {
		t.Errorf("empty non-void element = %q", got)
	}
	// a valueless attribute stays valueless
	if got := markupSerialize(markupTree(database.TypeHTML, "input disabled type=text"), 0); got != `<input disabled type="text"/>` {
		t.Errorf("valueless attribute = %q", got)
	}
}

// TestMarkupPreviewReadsTheShape: an element row carries a dim one-line preview
// of its subtree, so the document reads without expanding it — the same "simple
// stays inline" the Math node has.
func TestMarkupPreviewReadsTheShape(t *testing.T) {
	root := markupTree(database.TypeHTML, `
div
  p
    hello
  br
`)
	if got := markupPreview(root); got != "<div><p>hello</p> <br/></div>" {
		t.Errorf("preview = %q", got)
	}
	// a leaf is already the whole element, so it gets no tail
	if tail := markupBodyTail(markupTree(database.TypeHTML, "br"), nil); tail != "" {
		t.Errorf("a leaf grew a tail: %q", tail)
	}
	if tail := markupBodyTail(root, nil); !strings.Contains(tail, "<p>hello</p>") {
		t.Errorf("tail = %q", tail)
	}
}

// TestMarkupColorsTagsAndAttributeNames: one visual signal, like Math's yellow
// operators — the tag reads accent, its attribute names dim, and a row that is
// only text gets no tint at all.
func TestMarkupColorsTagsAndAttributeNames(t *testing.T) {
	it := &item{typ: database.TypeSVG, name: `circle cx=12 fill="#569cd6"`}
	got := markupSpanColor(it, []rune(it.name))
	for i := 0; i < len("circle"); i++ {
		if got[i] != cAccent {
			t.Fatalf("tag rune %d = %q, want the accent", i, got[i])
		}
	}
	// "cx=" is dim, its value is not
	if got[len("circle ")] != cDim {
		t.Error("the attribute name is not dim")
	}
	if got[len("circle cx=")] == cDim {
		t.Error("the attribute VALUE was dimmed — it is the part you read")
	}
	plain := &item{typ: database.TypeSVG, name: "just some words"}
	if c := markupSpanColor(plain, []rune(plain.name)); len(c) != 0 {
		t.Errorf("a text row was tinted: %v", c)
	}
}

// TestSVGDocumentWrapsAnInnerSubtree: alt+r on a shape inside a drawing renders
// that shape, so the wrapper is supplied rather than leaving the rasterizer a
// fragment it will refuse.
func TestSVGDocumentWrapsAnInnerSubtree(t *testing.T) {
	inner := markupTree(database.TypeSVG, "circle cx=5 cy=5 r=4")
	doc := svgDocument(inner)
	if !strings.HasPrefix(doc, `<svg xmlns="http://www.w3.org/2000/svg">`) {
		t.Errorf("inner subtree was not wrapped:\n%s", doc)
	}
	if !strings.Contains(doc, `<circle cx="5" cy="5" r="4"/>`) {
		t.Errorf("the shape is missing:\n%s", doc)
	}
	// a root that IS an <svg> is not wrapped again, but does gain the namespace
	root := markupTree(database.TypeSVG, "svg width=10")
	got := svgDocument(root)
	if strings.Count(got, "<svg") != 1 {
		t.Errorf("an svg root was wrapped again:\n%s", got)
	}
	if !strings.Contains(got, `xmlns="http://www.w3.org/2000/svg"`) {
		t.Errorf("the namespace is missing:\n%s", got)
	}
}

// TestHTMLRunWritesTheMarkup: alt+r on an HTML node puts the serialized document
// in its run band, the way alt+r on a Math node puts LaTeX there.
func TestHTMLRunWritesTheMarkup(t *testing.T) {
	m, _ := dbModel(t, database.Node{UUID: "n", Name: "div class=box", Type: database.TypeHTML})
	root := m.tree.byUUID["n"]
	kid := &item{typ: database.TypeHTML, name: "hello", parent: root}
	root.children = append(root.children, kid)
	m.tree.byUUID["k"] = kid
	m.refreshRows()

	runHTMLOut(m, root)
	var lines []string
	for _, l := range m.ensureRun("n").out {
		lines = append(lines, l.text)
	}
	got := strings.Join(lines, "\n")
	if got != "<div class=\"box\">\n  hello\n</div>" {
		t.Errorf("run band holds:\n%s", got)
	}
}

// TestSVGRunRendersAPicture is the round trip: an outline becomes an SVG
// document, the document becomes pixels, and the pixels become the node's own
// blob — which is what the half-block band and ⌥e both draw from. Skipped where
// no rasterizer is installed; the machine, not the code, is what is missing.
func TestSVGRunRendersAPicture(t *testing.T) {
	if !haveRasterizer() {
		t.Skip("no SVG rasterizer installed")
	}
	db := database.InitTestMemoryDB(t)
	if err := database.EnsureRoot(db); err != nil {
		t.Fatal(err)
	}
	for _, n := range []database.Node{
		{UUID: "svg", ParentUUID: database.RootUUID, Rank: 0,
			Name: `svg width=40 height=40 viewBox="0 0 40 40"`, Type: database.TypeSVG},
		{UUID: "c", ParentUUID: "svg", Rank: 0,
			Name: "circle cx=20 cy=20 r=18 fill=#569cd6", Type: database.TypeSVG},
	} {
		if err := n.Insert(db); err != nil {
			t.Fatal(err)
		}
	}
	tr, err := loadTree(db, database.RootUUID)
	if err != nil {
		t.Fatal(err)
	}
	m := &Model{db: db, ctx: context.DnoteCtx{DB: db}, tree: tr,
		viewStack: []*item{tr.root}, width: 80, height: 24, chips: map[string]database.Chip{}}
	m.refreshRows()

	root := m.tree.byUUID["svg"]
	runSVGRender(m, root)
	if strings.Contains(m.flash, "svg:") {
		t.Fatalf("render failed: %s", m.flash)
	}
	info, ok := m.imageLoad("svg")
	if !ok {
		t.Fatal("no picture was stored on the node")
	}
	if info.w < 10 || info.h < 10 {
		t.Errorf("picture is %d×%d — the document did not carry its size", info.w, info.h)
	}
	// and it draws under the row, without any preview setting to turn on
	m.cursor = m.rowIndexOfUUID("svg")
	if bands := m.svgBandLines(m.rows[m.cursor], false, 80); len(bands) == 0 {
		t.Error("the rendered picture drew no band")
	}
}

func haveRasterizer() bool {
	for _, r := range svgRasterizers {
		if _, err := exec.LookPath(r.bin); err == nil {
			return true
		}
	}
	return false
}

// TestMarkupTypesAreRegistered: both appear in the /type picker and stay
// ordinary editable rows — the markup is text you type, not a modal editor.
func TestMarkupTypesAreRegistered(t *testing.T) {
	for key, label := range map[string]string{database.TypeSVG: "SVG", database.TypeHTML: "HTML"} {
		if !database.ValidTypes[key] {
			t.Errorf("%s is not a valid node type", key)
		}
		d := typeOf(key)
		if d.key != key || d.label != label {
			t.Errorf("descriptor for %s = %+v", key, d)
		}
		if !d.inlineEditable {
			t.Errorf("%s should be inline-editable", key)
		}
		if d.run == nil || d.spanColor == nil || d.bodyTail == nil {
			t.Errorf("%s is missing a hook", key)
		}
	}
}
