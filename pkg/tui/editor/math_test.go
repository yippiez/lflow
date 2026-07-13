package editor

import (
	"strings"
	"testing"
)

// leaf builds an atom math node.
func mleaf(name string) *item { return &item{name: name, typ: "math"} }

// mop builds an operator math node with children.
func mop(name string, kids ...*item) *item {
	return &item{name: name, typ: "math", children: kids}
}

func TestMathPreview(t *testing.T) {
	cases := []struct {
		name string
		it   *item
		want string
	}{
		{"atom", mleaf("2a"), "2a"},
		{"sum", mop("+", mleaf("a"), mleaf("b")), "a + b"},
		{"fraction wraps compound numerator",
			mop("÷", mop("+", mleaf("a"), mleaf("b")), mleaf("c")),
			"(a + b)/c"},
		{"fraction leaves atom operands bare",
			mop("/", mleaf("1"), mleaf("2")), "1/2"},
		{"power", mop("^", mleaf("a"), mleaf("n")), "a^n"},
		{"radical", mop("√", mop("-", mleaf("b²"), mleaf("4ac"))), "√(b² - 4ac)"},
		{"big operator prefix",
			mop("Σ", mleaf("i=1..n"), mleaf("i²")), "Σ i=1..n i²"},
		{"matrix", mop("matrix", mleaf("a  b"), mleaf("c  d")), "[a  b; c  d]"},
		{"cases", mop("cases", mleaf("x  x≥0"), mleaf("-x  x<0")), "{ x  x≥0 ; -x  x<0 }"},
		{"quadratic",
			mop("=", mleaf("x"),
				mop("÷",
					mop("±", mleaf("-b"), mop("√", mleaf("b²-4ac"))),
					mleaf("2a"))),
			"x = (-b ± √(b²-4ac))/2a"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := mathPreview(c.it); got != c.want {
				t.Errorf("mathPreview() = %q, want %q", got, c.want)
			}
		})
	}
}

func TestMathBodyTailInlineWhenSimple(t *testing.T) {
	// a leaf is the whole expression on one row — no preview tail.
	if got := mathBodyTail(mleaf("a + b")); got != "" {
		t.Errorf("leaf tail = %q, want empty (simple stays inline)", got)
	}
	// an operator node shows the dim preview.
	tail := mathBodyTail(mop("+", mleaf("a"), mleaf("b")))
	if !strings.Contains(tail, "a + b") {
		t.Errorf("operator tail = %q, want preview containing %q", tail, "a + b")
	}
	if !strings.HasPrefix(tail, cDim) {
		t.Errorf("operator tail = %q, want dim prefix", tail)
	}
}

func TestMathSpanColorTintsOperators(t *testing.T) {
	runes := []rune("x = -b ± √(b²)")
	got := mathSpanColor(&item{typ: "math"}, runes)
	// operators are yellow: '=' @2, '-' @4, '±' @7, '√' @9
	for _, i := range []int{2, 4, 7, 9} {
		if got[i] != cYellow {
			t.Errorf("rune %d (%q) = %q, want yellow", i, string(runes[i]), got[i])
		}
	}
	// brackets are dim
	for i, r := range runes {
		if r == '(' || r == ')' {
			if got[i] != cDim {
				t.Errorf("bracket %d = %q, want dim", i, got[i])
			}
		}
	}
	// a variable is left untinted
	if _, tinted := got[0]; tinted { // 'x'
		t.Errorf("variable 'x' should not be tinted")
	}
}

func TestMathSpanColorFunctionWords(t *testing.T) {
	runes := []rune("lim x")
	got := mathSpanColor(&item{typ: "math"}, runes)
	for i := 0; i < 3; i++ { // l,i,m
		if got[i] != cYellow {
			t.Errorf("lim rune %d = %q, want yellow", i, got[i])
		}
	}
	// the 'x' after the space is not part of the word
	if _, tinted := got[4]; tinted {
		t.Errorf("'x' should not be tinted as a function word")
	}
	// a keyword embedded in a longer token is not a word match
	if idxs := wordRuneIndices("sinh", "sin"); len(idxs) != 0 {
		t.Errorf("'sin' in 'sinh' matched at %v, want no word match", idxs)
	}
}

func TestMathToContext(t *testing.T) {
	cx := mathToContext(mop("=", mleaf("E"), mop("×", mleaf("m"), mleaf("c²"))))
	if cx.tag != "math" {
		t.Errorf("tag = %q, want math", cx.tag)
	}
	if cx.body != "E = m × c²" {
		t.Errorf("body = %q, want %q", cx.body, "E = m × c²")
	}
}
