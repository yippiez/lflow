package nodes

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lflow/lflow/pkg/tui/database"
	"github.com/lflow/lflow/pkg/tui/editor"
)

// The Formal System node: a theoretical-CS sandbox. The node text is a ONE-LINE
// spec whose first word picks an engine and whose rest is space-separated
// key:value options (logic takes a raw expression instead). alt+e opens the
// read-only expanded view, which runs the system deterministically and renders
// it beneath the node; alt+r flashes a one-line summary. Nothing is persisted or
// synced — the spec IS the program, the output recomputes from it, so the node
// stays a plain inline-editable line that greps and syncs as text.
//
// Four engines, all pure and deterministic (no agent, no CLI, no randomness):
//
//	ca      rule:110 gens:24 width:49 init:single|0001000   elementary CA (Wolfram)
//	life    pattern:glider gens:6 w:18 h:12                  Conway's Game of Life (toroidal)
//	lsystem axiom:A rules:A=AB,B=A gens:5                    Lindenmayer string rewriting
//	logic   (A and B) or not C                               propositional truth table
//
// The engines (fsCA, fsLifeStep, fsLSystem, fsLogicEval) are plain data → data
// functions with no styling, so they test directly; the fs*Render helpers wrap
// them in the display lines the view shows.

// fsOn / fsOff are a grid cell's live / dead glyphs (plain Unicode block, per the
// no-emoji invariant).
const (
	fsOn  = "█"
	fsOff = " "
)

func init() {
	editor.RegisterNodePlugin(editor.NodePlugin{
		Key: database.TypeFormalSystem, Label: "Formal System",
		InlineEditable: true, // the spec line is edited inline like any bullet
		Glyph:          func() (string, string) { return "⊢", editor.NodeTheme().Cyan },
		Render:         fsRender,
		Run:            fsRun,
		View:           fsView{},
		ToContext:      fsToContext,
	})
}

// fsRender is the inline body: the spec with its engine keyword in cyan, or a dim
// hint when empty.
func fsRender(h editor.NodeHost, n editor.NodeRef) string {
	th := editor.NodeTheme()
	spec := strings.TrimSpace(n.Text())
	if spec == "" {
		return th.Dim + "formal system · ca · life · lsystem · logic — alt+e runs it" + th.Reset
	}
	engine, rest, _ := strings.Cut(spec, " ")
	body := th.Cyan + engine + th.Reset
	if rest != "" {
		body += " " + rest
	}
	return body
}

// fsRun (alt+r) flashes a one-line summary; the full render lives in alt+e.
func fsRun(h editor.NodeHost, n editor.NodeRef) tea.Cmd {
	h.NodeFlash("⊢ " + fsSummary(n.Text()) + " · alt+e to view")
	return nil
}

// fsToContext hands an agent the spec and which engine it drives.
func fsToContext(h editor.NodeHost, n editor.NodeRef) (string, string, string) {
	engine, _, _ := strings.Cut(strings.TrimSpace(n.Text()), " ")
	attrs := ""
	if engine != "" {
		attrs = `engine="` + strings.ToLower(engine) + `"`
	}
	return "formalsystem", attrs, strings.TrimSpace(n.Text())
}

// ── the spec parser ─────────────────────────────────────────────────────────

// fsArgs parses space-separated key:value options into a map (later keys win).
func fsArgs(remainder string) map[string]string {
	m := map[string]string{}
	for _, f := range strings.Fields(remainder) {
		if k, v, ok := strings.Cut(f, ":"); ok {
			m[strings.ToLower(k)] = v
		}
	}
	return m
}

func fsInt(a map[string]string, key string, def int) int {
	if v, ok := a[key]; ok {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func fsStr(a map[string]string, key, def string) string {
	if v, ok := a[key]; ok && v != "" {
		return v
	}
	return def
}

func fsClamp(n, lo, hi int) int {
	if n < lo {
		return lo
	}
	if n > hi {
		return hi
	}
	return n
}

// fsCompute runs the spec and returns the display lines (already styled) the view
// shows. It never errors — bad input renders an error line instead.
func fsCompute(spec string) []string {
	engine, rest, _ := strings.Cut(strings.TrimSpace(spec), " ")
	switch strings.ToLower(engine) {
	case "":
		return []string{fsErr("empty — try: ca rule:110 · life pattern:glider · lsystem axiom:A rules:A=AB,B=A · logic (A and B) or not C")}
	case "ca":
		return fsCARender(fsArgs(rest))
	case "life":
		return fsLifeRender(fsArgs(rest))
	case "lsystem", "ls":
		return fsLSystemRender(fsArgs(rest))
	case "logic", "truth":
		return fsLogicRender(strings.TrimSpace(rest))
	default:
		return []string{fsErr("unknown system %q — use ca, life, lsystem, or logic", engine)}
	}
}

// fsSummary is the plain (unstyled) one-liner alt+r flashes.
func fsSummary(spec string) string {
	engine, rest, _ := strings.Cut(strings.TrimSpace(spec), " ")
	switch strings.ToLower(engine) {
	case "ca":
		a := fsArgs(rest)
		return fmt.Sprintf("cellular automaton · rule %d", fsInt(a, "rule", 110))
	case "life":
		return "game of life · " + fsStr(fsArgs(rest), "pattern", "glider")
	case "lsystem", "ls":
		return "L-system · axiom " + fsStr(fsArgs(rest), "axiom", "A")
	case "logic", "truth":
		return "truth table · " + strings.TrimSpace(rest)
	case "":
		return "empty spec"
	default:
		return "unknown system " + engine
	}
}

// fsHead / fsSub / fsErr are the styled header, sub-header and error lines; grid
// and table rows stay plain so they read as the raw structure.
func fsHead(format string, a ...any) string {
	th := editor.NodeTheme()
	return th.Cyan + fmt.Sprintf(format, a...) + th.Reset
}

func fsSub(format string, a ...any) string {
	th := editor.NodeTheme()
	return th.Dim + fmt.Sprintf(format, a...) + th.Reset
}

func fsErr(format string, a ...any) string {
	th := editor.NodeTheme()
	return th.Red + fmt.Sprintf(format, a...) + th.Reset
}

// fsGridRow renders one row of a boolean grid, trailing dead cells trimmed.
func fsGridRow(row []bool) string {
	var b strings.Builder
	for _, c := range row {
		if c {
			b.WriteString(fsOn)
		} else {
			b.WriteString(fsOff)
		}
	}
	return strings.TrimRight(b.String(), fsOff)
}

// ── engine: elementary cellular automaton ───────────────────────────────────

// fsCA runs a Wolfram elementary CA for gens rows on a fixed-width strip (dead
// boundaries). rule is the 8-bit rule number; init is "single" (one live cell,
// centered) or a binary string laid at the left. It returns one row per
// generation, oldest first.
func fsCA(rule, gens, width int, init string) [][]bool {
	if width < 1 {
		width = 1
	}
	row := make([]bool, width)
	if init == "" || init == "single" {
		row[width/2] = true
	} else {
		for i := 0; i < width && i < len(init); i++ {
			row[i] = init[i] == '1'
		}
	}
	grid := make([][]bool, 0, gens)
	for g := 0; g < gens; g++ {
		grid = append(grid, row)
		next := make([]bool, width)
		for i := 0; i < width; i++ {
			var idx int
			if i > 0 && row[i-1] {
				idx |= 4
			}
			if row[i] {
				idx |= 2
			}
			if i < width-1 && row[i+1] {
				idx |= 1
			}
			next[i] = rule&(1<<idx) != 0
		}
		row = next
	}
	return grid
}

func fsCARender(a map[string]string) []string {
	rule := fsInt(a, "rule", 110)
	if rule < 0 || rule > 255 {
		return []string{fsErr("rule must be 0..255, got %d", rule)}
	}
	gens := fsClamp(fsInt(a, "gens", 24), 1, 200)
	width := fsInt(a, "width", 0)
	if width == 0 {
		width = 2*gens + 1 // the growth cone fits without clipping
	}
	width = fsClamp(width, 1, 200)
	out := []string{fsHead("cellular automaton · rule %d · %d generations · width %d", rule, gens, width)}
	for _, row := range fsCA(rule, gens, width, a["init"]) {
		out = append(out, fsGridRow(row))
	}
	return out
}

// ── engine: Conway's Game of Life ───────────────────────────────────────────

// fsLifePattern seeds a w×h grid with a named starting pattern near the top-left,
// or returns nil for an unknown name.
func fsLifePattern(name string, w, h int) [][]bool {
	g := make([][]bool, h)
	for i := range g {
		g[i] = make([]bool, w)
	}
	set := func(coords ...[2]int) [][]bool {
		for _, c := range coords {
			x, y := c[0]+1, c[1]+1 // small offset from the edge
			if x >= 0 && x < w && y >= 0 && y < h {
				g[y][x] = true
			}
		}
		return g
	}
	switch strings.ToLower(name) {
	case "block":
		return set([2]int{0, 0}, [2]int{1, 0}, [2]int{0, 1}, [2]int{1, 1})
	case "blinker":
		return set([2]int{0, 1}, [2]int{1, 1}, [2]int{2, 1})
	case "toad":
		return set([2]int{1, 0}, [2]int{2, 0}, [2]int{3, 0}, [2]int{0, 1}, [2]int{1, 1}, [2]int{2, 1})
	case "beacon":
		return set([2]int{0, 0}, [2]int{1, 0}, [2]int{0, 1}, [2]int{1, 1},
			[2]int{2, 2}, [2]int{3, 2}, [2]int{2, 3}, [2]int{3, 3})
	case "glider", "":
		return set([2]int{1, 0}, [2]int{2, 1}, [2]int{0, 2}, [2]int{1, 2}, [2]int{2, 2})
	}
	return nil
}

// fsLifeStep advances one Life generation on a toroidal grid (edges wrap).
func fsLifeStep(g [][]bool) [][]bool {
	h := len(g)
	if h == 0 {
		return g
	}
	w := len(g[0])
	next := make([][]bool, h)
	for y := range next {
		next[y] = make([]bool, w)
	}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			n := 0
			for dy := -1; dy <= 1; dy++ {
				for dx := -1; dx <= 1; dx++ {
					if dx == 0 && dy == 0 {
						continue
					}
					if g[(y+dy+h)%h][(x+dx+w)%w] {
						n++
					}
				}
			}
			next[y][x] = n == 3 || (g[y][x] && n == 2)
		}
	}
	return next
}

func fsLifeRender(a map[string]string) []string {
	pat := fsStr(a, "pattern", "glider")
	gens := fsClamp(fsInt(a, "gens", 6), 0, 60)
	w := fsClamp(fsInt(a, "w", 18), 3, 120)
	h := fsClamp(fsInt(a, "h", 12), 3, 60)
	g := fsLifePattern(pat, w, h)
	if g == nil {
		return []string{fsErr("unknown pattern %q — block, blinker, glider, toad, beacon", pat)}
	}
	out := []string{fsHead("game of life · %s · %d generations · %dx%d torus", pat, gens, w, h)}
	for gen := 0; gen <= gens; gen++ {
		out = append(out, fsSub("gen %d", gen))
		for _, row := range g {
			out = append(out, fsGridRow(row))
		}
		if gen < gens {
			g = fsLifeStep(g)
		}
	}
	return out
}

// ── engine: L-system (Lindenmayer string rewriting) ─────────────────────────

// fsLSMax caps a generation's length so a runaway production can't blow up.
const fsLSMax = 8192

// fsLSystem rewrites axiom for gens generations under the production rules (each
// symbol → its replacement, unmatched symbols pass through), returning every
// generation including the axiom. It stops early once a generation exceeds
// fsLSMax runes.
func fsLSystem(axiom string, rules map[rune]string, gens int) []string {
	cur := axiom
	out := []string{cur}
	for g := 0; g < gens; g++ {
		var b strings.Builder
		for _, r := range cur {
			if rep, ok := rules[r]; ok {
				b.WriteString(rep)
			} else {
				b.WriteRune(r)
			}
		}
		cur = b.String()
		out = append(out, cur)
		if len([]rune(cur)) > fsLSMax {
			break
		}
	}
	return out
}

// fsParseRules parses "A=AB,B=A" into {'A':"AB", 'B':"A"}. Each LHS is one rune.
func fsParseRules(spec string) (map[rune]string, error) {
	rules := map[rune]string{}
	if strings.TrimSpace(spec) == "" {
		return rules, nil
	}
	for _, part := range strings.Split(spec, ",") {
		lhs, rhs, ok := strings.Cut(part, "=")
		lr := []rune(lhs)
		if !ok || len(lr) != 1 {
			return nil, fmt.Errorf("bad rule %q — want SYMBOL=REPLACEMENT", part)
		}
		rules[lr[0]] = rhs
	}
	return rules, nil
}

// fsRulesStr renders a rule set back to "A→AB  B→A" in sorted order.
func fsRulesStr(rules map[rune]string) string {
	keys := make([]string, 0, len(rules))
	for k := range rules {
		keys = append(keys, string(k))
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+"→"+rules[[]rune(k)[0]])
	}
	return strings.Join(parts, "  ")
}

func fsLSystemRender(a map[string]string) []string {
	axiom := fsStr(a, "axiom", "A")
	gens := fsClamp(fsInt(a, "gens", 5), 0, 40)
	rules, err := fsParseRules(a["rules"])
	if err != nil {
		return []string{fsErr("%v", err)}
	}
	if len(rules) == 0 {
		rules = map[rune]string{'A': "AB", 'B': "A"} // Lindenmayer's algae
	}
	out := []string{
		fsHead("L-system · axiom %s · %d generations", axiom, gens),
		fsSub("rules: %s", fsRulesStr(rules)),
	}
	for i, s := range fsLSystem(axiom, rules, gens) {
		disp := s
		if len([]rune(disp)) > 180 {
			disp = string([]rune(disp)[:180]) + "…"
		}
		out = append(out, fmt.Sprintf("%2d  %s  %s", i, disp, fsSub("(len %d)", len([]rune(s)))))
	}
	return out
}

// ── engine: propositional logic truth table ─────────────────────────────────

// fsLogicEval tokenizes and parses a propositional expression, then evaluates it
// over every assignment of its variables. It returns the variables (alphabetical),
// one assignment row per line (MSB = first variable), and the result per row.
// Operators: not/!/~, and/&, xor/^, or/|, -> (implies), <-> (iff); parens group.
func fsLogicEval(exprStr string) (vars []string, table [][]bool, results []bool, err error) {
	toks, err := fsLex(exprStr)
	if err != nil {
		return nil, nil, nil, err
	}
	if len(toks) == 0 {
		return nil, nil, nil, fmt.Errorf("empty expression")
	}
	seen := map[string]bool{}
	for _, t := range toks {
		if t.kind == "var" && !seen[t.text] {
			seen[t.text] = true
			vars = append(vars, t.text)
		}
	}
	sort.Strings(vars)
	if len(vars) > 6 {
		return nil, nil, nil, fmt.Errorf("too many variables (%d) — max 6", len(vars))
	}
	p := &fsParser{toks: toks}
	ast := p.parse()
	if p.err != nil {
		return nil, nil, nil, p.err
	}
	if p.pos != len(toks) {
		return nil, nil, nil, fmt.Errorf("unexpected %q", toks[p.pos].text)
	}
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
		results = append(results, ast(env))
	}
	return vars, table, results, nil
}

// fsTok is one lexer token; kind is the grammar symbol, text the source slice.
type fsTok struct{ kind, text string }

func fsIsLetter(r rune) bool {
	return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z'
}
func fsIsWord(r rune) bool {
	return fsIsLetter(r) || r >= '0' && r <= '9' || r == '_'
}

func fsLex(s string) ([]fsTok, error) {
	var toks []fsTok
	r := []rune(s)
	for i := 0; i < len(r); {
		c := r[i]
		switch {
		case c == ' ' || c == '\t':
			i++
		case c == '(':
			toks = append(toks, fsTok{"(", "("})
			i++
		case c == ')':
			toks = append(toks, fsTok{")", ")"})
			i++
		case c == '&':
			toks = append(toks, fsTok{"and", "&"})
			i++
		case c == '|':
			toks = append(toks, fsTok{"or", "|"})
			i++
		case c == '^':
			toks = append(toks, fsTok{"xor", "^"})
			i++
		case c == '!' || c == '~':
			toks = append(toks, fsTok{"not", string(c)})
			i++
		case c == '-' && i+1 < len(r) && r[i+1] == '>':
			toks = append(toks, fsTok{"imp", "->"})
			i += 2
		case c == '<' && i+2 < len(r) && r[i+1] == '-' && r[i+2] == '>':
			toks = append(toks, fsTok{"iff", "<->"})
			i += 3
		case fsIsLetter(c) || c == '_':
			j := i
			for j < len(r) && fsIsWord(r[j]) {
				j++
			}
			w := string(r[i:j])
			i = j
			switch strings.ToLower(w) {
			case "and":
				toks = append(toks, fsTok{"and", w})
			case "or":
				toks = append(toks, fsTok{"or", w})
			case "not":
				toks = append(toks, fsTok{"not", w})
			case "xor":
				toks = append(toks, fsTok{"xor", w})
			case "implies":
				toks = append(toks, fsTok{"imp", w})
			case "iff":
				toks = append(toks, fsTok{"iff", w})
			case "true":
				toks = append(toks, fsTok{"true", w})
			case "false":
				toks = append(toks, fsTok{"false", w})
			default:
				toks = append(toks, fsTok{"var", w})
			}
		default:
			return nil, fmt.Errorf("unexpected character %q", string(c))
		}
	}
	return toks, nil
}

// fsExpr is a compiled expression: an environment → truth value.
type fsExpr func(env map[string]bool) bool

// fsParser is a recursive-descent parser over the token stream. Precedence, low
// to high: iff, implies, or, xor, and, not, atom. implies and iff are
// right-associative (the usual reading of P -> Q -> R as P -> (Q -> R)).
type fsParser struct {
	toks []fsTok
	pos  int
	err  error
}

func (p *fsParser) peek() string {
	if p.pos < len(p.toks) {
		return p.toks[p.pos].kind
	}
	return ""
}

func (p *fsParser) parse() fsExpr { return p.parseIff() }

func (p *fsParser) parseIff() fsExpr {
	left := p.parseImp()
	for p.peek() == "iff" {
		p.pos++
		right := p.parseImp()
		l, r := left, right
		left = func(e map[string]bool) bool { return l(e) == r(e) }
	}
	return left
}

func (p *fsParser) parseImp() fsExpr {
	left := p.parseOr()
	if p.peek() == "imp" {
		p.pos++
		right := p.parseImp() // right-associative
		l, r := left, right
		return func(e map[string]bool) bool { return !l(e) || r(e) }
	}
	return left
}

func (p *fsParser) parseOr() fsExpr {
	left := p.parseXor()
	for p.peek() == "or" {
		p.pos++
		right := p.parseXor()
		l, r := left, right
		left = func(e map[string]bool) bool { return l(e) || r(e) }
	}
	return left
}

func (p *fsParser) parseXor() fsExpr {
	left := p.parseAnd()
	for p.peek() == "xor" {
		p.pos++
		right := p.parseAnd()
		l, r := left, right
		left = func(e map[string]bool) bool { return l(e) != r(e) }
	}
	return left
}

func (p *fsParser) parseAnd() fsExpr {
	left := p.parseNot()
	for p.peek() == "and" {
		p.pos++
		right := p.parseNot()
		l, r := left, right
		left = func(e map[string]bool) bool { return l(e) && r(e) }
	}
	return left
}

func (p *fsParser) parseNot() fsExpr {
	if p.peek() == "not" {
		p.pos++
		inner := p.parseNot()
		return func(e map[string]bool) bool { return !inner(e) }
	}
	return p.parseAtom()
}

func (p *fsParser) parseAtom() fsExpr {
	switch p.peek() {
	case "(":
		p.pos++
		inner := p.parseIff()
		if p.peek() != ")" {
			p.fail("missing )")
			return fsFalse
		}
		p.pos++
		return inner
	case "true":
		p.pos++
		return func(map[string]bool) bool { return true }
	case "false":
		p.pos++
		return fsFalse
	case "var":
		name := p.toks[p.pos].text
		p.pos++
		return func(e map[string]bool) bool { return e[name] }
	default:
		p.fail("expected a variable or (")
		return fsFalse
	}
}

func fsFalse(map[string]bool) bool { return false }

func (p *fsParser) fail(msg string) {
	if p.err == nil {
		p.err = fmt.Errorf("%s", msg)
	}
	p.pos = len(p.toks) // stop advancing
}

func fsTF(b bool) string {
	if b {
		return "T"
	}
	return "F"
}

func fsLogicRender(exprStr string) []string {
	if strings.TrimSpace(exprStr) == "" {
		return []string{fsErr("write an expression, e.g. logic (A and B) or not C")}
	}
	vars, table, results, err := fsLogicEval(exprStr)
	if err != nil {
		return []string{fsErr("%v", err)}
	}
	out := []string{fsHead("truth table · %s", exprStr)}
	if len(vars) == 0 { // a constant expression (true/false only)
		out = append(out, "result: "+fsTF(results[0]))
		return append(out, fsSub("%s", fsLogicVerdict(results)))
	}
	// column widths: each variable's column fits its name (values are 1 char)
	widths := make([]int, len(vars))
	var head strings.Builder
	for i, v := range vars {
		widths[i] = len([]rune(v))
		if widths[i] < 1 {
			widths[i] = 1
		}
		head.WriteString(fsPad(v, widths[i]) + " ")
	}
	head.WriteString("│ result")
	out = append(out, head.String())
	for i := range table {
		var line strings.Builder
		for j := range vars {
			line.WriteString(fsPad(fsTF(table[i][j]), widths[j]) + " ")
		}
		line.WriteString("│ " + fsTF(results[i]))
		out = append(out, line.String())
	}
	return append(out, fsSub("%s", fsLogicVerdict(results)))
}

// fsLogicVerdict classifies a result column: tautology, contradiction, or
// contingent (satisfiable but not valid).
func fsLogicVerdict(results []bool) string {
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

// fsPad right-pads s (by display runes) to width w.
func fsPad(s string, w int) string {
	if n := w - len([]rune(s)); n > 0 {
		return s + strings.Repeat(" ", n)
	}
	return s
}

// ── the read-only expanded view (alt+e) ─────────────────────────────────────

// fsView renders the running system as scrollable bands beneath the node. It is
// read-only — the spec is edited inline, not here — so Key only scrolls. The
// rendered lines are cached in the node store on Enter (deterministic, so the
// cache is just to keep Lines/Bands consistent and cheap) and cleared on Leave.
type fsView struct{}

func fsOut(h editor.NodeHost, n editor.NodeRef) []string {
	d := h.NodeStore(n.UUID())
	if out, ok := d["fsOut"].([]string); ok {
		return out
	}
	out := fsCompute(n.Text())
	d["fsOut"] = out
	return out
}

func (fsView) Enter(h editor.NodeHost, n editor.NodeRef) bool {
	if strings.TrimSpace(n.Text()) == "" {
		return false
	}
	h.NodeStore(n.UUID())["fsOut"] = fsCompute(n.Text())
	return true
}

func (fsView) Leave(h editor.NodeHost, n editor.NodeRef) {
	delete(h.NodeStore(n.UUID()), "fsOut")
}

func (fsView) Lines(h editor.NodeHost, n editor.NodeRef, width int) int {
	return 1 + len(fsOut(h, n)) // the chrome header + the output
}

func (fsView) Key(h editor.NodeHost, n editor.NodeRef, k tea.KeyMsg) (tea.Cmd, bool) {
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

func (fsView) Bands(h editor.NodeHost, n editor.NodeRef, rail string, width, scroll, winH int, focused bool) []string {
	th := editor.NodeTheme()
	content := []string{th.Dim + "⊢ formal system · ↑↓ scroll · esc closes" + th.Reset}
	content = append(content, fsOut(h, n)...)
	rendered := make([]string, len(content))
	for i, l := range content {
		rendered[i] = editor.NodeClip(rail+th.Reset+"  "+l, width)
	}
	return editor.NodeWindowBands(rendered, scroll, winH)
}
