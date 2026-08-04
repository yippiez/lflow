package filecodec

import "strings"

// Math node conversion for the file codecs: a math subtree (operator text with
// operand children, atom leaves — see editor/math.go) renders to a real
// expression in a programming language, or a linear pretty form for markdown.
// One-way by design: the file gets a plain expression; reopening the file
// reads it back as an ordinary statement (python/rust) or a math atom
// (markdown $$ block). The tree is an authoring convenience, not a storage
// format the file must carry.

// mathInfix maps operator symbols to their programming-language spelling.
// Symbols already spelled like code (+ - * / % == != < <= > >= << >> & | ^^ …)
// pass through verbatim via the default arm.
var mathInfix = map[string]struct{ py, rs string }{
	"×": {"*", "*"}, "·": {"*", "*"}, "÷": {"/", "/"},
	"^": {"**", ".pow"}, "=": {"==", "=="}, "≠": {"!=", "!="},
	"≤": {"<=", "<="}, "≥": {">=", ">="},
	"∧": {"and", "&&"}, "∨": {"or", "||"}, "mod": {"%", "%"},
}

// mathFn maps prefix/function operators: rendered name(args).
var mathFn = map[string]struct{ py, rs string }{
	"√": {"math.sqrt", "f64::sqrt"}, "Σ": {"sum", "sum"},
	"abs": {"abs", "f64::abs"}, "min": {"min", "f64::min"}, "max": {"max", "f64::max"},
	"sin": {"math.sin", "f64::sin"}, "cos": {"math.cos", "f64::cos"},
	"tan": {"math.tan", "f64::tan"}, "ln": {"math.log", "f64::ln"},
	"log": {"math.log10", "f64::log10"}, "exp": {"math.exp", "f64::exp"},
}

// mathAtom maps constant symbols.
var mathAtom = map[string]struct{ py, rs string }{
	"π": {"math.pi", "std::f64::consts::PI"},
	"e": {"math.e", "std::f64::consts::E"},
	"∞": {"math.inf", "f64::INFINITY"},
}

// mathExpr renders a math subtree as an expression in lang ("python"/"rust").
// A leaf is its own text (constants translated); an operator composes its
// children: known infix ops join them, everything else becomes name(args).
func mathExpr(n *SrcNode, lang string) string {
	pick := func(py, rs string) string {
		if lang == "rust" {
			return rs
		}
		return py
	}
	text := strings.TrimSpace(n.Text)
	if len(n.Kids) == 0 {
		if a, ok := mathAtom[text]; ok {
			return pick(a.py, a.rs)
		}
		return text
	}
	args := make([]string, 0, len(n.Kids))
	for _, k := range n.Kids {
		args = append(args, mathExpr(k, lang))
	}
	if op, ok := mathInfix[text]; ok {
		sym := pick(op.py, op.rs)
		if lang == "rust" && text == "^" {
			// rust has no ** — a.pow(b), folding left
			out := args[0]
			for _, a := range args[1:] {
				out = "(" + out + ").powf(" + a + ")"
			}
			return out
		}
		return "(" + strings.Join(args, " "+sym+" ") + ")"
	}
	if fn, ok := mathFn[text]; ok {
		return pick(fn.py, fn.rs) + "(" + strings.Join(args, ", ") + ")"
	}
	// code-spelled infix (+ - * / …): single-token non-alphanumeric operators
	if isOperatorToken(text) {
		return "(" + strings.Join(args, " "+text+" ") + ")"
	}
	// unknown named operator: a call
	return text + "(" + strings.Join(args, ", ") + ")"
}

// mathLinear renders a math subtree as one pretty line for markdown $$ blocks,
// keeping the original symbols.
func mathLinear(n *SrcNode) string {
	text := strings.TrimSpace(n.Text)
	if len(n.Kids) == 0 {
		return text
	}
	args := make([]string, 0, len(n.Kids))
	for _, k := range n.Kids {
		args = append(args, mathLinear(k))
	}
	// same precedence as mathExpr: known infix, then known prefix/function
	// symbols (√, Σ, …) — checking isOperatorToken first would catch these
	// short unicode symbols too and flatten "√x" down to a bare "(x)"
	if mathInfixKnown(text) {
		return "(" + strings.Join(args, " "+text+" ") + ")"
	}
	if _, ok := mathFn[text]; ok {
		return text + "(" + strings.Join(args, ", ") + ")"
	}
	if isOperatorToken(text) {
		return "(" + strings.Join(args, " "+text+" ") + ")"
	}
	return text + "(" + strings.Join(args, ", ") + ")"
}

func mathInfixKnown(s string) bool { _, ok := mathInfix[s]; return ok }

// isOperatorToken reports a short symbol-only token (+, -, <=, //, …).
func isOperatorToken(s string) bool {
	if s == "" || len(s) > 3 {
		return false
	}
	for _, r := range s {
		if r == '_' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			return false
		}
	}
	return true
}
