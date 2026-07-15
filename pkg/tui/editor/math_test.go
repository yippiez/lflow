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

func TestMathToLatexShapes(t *testing.T) {
	cases := []struct {
		name string
		it   *item
		want string
	}{
		{"atom unicode → latex", mleaf("θ ≤ π"), `\theta \leq \pi`},
		{"superscript folds", mleaf("x²"), `x^{2}`},
		{"multi superscript groups", mleaf("x²⁵"), `x^{25}`},
		{"subscript folds", mleaf("aᵢ"), `a_{i}`},
		{"fraction", mop("÷", mleaf("a"), mleaf("b")), `\frac{a}{b}`},
		{"power wraps compound base",
			mop("^", mop("+", mleaf("a"), mleaf("b")), mleaf("2")),
			`(a + b)^{2}`},
		{"power bare base", mop("^", mleaf("e"), mleaf("x")), `e^{x}`},
		{"radical", mop("√", mleaf("2")), `\sqrt{2}`},
		{"sum with limits",
			mop("Σ", mleaf("i=1"), mleaf("n"), mop("^", mleaf("i"), mleaf("2"))),
			`\sum_{i=1}^{n} i^{2}`},
		{"integral with limits",
			mop("∫", mleaf("0"), mleaf("∞"), mleaf("e⁻ˣ dx")),
			`\int_{0}^{\infty} e^{-x} dx`},
		{"function application", mop("sin", mleaf("θ")), `\sin(\theta)`},
		{"relation infix", mop("=", mleaf("E"), mop("×", mleaf("m"), mleaf("c²"))),
			`E = m \times c^{2}`},
		{"matrix",
			mop("matrix", mleaf("a & b"), mleaf("c & d")),
			`\begin{pmatrix} a & b \\ c & d \end{pmatrix}`},
		{"cases",
			mop("cases", mleaf("x & x ≥ 0"), mleaf("-x & x < 0")),
			`\begin{cases} x & x \geq 0 \\ -x & x < 0 \end{cases}`},
		{"quadratic full tree",
			mop("=", mleaf("x"),
				mop("÷",
					mop("±", mleaf("-b"), mop("√", mleaf("b²-4ac"))),
					mleaf("2a"))),
			`x = \frac{-b \pm \sqrt{b^{2}-4ac}}{2a}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := mathToLatex(c.it); got != c.want {
				t.Errorf("mathToLatex()\n  got  %q\n  want %q", got, c.want)
			}
		})
	}
}

func TestMathToLatexOperators(t *testing.T) {
	cases := []struct {
		name string
		it   *item
		want string
	}{
		{"logical and node", mop("and", mleaf("a"), mleaf("b")), `a \land b`},
		{"logical or node", mop("or", mleaf("p"), mleaf("q")), `p \lor q`},
		{"xor node", mop("xor", mleaf("a"), mleaf("b")), `a \oplus b`},
		{"mod node", mop("mod", mleaf("a"), mleaf("n")), `a \bmod n`},
		{"right shift node", mop(">>", mleaf("a"), mleaf("n")), `a \gg n`},
		{"left shift node", mop("<<", mleaf("a"), mleaf("n")), `a \ll n`},
		{"symbolic and node", mop("&&", mleaf("x"), mleaf("y")), `x \land y`},
		{"not equal node", mop("!=", mleaf("a"), mleaf("b")), `a \neq b`},
		{"assign node", mop(":=", mleaf("x"), mop("+", mleaf("x"), mleaf("1"))), `x \coloneqq x + 1`},
		{"implies node", mop("=>", mleaf("a"), mleaf("b")), `a \Rightarrow b`},
		{"einsum function", mop("einsum", mleaf("ij,jk->ik"), mleaf("A"), mleaf("B")),
			`\operatorname{einsum}(ij,jk\to ik, A, B)`},
		{"softmax function", mop("softmax", mleaf("z")), `\operatorname{softmax}(z)`},
		// atom-level operator conversion
		{"atom right shift", mleaf("a >> n"), `a \gg n`},
		{"atom logical words", mleaf("not(a and b)"), `\lnot(a \land b)`},
		{"atom not equal", mleaf("a != b"), `a \neq b`},
		{"atom einstein sum", mleaf("cᵢₖ = aᵢⱼ bⱼₖ"), `c_{ik} = a_{ij} b_{jk}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := mathToLatex(c.it); got != c.want {
				t.Errorf("mathToLatex()\n  got  %q\n  want %q", got, c.want)
			}
		})
	}
}

// TestMathNonsensical pins the "nonsensical AST does not work" contract: a
// malformed tree must never crash and must never fake a valid structure. A
// fraction/power with the wrong number of operands degrades to a neutral
// fallback (no \frac / no ^{}), and a garbage operator is passed through literally.
func TestMathNonsensical(t *testing.T) {
	cases := []struct {
		name        string
		it          *item
		wantLatex   string // exact fallback
		mustNotHave string // structure it must NOT fabricate ("" = skip)
	}{
		{"fraction one operand", mop("÷", mleaf("a")), `\div a`, `\frac`},
		{"fraction three operands", mop("÷", mleaf("a"), mleaf("b"), mleaf("c")), `\div a b c`, `\frac`},
		{"power one operand", mop("^", mleaf("a")), `^ a`, `^{`},
		{"subscript zero operands is leaf", mleaf("_"), `_`, `_{`},
		{"radical no operand is leaf", mleaf("√"), `\sqrt`, `\sqrt{`},
		{"relation one operand drops operator", mop("=", mleaf("a")), `a`, `=`},
		{"garbage operator passes through", mop("@@@", mleaf("a"), mleaf("b")), `@@@ a b`, `\frac`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := mathToLatex(c.it) // must not panic
			if got != c.wantLatex {
				t.Errorf("mathToLatex()\n  got  %q\n  want %q", got, c.wantLatex)
			}
			if c.mustNotHave != "" && strings.Contains(got, c.mustNotHave) {
				t.Errorf("nonsensical tree fabricated %q in %q", c.mustNotHave, got)
			}
			// preview and body tail must also survive without panicking
			_ = mathPreview(c.it)
			_ = mathBodyTail(c.it)
		})
	}
	// an empty-name operator with children must not panic and must not fake a frac.
	if got := mathToLatex(mop("", mleaf("a"), mleaf("b"))); strings.Contains(got, `\frac`) {
		t.Errorf("empty operator fabricated a fraction: %q", got)
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
