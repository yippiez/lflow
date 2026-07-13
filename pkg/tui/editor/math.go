package editor

import "strings"

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
// This file is the whole type: two registry hooks (spanColor, bodyTail), a
// context serializer, and the pure preview/classification helpers under them.

// ── operator vocabulary ────────────────────────────────────────────────────

// mathOpRunes are single-rune operators; each is tinted yellow in a math body
// and drives the preview's infix/prefix layout below.
var mathOpRunes = func() map[rune]bool {
	m := map[rune]bool{}
	for _, r := range "=≠<>≤≥≈≡+-−±∓×*·/÷^_√∛∜Σ∑∏∫∮∂∇→↦⇒⟺∝∈∉∪∩" {
		m[r] = true
	}
	return m
}()

// mathBrackets are structural — dimmed, not yellow — so "(-b ± √…)" reads as
// operators-on-structure rather than a wall of highlight.
var mathBrackets = map[rune]bool{'(': true, ')': true, '[': true, ']': true, '{': true, '}': true}

// mathWords are multi-letter functions/operators tinted whole.
var mathWords = []string{"lim", "sin", "cos", "tan", "cot", "sec", "csc", "log", "ln", "exp", "det", "sqrt", "abs"}

// ── registry hooks ─────────────────────────────────────────────────────────

// mathSpanColor tints operator runes yellow and brackets dim, leaving variables
// and numbers the body's normal color. It rides the per-rune kwColor channel
// (see renderBody), so the caret and selection keep working while typing.
func mathSpanColor(it *item, runes []rune) map[int]string {
	if len(runes) == 0 {
		return nil
	}
	out := make(map[int]string)
	s := string(runes)
	for _, kw := range mathWords {
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
		case mathOpRunes[r]:
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

// ── preview: subtree → one linear line ─────────────────────────────────────

// mathPreview flattens a math node's subtree into a compact one-line form. A
// leaf is its own text; an operator composes its children by shape (fraction,
// power, radical, big operator, matrix, cases, or plain infix). It is pure and
// recursive — the same routine feeds the inline preview and the agent context.
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
	// unknown operator with children: read it as a prefix over its operands.
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
// operator (a space-flanked operator, e.g. " + ", " ± ", " = "). A bare "-b" or
// "2a" is atomic and stays unwrapped; "b² - 4ac" is compound.
func mathCompound(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return false
	}
	// already fully bracketed → not compound for wrapping purposes
	if (strings.HasPrefix(s, "(") && strings.HasSuffix(s, ")")) ||
		(strings.HasPrefix(s, "[") && strings.HasSuffix(s, "]")) {
		return false
	}
	rs := []rune(s)
	for i := 1; i < len(rs)-1; i++ {
		if rs[i-1] == ' ' && rs[i+1] == ' ' && mathOpRunes[rs[i]] {
			return true
		}
	}
	return false
}

func mathIsRadical(op string) bool { return op == "√" || op == "∛" || op == "∜" || op == "sqrt" }

func mathIsBigOrFunc(op string) bool {
	switch op {
	case "Σ", "∑", "∏", "∫", "∮", "∂", "∇", "lim", "sin", "cos", "tan",
		"cot", "sec", "csc", "log", "ln", "exp", "det", "abs":
		return true
	}
	return false
}

func mathIsInfix(op string) bool {
	if op == "" {
		return false
	}
	for _, r := range op {
		if !mathOpRunes[r] {
			return false
		}
	}
	// a run made only of operator runes and not a special-cased shape above
	return op != "÷" && op != "/" && op != "^" && op != "_" && !mathIsRadical(op)
}

// wordRuneIndices returns the rune index of each whole-word occurrence of kw in
// s (bounded by non-letters), so "ln" in "ln x" tints but the "ln" in a longer
// token does not. Word boundaries use the math letter class (ASCII letters).
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
