package editor

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/lflow/lflow/packages/database"
)

// markupModel builds a db-backed model and a markup subtree from an indented
// sketch, so a test reads like the outline it describes. Two spaces per level.
// A line of the form "<spec> | text" gets an element chip carrying spec and the
// text after it; a line with no "|" is plain text content.
func markupModel(t *testing.T, typ, sketch string) (*Model, *item) {
	t.Helper()
	m, _ := dbModel(t, database.Node{UUID: "root0", Name: "seed"})
	m.chips = map[string]database.Chip{}

	var root *item
	stack := map[int]*item{}
	n := 0
	for _, line := range strings.Split(strings.TrimRight(sketch, "\n"), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		depth := (len(line) - len(strings.TrimLeft(line, " "))) / 2
		body := strings.TrimSpace(line)

		n++
		it := &item{typ: typ}
		it.uuid = "n" + strings.Repeat("x", n)
		if spec, text, ok := strings.Cut(body, "|"); ok {
			anchor := m.createLabeledChip(chipKindElement, strings.TrimSpace(spec),
				elementChipLabel(strings.TrimSpace(spec)))
			it.name = anchor + strings.TrimSpace(text)
		} else {
			it.name = body
		}
		m.tree.byUUID[it.uuid] = it
		if depth == 0 {
			root = it
		} else {
			p := stack[depth-1]
			it.parent = p
			p.children = append(p.children, it)
		}
		stack[depth] = it
	}
	return m, root
}

// attach hangs a built subtree off the model's root so row-level helpers work.
func attach(m *Model, root *item) {
	m.tree.root.children = []*item{root}
	root.parent = m.tree.root
	m.refreshRows()
}

// TestMarkupSerializesTheOutline: the outline structure IS the document tree —
// a row's reserved element is the tag, its children are its children.
func TestMarkupSerializesTheOutline(t *testing.T) {
	m, root := markupModel(t, database.TypeSVG, `
svg viewBox="0 0 24 24" width=24 |
  circle cx=12 cy=12 r=10 fill=#569cd6 |
  text x=12 y=20 | hello there
`)
	want := strings.Join([]string{
		`<svg viewBox="0 0 24 24" width="24">`,
		`  <circle cx="12" cy="12" r="10" fill="#569cd6"/>`,
		`  <text x="12" y="20">`,
		`    hello there`,
		`  </text>`,
		`</svg>`,
	}, "\n")
	if got := m.markupSerialize(root, 0); got != want {
		t.Errorf("serialized:\n%s\n\nwant:\n%s", got, want)
	}
}

// TestMarkupProseIsNeverAnElement is the reason the slot is reserved: half of
// HTML's tag names are ordinary English words, so a row is markup only when it
// SAYS it is — never because of how its first word reads.
func TestMarkupProseIsNeverAnElement(t *testing.T) {
	m, root := markupModel(t, database.TypeHTML, `
div class=card |
  a headline that runs on for quite a while here
  code review notes
  time to ship it
`)
	got := m.markupSerialize(root, 0)
	for _, want := range []string{
		"a headline that runs on for quite a while here",
		"code review notes",
		"time to ship it",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("prose was lost:\n%s", got)
		}
	}
	for _, unwanted := range []string{"<a>", "<a/>", "<code>", "<time>"} {
		if strings.Contains(got, unwanted) {
			t.Errorf("prose became %s:\n%s", unwanted, got)
		}
	}
}

// TestMarkupTextEscaped: a sentence about markup is a sentence, not markup.
func TestMarkupTextEscaped(t *testing.T) {
	m, root := markupModel(t, database.TypeHTML, `
p |
  hello <world> & friends
`)
	if got := m.markupSerialize(root, 0); !strings.Contains(got, "hello &lt;world&gt; &amp; friends") {
		t.Errorf("text was not escaped:\n%s", got)
	}
}

// TestMarkupVoidAndEmptyElements: an element with nothing in it closes itself
// when the language says it is void, and writes an empty pair when it does not —
// <div></div> is a div, <div/> is a browser bug waiting to happen.
func TestMarkupVoidAndEmptyElements(t *testing.T) {
	for _, tc := range []struct{ sketch, want string }{
		{"br |", "<br/>"},
		{"div class=box |", `<div class="box"></div>`},
		{"input disabled type=text |", `<input disabled type="text"/>`},
		{"span | some words", "<span>\n  some words\n</span>"},
	} {
		m, root := markupModel(t, database.TypeHTML, tc.sketch)
		if got := m.markupSerialize(root, 0); got != tc.want {
			t.Errorf("%q serialized to %q, want %q", tc.sketch, got, tc.want)
		}
	}
}

// TestMarkupAttributesSurvive: width=100 and a quoted multi-word value both
// reach the markup, which is what the reserved slot carries.
func TestMarkupAttributesSurvive(t *testing.T) {
	m, root := markupModel(t, database.TypeSVG, `svg width=100 viewBox="0 0 10 10" |`)
	got := m.markupSerialize(root, 0)
	for _, want := range []string{`width="100"`, `viewBox="0 0 10 10"`} {
		if !strings.Contains(got, want) {
			t.Errorf("attribute lost: %s not in %s", want, got)
		}
	}
}

// TestMarkupRowMarksItsLanguage: the mark at the row's end says which language
// the reserved element belongs to.
func TestMarkupRowMarksItsLanguage(t *testing.T) {
	if markupMark(database.TypeHTML) != "</>" {
		t.Errorf("HTML mark = %q", markupMark(database.TypeHTML))
	}
	if markupMark(database.TypeSVG) != "<◇>" {
		t.Errorf("SVG mark = %q", markupMark(database.TypeSVG))
	}
	if markupMark(database.TypeBullets) != "" {
		t.Error("a plain type got a markup mark")
	}
}

// TestMarkupRowShowsTruncatedResult: the row carries the document it composes,
// cut to a glance, and ⌥e shows all of it.
func TestMarkupRowShowsTruncatedResult(t *testing.T) {
	var b strings.Builder
	b.WriteString("div |\n")
	for i := 0; i < 20; i++ {
		b.WriteString("  p |\n    a fairly long sentence of body text\n")
	}
	m, root := markupModel(t, database.TypeHTML, b.String())
	attach(m, root)

	suffix := stripSGR(m.typeSuffix(m.rows[m.rowIndexOf(root)]))
	if !strings.Contains(suffix, "</>") {
		t.Errorf("row is unmarked: %q", suffix)
	}
	if !strings.Contains(suffix, "→ ") || !strings.Contains(suffix, "…") {
		t.Errorf("row does not carry a truncated result: %q", suffix)
	}

	full := strings.Join(m.markupViewContent(root, "", 200), "\n")
	if strings.Count(full, "a fairly long sentence") != 20 {
		t.Errorf("⌥e is not the whole document:\n%s", full)
	}
}

// TestSVGDocumentWrapsAnInnerSubtree: ⌥r on a shape inside a drawing renders
// that shape, so the wrapper is supplied rather than leaving the rasterizer a
// fragment it will refuse.
func TestSVGDocumentWrapsAnInnerSubtree(t *testing.T) {
	m, inner := markupModel(t, database.TypeSVG, `circle cx=5 cy=5 r=4 |`)
	doc := m.svgDocument(inner)
	if !strings.HasPrefix(doc, `<svg xmlns="http://www.w3.org/2000/svg">`) {
		t.Errorf("inner subtree was not wrapped:\n%s", doc)
	}
	if !strings.Contains(doc, `<circle cx="5" cy="5" r="4"/>`) {
		t.Errorf("the shape is missing:\n%s", doc)
	}

	m2, root := markupModel(t, database.TypeSVG, `svg width=10 |`)
	got := m2.svgDocument(root)
	if strings.Count(got, "<svg") != 1 {
		t.Errorf("an svg root was wrapped again:\n%s", got)
	}
	if !strings.Contains(got, `xmlns="http://www.w3.org/2000/svg"`) {
		t.Errorf("the namespace is missing:\n%s", got)
	}
}

// TestHTMLRunWritesTheMarkup: ⌥r on an HTML node puts the serialized document in
// its run band, the way ⌥r on a Math node puts LaTeX there.
func TestHTMLRunWritesTheMarkup(t *testing.T) {
	m, root := markupModel(t, database.TypeHTML, `
div class=box |
  hello
`)
	attach(m, root)

	runHTMLOut(m, root)
	var lines []string
	for _, l := range m.ensureRun(root.uuid).out {
		lines = append(lines, l.text)
	}
	if got := strings.Join(lines, "\n"); got != "<div class=\"box\">\n  hello\n</div>" {
		t.Errorf("run band holds:\n%s", got)
	}
}

// TestSVGRunRendersAPicture is the round trip: an outline becomes an SVG
// document, the document becomes pixels, and the pixels become the node's own
// blob. Skipped where no rasterizer is installed — the machine, not the code.
func TestSVGRunRendersAPicture(t *testing.T) {
	if !haveRasterizer() {
		t.Skip("no SVG rasterizer installed")
	}
	m, root := markupModel(t, database.TypeSVG, `
svg width=40 height=40 viewBox="0 0 40 40" |
  circle cx=20 cy=20 r=18 fill=#569cd6 |
`)
	attach(m, root)

	runSVGRender(m, root)
	if strings.Contains(m.flash, "svg:") {
		t.Fatalf("render failed: %s", m.flash)
	}
	info, ok := m.imageLoad(root.uuid)
	if !ok {
		t.Fatal("no picture was stored on the node")
	}
	if info.w < 10 || info.h < 10 {
		t.Errorf("picture is %d×%d — the document did not carry its size", info.w, info.h)
	}
	if !(markupView{}).picture(m, root) {
		t.Error("a rendered SVG did not switch ⌥e to its picture")
	}
	m.cursor = m.rowIndexOf(root)
	if bands := m.svgBandLines(m.rows[m.cursor], false, 80); len(bands) == 0 {
		t.Error("the rendered picture drew no band")
	}
}

// TestMarkupOnTypeReservesTheSlot: making a node SVG/HTML gives it an element
// chip and opens it — a markup node with no element is not yet anything.
func TestMarkupOnTypeReservesTheSlot(t *testing.T) {
	m, _ := dbModel(t, database.Node{UUID: "n", Name: "the card"})
	it := m.tree.byUUID["n"]
	it.typ = database.TypeHTML
	markupOnType(m, it)

	c, ok := m.markupElementChip(it)
	if !ok {
		t.Fatal("no element chip was reserved")
	}
	if m.mode != modeCmdEdit || m.cmdEditID != c.ID {
		t.Errorf("the element field did not open: mode=%v id=%q", m.mode, m.cmdEditID)
	}
	// the element is the row's HEAD; the words already there stay as its text
	if !strings.HasPrefix(it.name, chipAnchor(c.ID)) {
		t.Errorf("the element is not at the head of the row: %q", it.name)
	}
	if got := m.markupTextOf(it); got != "the card" {
		t.Errorf("row text = %q, want the words that were already there", got)
	}

	// filling it in makes the row an element
	m.cmdEditValue = "div class=card"
	m.saveCmdEdit()
	el := m.markupParse(it)
	if el.tag != "div" || len(el.attrs) != 1 || el.attrs[0].value != "card" {
		t.Errorf("parsed element = %+v", el)
	}
	if got := m.chips[c.ID].Label; got != "<div class=card>" {
		t.Errorf("chip label = %q", got)
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
// ordinary editable rows — the text beside the element is text you type.
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
		if d.run == nil || d.onType == nil || d.view == nil {
			t.Errorf("%s is missing a hook", key)
		}
	}
}
