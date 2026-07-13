package editor

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// The Math node (database.TypeMath) is a mathematical expression composed AS an
// outline. There is no parser and no separate editor: a node's text is either an
// OPERATOR (= ÷ ± √ Σ ∫ ^ …) whose operands are its children, or a plain ATOM
// leaf (b, 2a, n+1). The outline structure IS the AST.
//
// The type declares itself with ONE visual signal: operator glyphs render yellow
// (mathSpanColor) while the node stays fully inline-editable — type "=" and it
// lights up. Simple expressions live on a single row; complex ones fan out into
// a child tree, and each operator row carries a dim linear PREVIEW of its whole
// subtree (mathBodyTail) so the gestalt reads at a glance without expanding.
//
// alt+r exports LaTeX for the node's subtree (mathToLatex) into its ephemeral
// run band — run it on any node to get the LaTeX for just that sub-expression.
//
// This file is the whole type: the registry hooks, the unified symbol table that
// drives BOTH coloring and LaTeX, and the pure preview / LaTeX serializers.

// ── the symbol table: one source for coloring AND LaTeX ────────────────────

// mathSymInfo is one entry of the symbol table: the LaTeX that renders the rune,
// and whether it is an OPERATOR (tinted yellow in a math body). Non-operators
// (Greek letters, ℏ, ∞, brackets-as-values) map to LaTeX but keep their color.
type mathSymInfo struct {
	latex string
	op    bool
}

// mathSym maps a single rune to its LaTeX and operator-ness. A new symbol is one
// line here — it teaches the node to both color and export the rune. Built in a
// func (not a literal) so a duplicated rune is a last-wins assignment, never a
// compile error, and the groups stay readable.
var mathSym = buildMathSym()

func buildMathSym() map[rune]mathSymInfo {
	m := map[rune]mathSymInfo{}
	op := func(rs string, l string) {
		for _, r := range rs {
			m[r] = mathSymInfo{l, true}
		}
	}
	sym := func(r rune, l string) { m[r] = mathSymInfo{l, false} }

	// relations
	op("=", "=")
	op("≠", `\neq`)
	op("≈", `\approx`)
	op("≡", `\equiv`)
	op("≅", `\cong`)
	op("∼", `\sim`)
	op("≃", `\simeq`)
	op("≜", `\triangleq`)
	op("≝", `\stackrel{\text{def}}{=}`)
	op("∝", `\propto`)
	op("<", "<")
	op(">", ">")
	op("≤", `\leq`)
	op("≥", `\geq`)
	op("≪", `\ll`)
	op("≫", `\gg`)
	// arithmetic
	op("+", "+")
	op("-", "-")
	op("−", "-")
	op("±", `\pm`)
	op("∓", `\mp`)
	op("×", `\times`)
	op("⋅", `\cdot`)
	op("·", `\cdot`)
	op("÷", `\div`)
	op("∗", "*")
	op("*", "*")
	op("/", "/")
	op("^", "^")
	op("_", "_")
	// roots / big operators / calculus
	op("√", `\sqrt`)
	op("∛", `\sqrt[3]`)
	op("∜", `\sqrt[4]`)
	op("Σ", `\sum`)
	op("∑", `\sum`)
	op("∏", `\prod`)
	op("∐", `\coprod`)
	op("∫", `\int`)
	op("∬", `\iint`)
	op("∭", `\iiint`)
	op("∮", `\oint`)
	op("∂", `\partial`)
	op("∇", `\nabla`)
	op("∆", `\Delta`)
	// arrows / logic / set theory
	op("→", `\to`)
	op("←", `\leftarrow`)
	op("↔", `\leftrightarrow`)
	op("⇒", `\Rightarrow`)
	op("⇐", `\Leftarrow`)
	op("⇔", `\Leftrightarrow`)
	op("↦", `\mapsto`)
	op("⟹", `\implies`)
	op("⟸", `\impliedby`)
	op("⟺", `\iff`)
	op("∈", `\in`)
	op("∉", `\notin`)
	op("∋", `\ni`)
	op("∪", `\cup`)
	op("∩", `\cap`)
	op("⊂", `\subset`)
	op("⊃", `\supset`)
	op("⊆", `\subseteq`)
	op("⊇", `\supseteq`)
	op("∖", `\setminus`)
	op("∀", `\forall`)
	op("∃", `\exists`)
	op("∄", `\nexists`)
	op("¬", `\neg`)
	op("∧", `\wedge`)
	op("∨", `\vee`)
	op("⊕", `\oplus`)
	op("⊗", `\otimes`)
	op("⊙", `\odot`)
	op("∘", `\circ`)
	op("⊥", `\perp`)
	op("∥", `\parallel`)
	op("⟂", `\perp`)
	op("∴", `\therefore`)
	op("∵", `\because`)
	op("⊢", `\vdash`)
	op("⊨", `\models`)
	op("≺", `\prec`)
	op("≻", `\succ`)
	op("⪯", `\preceq`)
	op("⪰", `\succeq`)
	op("≔", `\coloneqq`)
	op("⋆", `\star`)
	op("∙", `\bullet`)
	op("⊤", `\top`)
	op("⇌", `\rightleftharpoons`)
	op("⋀", `\bigwedge`)
	op("⋁", `\bigvee`)
	op("⋃", `\bigcup`)
	op("⋂", `\bigcap`)

	// lowercase Greek
	sym('α', `\alpha`)
	sym('β', `\beta`)
	sym('γ', `\gamma`)
	sym('δ', `\delta`)
	sym('ε', `\epsilon`)
	sym('ϵ', `\epsilon`)
	sym('ζ', `\zeta`)
	sym('η', `\eta`)
	sym('θ', `\theta`)
	sym('ϑ', `\vartheta`)
	sym('ι', `\iota`)
	sym('κ', `\kappa`)
	sym('λ', `\lambda`)
	sym('μ', `\mu`)
	sym('ν', `\nu`)
	sym('ξ', `\xi`)
	sym('π', `\pi`)
	sym('ϖ', `\varpi`)
	sym('ρ', `\rho`)
	sym('ϱ', `\varrho`)
	sym('σ', `\sigma`)
	sym('ς', `\varsigma`)
	sym('τ', `\tau`)
	sym('υ', `\upsilon`)
	sym('φ', `\phi`)
	sym('ϕ', `\varphi`)
	sym('χ', `\chi`)
	sym('ψ', `\psi`)
	sym('ω', `\omega`)
	// uppercase Greek
	sym('Γ', `\Gamma`)
	sym('Δ', `\Delta`)
	sym('Θ', `\Theta`)
	sym('Λ', `\Lambda`)
	sym('Ξ', `\Xi`)
	sym('Π', `\Pi`)
	sym('Φ', `\Phi`)
	sym('Ψ', `\Psi`)
	sym('Ω', `\Omega`)
	sym('Υ', `\Upsilon`)
	// blackboard / constants / misc values
	sym('∞', `\infty`)
	sym('∅', `\emptyset`)
	sym('ℏ', `\hbar`)
	sym('ℓ', `\ell`)
	sym('ℝ', `\mathbb{R}`)
	sym('ℂ', `\mathbb{C}`)
	sym('ℤ', `\mathbb{Z}`)
	sym('ℕ', `\mathbb{N}`)
	sym('ℚ', `\mathbb{Q}`)
	sym('ℙ', `\mathbb{P}`)
	sym('ℵ', `\aleph`)
	sym('°', `^\circ`)
	sym('′', `'`)
	sym('″', `''`)
	sym('…', `\dots`)
	sym('⋯', `\cdots`)
	sym('⋮', `\vdots`)
	sym('⋱', `\ddots`)
	sym('⟨', `\langle`)
	sym('⟩', `\rangle`)
	sym('⌊', `\lfloor`)
	sym('⌋', `\rfloor`)
	sym('⌈', `\lceil`)
	sym('⌉', `\rceil`)
	sym('|', `|`)
	sym('‖', `\|`)
	sym('ħ', `\hbar`) // U+0127 (Latin h-bar), alongside ℏ U+210F
	sym('ℑ', `\Im`)
	sym('ℜ', `\Re`)
	sym('↑', `\uparrow`)
	sym('↓', `\downarrow`)
	sym('⇑', `\Uparrow`)
	sym('⇓', `\Downarrow`)
	sym('∠', `\angle`)
	sym('△', `\triangle`)
	sym('□', `\square`)
	sym('∡', `\measuredangle`)
	sym('∎', `\blacksquare`)
	sym('ℯ', "e")
	sym('ℊ', "g")
	return m
}

// superscript / subscript rune → its inner text, grouped into ^{…} / _{…} by
// latexAtom so "x²" is x^{2} and "aᵢⱼ" is a_{ij}.
var mathSuper = map[rune]string{
	'⁰': "0", '¹': "1", '²': "2", '³': "3", '⁴': "4", '⁵': "5", '⁶': "6",
	'⁷': "7", '⁸': "8", '⁹': "9", '⁺': "+", '⁻': "-", '⁼': "=", '⁽': "(",
	'⁾': ")", 'ⁿ': "n", 'ⁱ': "i",
	// superscript letters
	'ᵃ': "a", 'ᵇ': "b", 'ᶜ': "c", 'ᵈ': "d", 'ᵉ': "e", 'ᶠ': "f", 'ᵍ': "g",
	'ʰ': "h", 'ʲ': "j", 'ᵏ': "k", 'ˡ': "l", 'ᵐ': "m", 'ᵒ': "o", 'ᵖ': "p",
	'ʳ': "r", 'ˢ': "s", 'ᵗ': "t", 'ᵘ': "u", 'ᵛ': "v", 'ʷ': "w", 'ˣ': "x", 'ʸ': "y", 'ᶻ': "z",
}
var mathSub = map[rune]string{
	'₀': "0", '₁': "1", '₂': "2", '₃': "3", '₄': "4", '₅': "5", '₆': "6",
	'₇': "7", '₈': "8", '₉': "9", '₊': "+", '₋': "-", '₌': "=", '₍': "(",
	'₎': ")", 'ₙ': "n", 'ₓ': "x", 'ᵢ': "i", 'ⱼ': "j", 'ₖ': "k", 'ₗ': "l",
	'ₘ': "m", 'ₚ': "p", 'ₜ': "t", 'ₐ': "a", 'ₑ': "e", 'ₒ': "o",
	'ₕ': "h", 'ₛ': "s", 'ᵣ': "r", 'ᵤ': "u", 'ᵥ': "v",
}

var mathBrackets = map[rune]bool{'(': true, ')': true, '[': true, ']': true, '{': true, '}': true}

// mathWords are multi-letter functions/operators tinted whole; the value is the
// LaTeX command they export to (\sin, \lim, …).
var mathWords = map[string]string{
	"lim": `\lim`, "sin": `\sin`, "cos": `\cos`, "tan": `\tan`, "cot": `\cot`,
	"sec": `\sec`, "csc": `\csc`, "sinh": `\sinh`, "cosh": `\cosh`, "tanh": `\tanh`,
	"arcsin": `\arcsin`, "arccos": `\arccos`, "arctan": `\arctan`,
	"log": `\log`, "ln": `\ln`, "exp": `\exp`, "det": `\det`, "gcd": `\gcd`,
	"min": `\min`, "max": `\max`, "sup": `\sup`, "inf": `\inf`, "arg": `\arg`,
	"deg": `\deg`, "dim": `\dim`, "ker": `\ker`, "mod": `\bmod`, "Pr": `\Pr`,
	"tr": `\operatorname{tr}`, "sgn": `\operatorname{sgn}`, "sqrt": `\sqrt`,
	"abs": `\operatorname{abs}`,
}

func mathIsOpRune(r rune) bool { return mathSym[r].op }

// ── registry hooks ─────────────────────────────────────────────────────────

// mathSpanColor tints operator runes yellow, function words yellow, and brackets
// dim, leaving variables and numbers the body's normal color. It rides the
// per-rune kwColor channel (see renderBody), so caret and selection keep working.
func mathSpanColor(it *item, runes []rune) map[int]string {
	if len(runes) == 0 {
		return nil
	}
	out := make(map[int]string)
	s := string(runes)
	for kw := range mathWords {
		for _, idx := range wordRuneIndices(s, kw) {
			for k := idx; k < idx+len([]rune(kw)) && k < len(runes); k++ {
				out[k] = cYellow
			}
		}
	}
	for i, r := range runes {
		if _, taken := out[i]; taken {
			continue
		}
		switch {
		case mathIsOpRune(r):
			out[i] = cYellow
		case mathBrackets[r]:
			out[i] = cDim
		}
	}
	return out
}

// mathBodyTail is the dim linear preview of an operator node's subtree, shown
// after the operator glyph. A leaf (no children) is already the whole expression
// inline, so it gets no tail — that is the "simple stays inline" behavior.
func mathBodyTail(it *item) string {
	if it == nil || len(it.children) == 0 {
		return ""
	}
	p := mathPreview(it)
	if p == "" {
		return ""
	}
	return cDim + p + cReset
}

// mathToContext gives the node its own <math> element carrying the flattened
// expression, so an agent reads "x = (-b ± √(b²-4ac))/2a" instead of a bare
// operator glyph; the AST children still nest inside.
func mathToContext(it *item) contextXML {
	return contextXML{tag: "math", body: mathPreview(it)}
}

// runMathLatex (alt+r) exports the node's subtree as LaTeX into its ephemeral run
// band. Because it serializes THIS node down, running it on any sub-node yields
// the LaTeX for just that part of the expression.
func runMathLatex(m *Model, it *item) tea.Cmd {
	l := mathToLatex(it)
	r := m.ensureRun(it.uuid)
	r.out = []outLine{{text: l}}
	r.dropped = 0
	m.persistRunOut(it.uuid)
	m.flash = "LaTeX → output"
	m.refreshRows()
	return nil
}

// mathFlashActions names the alt+r action "latex" in the flash bar.
func mathFlashActions(m *Model, it *item) []flashAction {
	return []flashAction{{verb: "latex", color: cGreen, do: runMathLatex}}
}

// ── preview: subtree → one linear line ─────────────────────────────────────

// mathPreview flattens a math node's subtree into a compact one-line form. A
// leaf is its own text; an operator composes its children by shape (fraction,
// power, radical, big operator, matrix, cases, or plain infix). Pure/recursive —
// the same routine feeds the inline preview and the agent context.
func mathPreview(it *item) string {
	if it == nil {
		return ""
	}
	name := strings.TrimSpace(it.name)
	if len(it.children) == 0 {
		return name
	}
	kids := make([]string, len(it.children))
	for i, c := range it.children {
		kids[i] = mathPreview(c)
	}
	switch op := name; {
	case op == "÷" || op == "/":
		if len(kids) >= 2 {
			return mathWrap(kids[0]) + "/" + mathWrap(strings.Join(kids[1:], " "))
		}
	case op == "^" || op == "_":
		if len(kids) >= 2 {
			return mathWrap(kids[0]) + op + mathWrap(strings.Join(kids[1:], " "))
		}
	case mathIsRadical(op):
		return "√(" + strings.Join(kids, ", ") + ")"
	case op == "matrix":
		return "[" + strings.Join(kids, "; ") + "]"
	case op == "cases":
		return "{ " + strings.Join(kids, " ; ") + " }"
	case mathIsBigOrFunc(op):
		return op + " " + strings.Join(kids, " ")
	case mathIsInfix(op):
		return strings.Join(kids, " "+op+" ")
	}
	return name + " " + strings.Join(kids, " ")
}

// mathWrap parenthesizes an operand that is itself a compound expression, so a
// fraction or power reads unambiguously — "(-b ± √…)/2a", not "-b ± √…/2a".
func mathWrap(s string) string {
	if mathCompound(s) {
		return "(" + s + ")"
	}
	return s
}

// mathCompound reports whether a preview string carries a top-level binary
// operator (a space-flanked operator, e.g. " + "). A bare "-b" or "2a" is atomic.
func mathCompound(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return false
	}
	if (strings.HasPrefix(s, "(") && strings.HasSuffix(s, ")")) ||
		(strings.HasPrefix(s, "[") && strings.HasSuffix(s, "]")) {
		return false
	}
	rs := []rune(s)
	for i := 1; i < len(rs)-1; i++ {
		if rs[i-1] == ' ' && rs[i+1] == ' ' && mathIsOpRune(rs[i]) {
			return true
		}
	}
	return false
}

// ── LaTeX: subtree → LaTeX ─────────────────────────────────────────────────

// mathToLatex serializes a math node's subtree to LaTeX. Leaves convert their
// unicode symbols (latexAtom); operators emit the matching LaTeX structure
// (\frac, \sqrt, ^{}, \sum_{}^{}, pmatrix, cases, or an infix join). It covers a
// broad, common subset of LaTeX — not its diagram/3-D/tikz corners.
func mathToLatex(it *item) string {
	if it == nil {
		return ""
	}
	name := strings.TrimSpace(it.name)
	if len(it.children) == 0 {
		return latexAtom(name)
	}
	kids := make([]string, len(it.children))
	for i, c := range it.children {
		kids[i] = mathToLatex(c)
	}
	switch op := name; {
	case op == "÷" || op == "/":
		if len(kids) >= 2 {
			return `\frac{` + kids[0] + `}{` + strings.Join(kids[1:], " ") + `}`
		}
	case op == "^":
		if len(kids) >= 2 {
			return latexBase(kids[0]) + "^{" + strings.Join(kids[1:], " ") + "}"
		}
	case op == "_":
		if len(kids) >= 2 {
			return latexBase(kids[0]) + "_{" + strings.Join(kids[1:], " ") + "}"
		}
	case mathIsRadical(op):
		root := `\sqrt`
		if op == "∛" {
			root = `\sqrt[3]`
		} else if op == "∜" {
			root = `\sqrt[4]`
		}
		return root + "{" + strings.Join(kids, " ") + "}"
	case mathIsBigOp(op):
		return latexBigOp(bigOpLatex(op), kids)
	case mathIsFunc(op):
		return funcLatex(op) + latexArg(kids)
	case op == "matrix":
		return `\begin{pmatrix} ` + strings.Join(kids, ` \\ `) + ` \end{pmatrix}`
	case op == "cases":
		return `\begin{cases} ` + strings.Join(kids, ` \\ `) + ` \end{cases}`
	case mathIsInfix(op):
		return strings.Join(kids, " "+latexOp(op)+" ")
	}
	return latexAtom(name) + " " + strings.Join(kids, " ")
}

// latexAtom converts an atom's unicode to LaTeX: runs of super/subscripts fold
// into ^{…}/_{…}, mapped symbols become their command, ASCII passes through.
func latexAtom(s string) string {
	rs := []rune(s)
	var b strings.Builder
	for i := 0; i < len(rs); {
		if inner, ok := mathSuper[rs[i]]; ok {
			b.WriteString("^{")
			b.WriteString(inner)
			for i++; i < len(rs); i++ {
				g, ok := mathSuper[rs[i]]
				if !ok {
					break
				}
				b.WriteString(g)
			}
			b.WriteString("}")
			continue
		}
		if inner, ok := mathSub[rs[i]]; ok {
			b.WriteString("_{")
			b.WriteString(inner)
			for i++; i < len(rs); i++ {
				g, ok := mathSub[rs[i]]
				if !ok {
					break
				}
				b.WriteString(g)
			}
			b.WriteString("}")
			continue
		}
		if info, ok := mathSym[rs[i]]; ok {
			b.WriteString(info.latex)
			// a command followed by a letter needs a space so "\pi x" ≠ "\pix"
			if strings.HasPrefix(info.latex, `\`) && i+1 < len(rs) && isMathLetter(rs[i+1]) {
				b.WriteByte(' ')
			}
			i++
			continue
		}
		b.WriteRune(rs[i])
		i++
	}
	return b.String()
}

// latexBase wraps a compound power/sub base in parens ("(a+b)^2"), leaves a
// single token bare ("e^x").
func latexBase(s string) string {
	if latexNeedsParen(s) {
		return "(" + s + ")"
	}
	return s
}

// latexArg wraps a function's operands in parens: \sin(x), \log(a + b).
func latexArg(kids []string) string {
	inner := strings.Join(kids, ", ")
	return "(" + inner + ")"
}

// latexBigOp renders \sum / \int with optional limits from the leading children:
// 3+ kids → _{lower}^{upper} body; 2 → _{lower} body; 1 → body.
func latexBigOp(cmd string, kids []string) string {
	switch len(kids) {
	case 0:
		return cmd
	case 1:
		return cmd + " " + kids[0]
	case 2:
		return cmd + "_{" + kids[0] + "} " + kids[1]
	default:
		return cmd + "_{" + kids[0] + "}^{" + kids[1] + "} " + strings.Join(kids[2:], " ")
	}
}

// latexNeedsParen reports whether a LaTeX fragment is compound (a top-level infix
// operator, shown by a spaced operator) and so needs parens as a base/operand.
func latexNeedsParen(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" || (strings.HasPrefix(s, "(") && strings.HasSuffix(s, ")")) {
		return false
	}
	return strings.Contains(s, " ")
}

func latexOp(op string) string {
	var b strings.Builder
	for i, r := range []rune(op) {
		if info, ok := mathSym[r]; ok {
			b.WriteString(info.latex)
		} else {
			b.WriteRune(r)
		}
		_ = i
	}
	return b.String()
}

func bigOpLatex(op string) string {
	if info, ok := mathSym[[]rune(op)[0]]; ok && info.latex != "" {
		return info.latex
	}
	return op
}

func funcLatex(op string) string {
	if l, ok := mathWords[op]; ok {
		return l
	}
	return `\operatorname{` + op + `}`
}

// ── operator classification ────────────────────────────────────────────────

func mathIsRadical(op string) bool { return op == "√" || op == "∛" || op == "∜" || op == "sqrt" }

func mathIsBigOp(op string) bool {
	switch op {
	case "Σ", "∑", "∏", "∐", "∫", "∬", "∭", "∮", "⋁", "⋀", "⋃", "⋂":
		return true
	}
	return false
}

func mathIsFunc(op string) bool {
	_, ok := mathWords[op]
	return ok && !mathIsRadical(op)
}

func mathIsBigOrFunc(op string) bool { return mathIsBigOp(op) || mathIsFunc(op) }

// mathIsInfix reports an operator made only of operator runes and not one of the
// special-cased shapes above — so it composes its children with an infix join.
func mathIsInfix(op string) bool {
	if op == "" {
		return false
	}
	for _, r := range op {
		if !mathIsOpRune(r) {
			return false
		}
	}
	return op != "÷" && op != "/" && op != "^" && op != "_" && !mathIsRadical(op)
}

// wordRuneIndices returns the rune index of each whole-word occurrence of kw in
// s (bounded by non-letters), so "ln" in "ln x" tints but "sin" in "sinh" does
// not. Boundaries use the math letter class (ASCII letters).
func wordRuneIndices(s, kw string) []int {
	var out []int
	rs := []rune(s)
	kr := []rune(kw)
	for i := 0; i+len(kr) <= len(rs); i++ {
		if string(rs[i:i+len(kr)]) != kw {
			continue
		}
		if i > 0 && isMathLetter(rs[i-1]) {
			continue
		}
		if i+len(kr) < len(rs) && isMathLetter(rs[i+len(kr)]) {
			continue
		}
		out = append(out, i)
	}
	return out
}

func isMathLetter(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
}
