package nodes

import (
	"fmt"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lflow/lflow/pkg/tui/database"
	"github.com/lflow/lflow/pkg/tui/editor"
)

// The Logic node: its text IS a propositional boolean expression. It parses into
// an explicit AST of gate nodes (NOT / AND / NAND / OR / NOR / XOR / XNOR /
// IMPLIES / IFF), and the alt+e view shows that AST as a tree plus the full
// truth table and a validity verdict. alt+r flashes the verdict. Everything is a
// pure function of the node text — nothing is persisted or synced.
//
// Grammar (precedence low → high; IMPLIES and IFF are right-associative):
//
//	iff     ::= imp ( ('<->'|'<=>'|'iff') imp )*
//	imp     ::= or  ( ('->'|'=>'|'implies') imp )?
//	or      ::= xor ( ('or'|'|'|'||'|'nor') xor )*
//	xor     ::= and ( ('xor'|'^'|'xnor') and )*
//	and     ::= not ( ('and'|'&'|'&&'|'nand') not )*
//	not     ::= ('not'|'!'|'~') not | atom
//	atom    ::= VAR | '0' | '1' | 'true' | 'false' | '(' iff ')'

func init() {
	editor.RegisterNodePlugin(editor.NodePlugin{
		Key: database.TypeLogic, Label: "Logic",
		InlineEditable: true, // the expression is edited inline
		Glyph:          func() (string, string) { return "∧", editor.NodeTheme().Cyan },
		Render:         logicRender,
		Run:            logicRun,
		View:           logicView{},
		ToContext:      logicToContext,
	})
}

// ── the AST ─────────────────────────────────────────────────────────────────

// lgNode is one node of the parsed boolean expression: it evaluates against a
// variable assignment, names itself for the AST tree, and reports its operands.
type lgNode interface {
	eval(env map[string]bool) bool
	label() string
	kids() []lgNode
}

type lgVar struct{ name string }

func (v lgVar) eval(e map[string]bool) bool { return e[v.name] }
func (v lgVar) label() string               { return v.name }
func (v lgVar) kids() []lgNode              { return nil }

type lgConst struct{ v bool }

func (c lgConst) eval(map[string]bool) bool { return c.v }
func (c lgConst) label() string {
	if c.v {
		return "TRUE"
	}
	return "FALSE"
}
func (c lgConst) kids() []lgNode { return nil }

type lgNot struct{ x lgNode }

func (n lgNot) eval(e map[string]bool) bool { return !n.x.eval(e) }
func (n lgNot) label() string               { return "NOT" }
func (n lgNot) kids() []lgNode              { return []lgNode{n.x} }

// lgBin is a two-input gate; op is one of and/nand/or/nor/xor/xnor/imp/iff.
type lgBin struct {
	op   string
	l, r lgNode
}

func (b lgBin) eval(e map[string]bool) bool {
	l, r := b.l.eval(e), b.r.eval(e)
	switch b.op {
	case "and":
		return l && r
	case "nand":
		return !(l && r)
	case "or":
		return l || r
	case "nor":
		return !(l || r)
	case "xor":
		return l != r
	case "xnor":
		return l == r
	case "imp":
		return !l || r
	case "iff":
		return l == r
	}
	return false
}

func (b lgBin) label() string {
	switch b.op {
	case "imp":
		return "IMPLIES (→)"
	case "iff":
		return "IFF (↔)"
	default:
		return strings.ToUpper(b.op)
	}
}

func (b lgBin) kids() []lgNode { return []lgNode{b.l, b.r} }

// lgTree renders an AST as an indented tree with box-drawing connectors.
func lgTree(n lgNode) []string {
	var out []string
	var walk func(n lgNode, prefix string, last, root bool)
	walk = func(n lgNode, prefix string, last, root bool) {
		branch, childPrefix := "", prefix
		if !root {
			if last {
				branch, childPrefix = "└─ ", prefix+"   "
			} else {
				branch, childPrefix = "├─ ", prefix+"│  "
			}
		}
		out = append(out, prefix+branch+n.label())
		kids := n.kids()
		for i, k := range kids {
			walk(k, childPrefix, i == len(kids)-1, false)
		}
	}
	walk(n, "", true, true)
	return out
}

// ── lexer ───────────────────────────────────────────────────────────────────

type lgTok struct{ kind, text string }

func lgLetter(r rune) bool { return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' }
func lgWord(r rune) bool   { return lgLetter(r) || r >= '0' && r <= '9' || r == '_' }

// lgLex turns a boolean expression into tokens. It accepts word operators
// (and/or/not/…), their symbol forms (& | ^ ! ~), and both ASCII and Unicode
// arrows for implication and equivalence.
func lgLex(s string) ([]lgTok, error) {
	var toks []lgTok
	r := []rune(s)
	push := func(kind, text string) { toks = append(toks, lgTok{kind, text}) }
	for i := 0; i < len(r); {
		c := r[i]
		switch {
		case c == ' ' || c == '\t':
			i++
		case c == '(':
			push("(", "("); i++
		case c == ')':
			push(")", ")"); i++
		case c == '~' || c == '!' || c == '¬':
			push("not", string(c)); i++
		case c == '&':
			if i+1 < len(r) && r[i+1] == '&' {
				push("and", "&&"); i += 2
			} else {
				push("and", "&"); i++
			}
		case c == '∧':
			push("and", string(c)); i++
		case c == '|':
			if i+1 < len(r) && r[i+1] == '|' {
				push("or", "||"); i += 2
			} else {
				push("or", "|"); i++
			}
		case c == '∨':
			push("or", string(c)); i++
		case c == '^' || c == '⊕':
			push("xor", string(c)); i++
		case c == '→' || c == '⇒':
			push("imp", string(c)); i++
		case c == '↔' || c == '⇔':
			push("iff", string(c)); i++
		case c == '-' && i+1 < len(r) && r[i+1] == '>':
			push("imp", "->"); i += 2
		case c == '=' && i+1 < len(r) && r[i+1] == '>':
			push("imp", "=>"); i += 2
		case c == '<' && i+2 < len(r) && r[i+1] == '-' && r[i+2] == '>':
			push("iff", "<->"); i += 3
		case c == '<' && i+2 < len(r) && r[i+1] == '=' && r[i+2] == '>':
			push("iff", "<=>"); i += 3
		case c == '0' || c == '1':
			push(map[bool]string{true: "true", false: "false"}[c == '1'], string(c)); i++
		case lgLetter(c) || c == '_':
			j := i
			for j < len(r) && lgWord(r[j]) {
				j++
			}
			w := string(r[i:j])
			i = j
			switch strings.ToLower(w) {
			case "and":
				push("and", w)
			case "nand":
				push("nand", w)
			case "or":
				push("or", w)
			case "nor":
				push("nor", w)
			case "xor":
				push("xor", w)
			case "xnor":
				push("xnor", w)
			case "not":
				push("not", w)
			case "implies":
				push("imp", w)
			case "iff":
				push("iff", w)
			case "true":
				push("true", w)
			case "false":
				push("false", w)
			default:
				push("var", w)
			}
		default:
			return nil, fmt.Errorf("unexpected character %q", string(c))
		}
	}
	return toks, nil
}

// ── parser (recursive descent → AST) ────────────────────────────────────────

type lgParser struct {
	toks []lgTok
	pos  int
	err  error
}

func (p *lgParser) peek() string {
	if p.pos < len(p.toks) {
		return p.toks[p.pos].kind
	}
	return ""
}

func (p *lgParser) is(kinds ...string) bool {
	k := p.peek()
	for _, want := range kinds {
		if k == want {
			return true
		}
	}
	return false
}

func (p *lgParser) fail(msg string) lgNode {
	if p.err == nil {
		p.err = fmt.Errorf("%s", msg)
	}
	p.pos = len(p.toks)
	return lgConst{false}
}

func (p *lgParser) parse() lgNode { return p.parseIff() }

func (p *lgParser) parseIff() lgNode {
	left := p.parseImp()
	for p.is("iff") {
		p.pos++
		left = lgBin{"iff", left, p.parseImp()}
	}
	return left
}

func (p *lgParser) parseImp() lgNode {
	left := p.parseOr()
	if p.is("imp") {
		p.pos++
		return lgBin{"imp", left, p.parseImp()} // right-associative
	}
	return left
}

func (p *lgParser) parseOr() lgNode {
	left := p.parseXor()
	for p.is("or", "nor") {
		op := p.peek()
		p.pos++
		left = lgBin{op, left, p.parseXor()}
	}
	return left
}

func (p *lgParser) parseXor() lgNode {
	left := p.parseAnd()
	for p.is("xor", "xnor") {
		op := p.peek()
		p.pos++
		left = lgBin{op, left, p.parseAnd()}
	}
	return left
}

func (p *lgParser) parseAnd() lgNode {
	left := p.parseNot()
	for p.is("and", "nand") {
		op := p.peek()
		p.pos++
		left = lgBin{op, left, p.parseNot()}
	}
	return left
}

func (p *lgParser) parseNot() lgNode {
	if p.is("not") {
		p.pos++
		return lgNot{p.parseNot()}
	}
	return p.parseAtom()
}

func (p *lgParser) parseAtom() lgNode {
	switch p.peek() {
	case "(":
		p.pos++
		inner := p.parseIff()
		if !p.is(")") {
			return p.fail("missing )")
		}
		p.pos++
		return inner
	case "true":
		p.pos++
		return lgConst{true}
	case "false":
		p.pos++
		return lgConst{false}
	case "var":
		name := p.toks[p.pos].text
		p.pos++
		return lgVar{name}
	default:
		return p.fail("expected a variable or (")
	}
}

// lgBuild lexes and parses an expression into its AST and its sorted variable
// list, erroring on lex/parse failure or leftover tokens.
func lgBuild(expr string) (lgNode, []string, error) {
	toks, err := lgLex(expr)
	if err != nil {
		return nil, nil, err
	}
	if len(toks) == 0 {
		return nil, nil, fmt.Errorf("empty expression")
	}
	var vars []string
	seen := map[string]bool{}
	for _, t := range toks {
		if t.kind == "var" && !seen[t.text] {
			seen[t.text] = true
			vars = append(vars, t.text)
		}
	}
	sort.Strings(vars)
	if len(vars) > 8 {
		return nil, nil, fmt.Errorf("too many variables (%d) — max 8", len(vars))
	}
	p := &lgParser{toks: toks}
	ast := p.parse()
	if p.err != nil {
		return nil, nil, p.err
	}
	if p.pos != len(toks) {
		return nil, nil, fmt.Errorf("unexpected %q", toks[p.pos].text)
	}
	return ast, vars, nil
}

// lgTruth enumerates every assignment of vars (MSB = first var) and evaluates
// the AST, returning the assignment rows and the result column.
func lgTruth(ast lgNode, vars []string) (table [][]bool, results []bool) {
	n := len(vars)
	for mask := 0; mask < (1 << n); mask++ {
		env := make(map[string]bool, n)
		bits := make([]bool, n)
		for i, v := range vars {
			bit := mask&(1<<(n-1-i)) != 0
			env[v] = bit
			bits[i] = bit
		}
		table = append(table, bits)
		results = append(results, ast.eval(env))
	}
	return table, results
}

// lgVerdict classifies a result column.
func lgVerdict(results []bool) string {
	allT, allF := true, true
	for _, r := range results {
		if r {
			allF = false
		} else {
			allT = false
		}
	}
	switch {
	case allT:
		return "tautology — true under every assignment"
	case allF:
		return "contradiction — false under every assignment"
	default:
		return "contingent — satisfiable, but not valid"
	}
}

// ── rendered output ─────────────────────────────────────────────────────────

func lgTF(b bool) string {
	if b {
		return "T"
	}
	return "F"
}

func lgPad(s string, w int) string {
	if n := w - len([]rune(s)); n > 0 {
		return s + strings.Repeat(" ", n)
	}
	return s
}

func lgHead(format string, a ...any) string {
	th := editor.NodeTheme()
	return th.Cyan + fmt.Sprintf(format, a...) + th.Reset
}

func lgSub(format string, a ...any) string {
	th := editor.NodeTheme()
	return th.Dim + fmt.Sprintf(format, a...) + th.Reset
}

func lgErr(format string, a ...any) string {
	th := editor.NodeTheme()
	return th.Red + fmt.Sprintf(format, a...) + th.Reset
}

// lgCompute is the alt+e view body: the AST tree, then the truth table, then the
// verdict. It never errors — a bad expression renders an error line.
func lgCompute(expr string) []string {
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return []string{lgErr("write a boolean expression, e.g. (A and B) or not C")}
	}
	ast, vars, err := lgBuild(expr)
	if err != nil {
		return []string{lgErr("%v", err)}
	}
	out := []string{lgHead("expression · %s", expr), lgSub("AST")}
	for _, l := range lgTree(ast) {
		out = append(out, "  "+l)
	}
	out = append(out, "", lgSub("truth table"))

	table, results := lgTruth(ast, vars)
	if len(vars) == 0 { // a constant expression
		out = append(out, "  result: "+lgTF(results[0]))
	} else {
		widths := make([]int, len(vars))
		var head strings.Builder
		head.WriteString("  ")
		for i, v := range vars {
			widths[i] = len([]rune(v))
			if widths[i] < 1 {
				widths[i] = 1
			}
			head.WriteString(lgPad(v, widths[i]) + " ")
		}
		head.WriteString("│ out")
		out = append(out, head.String())
		for i := range table {
			var line strings.Builder
			line.WriteString("  ")
			for j := range vars {
				line.WriteString(lgPad(lgTF(table[i][j]), widths[j]) + " ")
			}
			line.WriteString("│ " + lgTF(results[i]))
			out = append(out, line.String())
		}
	}
	return append(out, lgSub("%s", lgVerdict(results)))
}

// lgSummary is the plain one-liner alt+r flashes.
func lgSummary(expr string) string {
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return "empty expression"
	}
	ast, vars, err := lgBuild(expr)
	if err != nil {
		return err.Error()
	}
	_, results := lgTruth(ast, vars)
	return lgVerdict(results)
}

// lgTint styles a boolean expression for the inline body: gate keywords and
// operator symbols in cyan, parentheses dim, variables/constants plain. On a lex
// failure it returns the text unstyled.
func lgTint(s string) string {
	th := editor.NodeTheme()
	const ops = "&|^!~∧∨⊕¬→↔⇒⇔-><="
	kw := map[string]bool{
		"and": true, "nand": true, "or": true, "nor": true, "xor": true,
		"xnor": true, "not": true, "implies": true, "iff": true,
		"true": true, "false": true,
	}
	r := []rune(s)
	var b strings.Builder
	for i := 0; i < len(r); {
		c := r[i]
		switch {
		case c == '(' || c == ')':
			b.WriteString(th.Dim + string(c) + th.Reset)
			i++
		case lgLetter(c) || c == '_':
			j := i
			for j < len(r) && lgWord(r[j]) {
				j++
			}
			w := string(r[i:j])
			i = j
			if kw[strings.ToLower(w)] {
				b.WriteString(th.Cyan + w + th.Reset)
			} else {
				b.WriteString(w)
			}
		case strings.ContainsRune(ops, c):
			j := i
			for j < len(r) && strings.ContainsRune(ops, r[j]) {
				j++
			}
			b.WriteString(th.Cyan + string(r[i:j]) + th.Reset)
			i = j
		default:
			b.WriteRune(c)
			i++
		}
	}
	return b.String()
}

// ── plugin hooks ────────────────────────────────────────────────────────────

func logicRender(h editor.NodeHost, n editor.NodeRef) string {
	th := editor.NodeTheme()
	if strings.TrimSpace(n.Text()) == "" {
		return th.Dim + "logic · a boolean expression, e.g. (A and B) or not C — alt+e for the AST" + th.Reset
	}
	return lgTint(n.Text())
}

func logicRun(h editor.NodeHost, n editor.NodeRef) tea.Cmd {
	h.NodeFlash("∧ " + lgSummary(n.Text()) + " · alt+e for the AST")
	return nil
}

func logicToContext(h editor.NodeHost, n editor.NodeRef) (string, string, string) {
	expr := strings.TrimSpace(n.Text())
	attrs := ""
	if _, _, err := lgBuild(expr); err == nil {
		attrs = `verdict="` + strings.SplitN(lgSummary(expr), " ", 2)[0] + `"`
	}
	return "logic", attrs, expr
}

// ── the read-only expanded view (alt+e) ─────────────────────────────────────

// logicView renders the AST + truth table as scrollable bands. It is read-only —
// the expression is edited inline — so Key only scrolls. The rendered lines are
// cached in the node store on Enter (deterministic; the cache just keeps
// Lines/Bands consistent and cheap) and cleared on Leave.
type logicView struct{}

func lgOut(h editor.NodeHost, n editor.NodeRef) []string {
	d := h.NodeStore(n.UUID())
	if out, ok := d["lgOut"].([]string); ok {
		return out
	}
	out := lgCompute(n.Text())
	d["lgOut"] = out
	return out
}

func (logicView) Enter(h editor.NodeHost, n editor.NodeRef) bool {
	if strings.TrimSpace(n.Text()) == "" {
		return false
	}
	h.NodeStore(n.UUID())["lgOut"] = lgCompute(n.Text())
	return true
}

func (logicView) Leave(h editor.NodeHost, n editor.NodeRef) {
	delete(h.NodeStore(n.UUID()), "lgOut")
}

func (logicView) Lines(h editor.NodeHost, n editor.NodeRef, width int) int {
	return 1 + len(lgOut(h, n)) // the chrome header + the output
}

func (logicView) Key(h editor.NodeHost, n editor.NodeRef, k tea.KeyMsg) (tea.Cmd, bool) {
	switch k.String() {
	case "up", "k":
		h.NodeScroll(-1)
		return nil, true
	case "down", "j":
		h.NodeScroll(1)
		return nil, true
	case "pgup":
		h.NodeScroll(-8)
		return nil, true
	case "pgdown":
		h.NodeScroll(8)
		return nil, true
	}
	return nil, false // esc / alt+e / others fall through to central handling
}

func (logicView) Bands(h editor.NodeHost, n editor.NodeRef, rail string, width, scroll, winH int, focused bool) []string {
	th := editor.NodeTheme()
	content := []string{th.Dim + "∧ logic · ↑↓ scroll · esc closes" + th.Reset}
	content = append(content, lgOut(h, n)...)
	rendered := make([]string, len(content))
	for i, l := range content {
		rendered[i] = editor.NodeClip(rail+th.Reset+"  "+l, width)
	}
	return editor.NodeWindowBands(rendered, scroll, winH)
}
