package fileeditor

import (
	"strings"
	"testing"

	"github.com/lflow/lflow/packages/database"
)

// TestMarkdownOrderedListDoesNotJoin: "1. first\n2. second" used to join into
// ONE text node (the paragraph-join guard didn't recognize ordered-list
// markers), destroying the list on save. A matching line must at minimum
// break the paragraph — one node per line, round-tripping verbatim as text.
func TestMarkdownOrderedListDoesNotJoin(t *testing.T) {
	doc, err := (markdownCodec{}).Parse("1. first\n2. second\n")
	if err != nil {
		t.Fatal(err)
	}
	if len(doc) != 2 {
		t.Fatalf("ordered list collapsed into %d node(s): %+v", len(doc), doc)
	}
	if doc[0].Text != "1. first" || doc[1].Text != "2. second" {
		t.Fatalf("ordered list text: %+v", doc)
	}
	out, err := (markdownCodec{}).Render(doc)
	if err != nil {
		t.Fatal(err)
	}
	roundTrip(t, markdownCodec{}, out, out)
}

// TestMarkdownTabIndentedList: indent was measured with TrimLeft(line, " ")
// only, so a tab-indented nested item parsed as prose (the tab survived
// inside the "trimmed" text and even hid the "- " bullet lead). Nesting must
// use the same tab-aware indentWidth the code-language codecs use.
func TestMarkdownTabIndentedList(t *testing.T) {
	doc, err := (markdownCodec{}).Parse("- clothing\n\t- rain jacket\n")
	if err != nil {
		t.Fatal(err)
	}
	if len(doc) != 1 || doc[0].Type != database.TypeBullets || doc[0].Text != "clothing" {
		t.Fatalf("shape: %+v", doc)
	}
	if len(doc[0].Kids) != 1 || doc[0].Kids[0].Text != "rain jacket" {
		t.Fatalf("tab-indented nesting: %+v", doc[0])
	}
}

// TestMarkdownCommentChildSurvives: the comment, code, divider and nlpcompute
// render arms never recursed into n.Kids, so children nested under such a
// node were silently deleted on save. A round trip must keep them.
func TestMarkdownCommentChildSurvives(t *testing.T) {
	doc := []*SrcNode{{
		Type: database.TypeComment, Text: "note",
		Kids: []*SrcNode{{Type: database.TypeBullets, Text: "detail"}},
	}}
	out, err := (markdownCodec{}).Render(doc)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "- detail") {
		t.Fatalf("comment child deleted on render: %q", out)
	}
	roundTrip(t, markdownCodec{}, out, out)
}

// TestMarkdownDividerAndCodeChildrenSurvive: same deletion bug for the other
// three arms sharing the drift (divider, code, nlpcompute).
func TestMarkdownDividerAndCodeChildrenSurvive(t *testing.T) {
	doc := []*SrcNode{
		{Type: database.TypeDivider, Kids: []*SrcNode{{Type: database.TypeText, Text: "after divider"}}},
		{Type: database.TypeCode, Text: "x = 1", Note: "python",
			Kids: []*SrcNode{{Type: database.TypeText, Text: "after code"}}},
		{Type: database.TypeNLPCompute, Text: "sum it",
			Kids: []*SrcNode{{Type: database.TypeText, Text: "after nlp"}}},
	}
	out, err := (markdownCodec{}).Render(doc)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"after divider", "after code", "after nlp"} {
		if !strings.Contains(out, want) {
			t.Fatalf("child deleted on render (%q missing): %q", want, out)
		}
	}
}

// TestClassifyMarkerBodyBareEmptyMarker: an empty marker's rendered text ends
// "lflow <type>: " with a trailing space; a comment-based codec's line-trim
// strips that trailing space before the body reaches the classifier, leaving
// a bare "lflow <type>:" with the colon at end-of-string. That must still
// restore the type, with empty text, instead of degrading to a plain comment.
func TestClassifyMarkerBodyBareEmptyMarker(t *testing.T) {
	n := classifyComment("lflow log:")
	if n == nil || n.Type != database.TypeLog || n.Text != "" {
		t.Fatalf("bare marker via classifyComment: %+v", n)
	}
	n2 := classifyMarkerBody("lflow log:", false)
	if n2 == nil || n2.Type != database.TypeLog || n2.Text != "" {
		t.Fatalf("bare marker via classifyMarkerBody: %+v", n2)
	}
}

// TestMathLinearFunctionOperators: mathLinear tested isOperatorToken before
// mathFn, so a short unicode function symbol like √ (a single-rune, 3-byte
// token — passes isOperatorToken's "short symbol" test) rendered as a bare
// infix join: "√" with child "x" collapsed to "(x)", losing the operator.
func TestMathLinearFunctionOperators(t *testing.T) {
	sqrt := &SrcNode{Type: database.TypeMath, Text: "√", Kids: []*SrcNode{{Text: "x"}}}
	if got := mathLinear(sqrt); got != "√(x)" {
		t.Fatalf("sqrt: got %q want %q", got, "√(x)")
	}
	sum := &SrcNode{Type: database.TypeMath, Text: "Σ", Kids: []*SrcNode{{Text: "a"}, {Text: "b"}}}
	if got := mathLinear(sum); got != "Σ(a, b)" {
		t.Fatalf("sum: got %q want %q", got, "Σ(a, b)")
	}
	div := &SrcNode{Type: database.TypeMath, Text: "÷", Kids: []*SrcNode{{Text: "distance"}, {Text: "speed"}}}
	if got := mathLinear(div); got != "(distance ÷ speed)" {
		t.Fatalf("div: got %q want %q", got, "(distance ÷ speed)")
	}
}
