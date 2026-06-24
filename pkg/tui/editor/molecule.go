package editor

import (
	"fmt"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// molecule.go is the self-contained "molecule" node type: the node text is a
// SMILES or SELFIES string (auto-detected), and alt+e opens an inline 2D
// node-link viewer — atoms are circles/labels, bonds are lines — rendered as
// bands beneath the node (never a separate screen, per the alt-screen invariant).
// Nothing here is persisted beyond it.name: the parsed graph, the force-directed
// 2D layout and the rasterized canvas all live in the ephemeral per-node store
// and are recomputed on demand.

// ── chemical model ─────────────────────────────────────────────────────────

type molAtom struct {
	sym  string // element symbol, e.g. "C", "O", "Cl"
	arom bool   // aromatic (lowercase SMILES atom)
}

type molBond struct {
	a, b  int  // atom indices
	order int  // 1 single, 2 double, 3 triple, 4 quadruple
	arom  bool // aromatic bond
}

type molGraph struct {
	atoms  []molAtom
	bonds  []molBond
	format string // "SMILES" or "SELFIES"
}

func (g *molGraph) addAtom(sym string, arom bool) int {
	g.atoms = append(g.atoms, molAtom{sym: sym, arom: arom})
	return len(g.atoms) - 1
}

// parseMolecule auto-detects the notation and dispatches. A string made only of
// bracket tokens (e.g. "[C][C][O]") is treated as SELFIES; anything else is
// SMILES.
func parseMolecule(s string) (*molGraph, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, fmt.Errorf("empty")
	}
	if looksLikeSELFIES(s) {
		return parseSELFIES(s)
	}
	return parseSMILES(s)
}

// looksLikeSELFIES reports whether every token is bracketed with nothing in
// between, e.g. "[C][=C][Ring1][=Branch1]".
func looksLikeSELFIES(s string) bool {
	if !strings.HasPrefix(s, "[") || !strings.HasSuffix(s, "]") {
		return false
	}
	depth := 0
	for _, r := range s {
		switch r {
		case '[':
			if depth != 0 {
				return false
			}
			depth = 1
		case ']':
			if depth != 1 {
				return false
			}
			depth = 0
		default:
			if depth == 0 {
				return false // a bare char between tokens → SMILES
			}
		}
	}
	return depth == 0
}

// ── SMILES ─────────────────────────────────────────────────────────────────

// parseSMILES parses the organic-subset SMILES grammar: bare/bracketed atoms,
// bonds (- = # $ : / \), branches ( ), ring-closure digits and %nn, and the
// disconnection dot. Isotopes, charges, explicit H counts and stereochemistry
// are tolerated but ignored — only connectivity matters for the 2D view.
func parseSMILES(s string) (*molGraph, error) {
	g := &molGraph{format: "SMILES"}
	r := []rune(s)
	var stack []int // branch points (atom index to return to)
	prev := -1
	pendingOrder := 0 // explicit bond order before the next atom; 0 = default
	pendingArom := false

	type ringOpen struct {
		atom, order int
		arom        bool
	}
	rings := map[int]ringOpen{}

	bond := func(a int, atomArom bool) {
		if prev < 0 {
			pendingOrder, pendingArom = 0, false
			return
		}
		order := pendingOrder
		arom := pendingArom
		if order == 0 {
			if atomArom && g.atoms[prev].arom {
				arom = true
			}
			order = 1
		}
		g.bonds = append(g.bonds, molBond{a: prev, b: a, order: order, arom: arom})
		pendingOrder, pendingArom = 0, false
	}

	ringClose := func(label int) {
		if op, ok := rings[label]; ok {
			delete(rings, label)
			if op.atom == prev {
				return
			}
			order := pendingOrder
			arom := pendingArom
			if order == 0 {
				order = op.order
			}
			if op.order != 0 && order == 0 {
				order = op.order
			}
			if order == 0 {
				if g.atoms[op.atom].arom && g.atoms[prev].arom {
					arom = true
				}
				order = 1
			}
			g.bonds = append(g.bonds, molBond{a: op.atom, b: prev, order: order, arom: arom})
			pendingOrder, pendingArom = 0, false
			return
		}
		rings[label] = ringOpen{atom: prev, order: pendingOrder, arom: pendingArom}
		pendingOrder, pendingArom = 0, false
	}

	i := 0
	for i < len(r) {
		c := r[i]
		switch {
		case c == '(':
			stack = append(stack, prev)
			i++
		case c == ')':
			if len(stack) > 0 {
				prev = stack[len(stack)-1]
				stack = stack[:len(stack)-1]
			}
			i++
		case c == '-':
			pendingOrder = 1
			i++
		case c == '=':
			pendingOrder = 2
			i++
		case c == '#':
			pendingOrder = 3
			i++
		case c == '$':
			pendingOrder = 4
			i++
		case c == ':':
			pendingOrder, pendingArom = 1, true
			i++
		case c == '/' || c == '\\':
			pendingOrder = 1
			i++
		case c == '.':
			prev, pendingOrder, pendingArom = -1, 0, false
			i++
		case c == '[':
			j := i + 1
			for j < len(r) && r[j] != ']' {
				j++
			}
			if j >= len(r) {
				return nil, fmt.Errorf("unclosed '[' in SMILES")
			}
			sym, arom := parseBracketAtom(string(r[i+1 : j]))
			a := g.addAtom(sym, arom)
			bond(a, arom)
			prev = a
			i = j + 1
		case c >= '0' && c <= '9':
			ringClose(int(c - '0'))
			i++
		case c == '%':
			if i+2 < len(r) && r[i+1] >= '0' && r[i+1] <= '9' && r[i+2] >= '0' && r[i+2] <= '9' {
				ringClose(int(r[i+1]-'0')*10 + int(r[i+2]-'0'))
				i += 3
			} else {
				i++
			}
		default:
			sym, arom, adv := parseOrganicAtom(r, i)
			if adv == 0 {
				i++ // unknown char (whitespace, stray) — skip
				continue
			}
			a := g.addAtom(sym, arom)
			bond(a, arom)
			prev = a
			i += adv
		}
	}
	if len(g.atoms) == 0 {
		return nil, fmt.Errorf("no atoms parsed")
	}
	return g, nil
}

// parseOrganicAtom reads a bare (non-bracket) organic-subset atom at r[i],
// returning the element symbol, whether it is aromatic, and how many runes it
// consumed (0 if r[i] is not an atom).
func parseOrganicAtom(r []rune, i int) (sym string, arom bool, adv int) {
	// two-letter organic atoms
	if i+1 < len(r) {
		switch string(r[i : i+2]) {
		case "Cl":
			return "Cl", false, 2
		case "Br":
			return "Br", false, 2
		}
	}
	switch r[i] {
	case 'B', 'C', 'N', 'O', 'P', 'S', 'F', 'I':
		return string(r[i]), false, 1
	case 'b', 'c', 'n', 'o', 'p', 's':
		return strings.ToUpper(string(r[i])), true, 1
	case '*':
		return "*", false, 1
	}
	return "", false, 0
}

// parseBracketAtom extracts the element symbol (and aromaticity) from the inside
// of a SMILES bracket atom, ignoring isotope, H-count, charge and stereo.
func parseBracketAtom(inner string) (sym string, arom bool) {
	r := []rune(inner)
	i := 0
	for i < len(r) && r[i] >= '0' && r[i] <= '9' { // isotope
		i++
	}
	if i >= len(r) {
		return "C", false
	}
	switch {
	case r[i] >= 'a' && r[i] <= 'z': // aromatic, single-letter element
		return strings.ToUpper(string(r[i])), true
	case r[i] >= 'A' && r[i] <= 'Z':
		j := i + 1
		for j < len(r) && r[j] >= 'a' && r[j] <= 'z' { // two-letter element (Cl, Na, Si…)
			j++
		}
		return string(r[i:j]), false
	}
	return "C", false
}

// ── SELFIES ──────────────────────────────────────────────────────────────

// selfiesIndexAlphabet is the canonical index alphabet (base-16) used to decode
// the length index that follows a Branch/Ring symbol. Tokens are stored without
// their surrounding brackets.
var selfiesIndexAlphabet = []string{
	"C", "Ring1", "Ring2", "Branch1", "=Branch1", "#Branch1",
	"Branch2", "=Branch2", "#Branch2", "O", "N", "=N", "=C", "#C", "S", "P",
}

var selfiesIndexCode = func() map[string]int {
	m := make(map[string]int, len(selfiesIndexAlphabet))
	for i, s := range selfiesIndexAlphabet {
		m[s] = i
	}
	return m
}()

// parseSELFIES decodes a SELFIES string into a graph. Atoms, bond prefixes
// (= #), Branch1-3 and Ring1-3 are supported; valence/state capping is
// intentionally not modeled (connectivity only), so well-formed strings decode
// to the right skeleton.
func parseSELFIES(s string) (*molGraph, error) {
	toks := tokenizeSELFIES(s)
	if len(toks) == 0 {
		return nil, fmt.Errorf("no SELFIES tokens")
	}
	g := &molGraph{format: "SELFIES"}

	var derive func(toks []string, prev, incoming int)
	derive = func(toks []string, prev, incoming int) {
		first := true
		idx := 0
		for idx < len(toks) {
			t := toks[idx]
			switch {
			case isSelfiesBranch(t):
				bt, L := branchInfo(t)
				if prev < 0 || idx+L >= len(toks) {
					idx++
					continue
				}
				q := selfiesIndex(toks[idx+1 : idx+1+L])
				bodyStart := idx + 1 + L
				bodyLen := q + 1
				if bodyStart+bodyLen > len(toks) {
					bodyLen = len(toks) - bodyStart
				}
				if bodyLen > 0 {
					derive(toks[bodyStart:bodyStart+bodyLen], prev, bt)
				}
				idx = bodyStart + bodyLen
				first = false
			case isSelfiesRing(t):
				rt, L := ringTypeInfo(t)
				if prev < 0 || idx+L >= len(toks) {
					idx++
					continue
				}
				q := selfiesIndex(toks[idx+1 : idx+1+L])
				target := prev - (q + 1)
				if target >= 0 && target < len(g.atoms) && target != prev {
					g.bonds = append(g.bonds, molBond{a: target, b: prev, order: rt})
				}
				idx = idx + 1 + L
				first = false
			case t == "nop" || t == "":
				idx++
			default:
				order, sym, arom := atomToken(t)
				a := g.addAtom(sym, arom)
				if prev >= 0 {
					o := order
					if first && incoming > o {
						o = incoming
					}
					g.bonds = append(g.bonds, molBond{a: prev, b: a, order: o, arom: arom && g.atoms[prev].arom})
				}
				prev = a
				first = false
				idx++
			}
		}
	}

	derive(toks, -1, 0)
	if len(g.atoms) == 0 {
		return nil, fmt.Errorf("no atoms parsed")
	}
	return g, nil
}

// tokenizeSELFIES splits "[A][B][C]" into ["A","B","C"] (brackets stripped).
func tokenizeSELFIES(s string) []string {
	s = strings.TrimSpace(s)
	var out []string
	for len(s) > 0 {
		if s[0] != '[' {
			break
		}
		end := strings.IndexByte(s, ']')
		if end < 0 {
			break
		}
		out = append(out, s[1:end])
		s = s[end+1:]
	}
	return out
}

func isSelfiesBranch(t string) bool {
	t = strings.TrimLeft(t, "=#/\\")
	return strings.HasPrefix(t, "Branch")
}

func isSelfiesRing(t string) bool {
	t = strings.TrimLeft(t, "=#/\\")
	return strings.HasPrefix(t, "Ring")
}

// branchInfo returns the branch bond order and the number L of index symbols
// that follow (Branch1→1, Branch2→2, Branch3→3).
func branchInfo(t string) (order, L int) {
	order, rest := bondPrefix(t)
	L = lastDigit(rest)
	return order, L
}

func ringTypeInfo(t string) (order, L int) {
	order, rest := bondPrefix(t)
	L = lastDigit(rest)
	return order, L
}

func bondPrefix(t string) (order int, rest string) {
	switch {
	case strings.HasPrefix(t, "="):
		return 2, t[1:]
	case strings.HasPrefix(t, "#"):
		return 3, t[1:]
	case strings.HasPrefix(t, "/"), strings.HasPrefix(t, "\\"):
		return 1, t[1:]
	}
	return 1, t
}

func lastDigit(s string) int {
	if s == "" {
		return 1
	}
	c := s[len(s)-1]
	if c >= '1' && c <= '9' {
		return int(c - '0')
	}
	return 1
}

// selfiesIndex decodes the base-16 length index encoded by the given symbols.
func selfiesIndex(syms []string) int {
	base := len(selfiesIndexAlphabet)
	q := 0
	for _, s := range syms {
		q = q*base + selfiesIndexCode[s] // unknown → 0
	}
	return q
}

// atomToken parses a SELFIES atom token like "C", "=C", "#N", "O".
func atomToken(t string) (order int, sym string, arom bool) {
	order, rest := bondPrefix(t)
	sym, arom = parseBracketAtom(rest)
	return order, sym, arom
}

// ── molecular formula (best-effort) ─────────────────────────────────────────

// standardValence is the typical neutral valence used to estimate implicit
// hydrogens for the organic subset; 0 means "don't add H".
var standardValence = map[string]int{
	"B": 3, "C": 4, "N": 3, "O": 2, "P": 3, "S": 2,
	"F": 1, "Cl": 1, "Br": 1, "I": 1,
}

// formula returns a Hill-system molecular formula (with estimated implicit H),
// e.g. "C2H6O" for ethanol. Best-effort: charges/hypervalence are not modeled.
func (g *molGraph) formula() string {
	counts := map[string]int{}
	bondSum := make([]int, len(g.atoms))
	aromAtom := make([]bool, len(g.atoms))
	for _, b := range g.bonds {
		bondSum[b.a] += b.order
		bondSum[b.b] += b.order
		if b.arom {
			aromAtom[b.a] = true
			aromAtom[b.b] = true
		}
	}
	h := 0
	for i, a := range g.atoms {
		counts[a.sym]++
		if v, ok := standardValence[a.sym]; ok {
			used := bondSum[i]
			if a.arom || aromAtom[i] {
				used++ // delocalized bonds count as ~1.5; nudge so benzene reads CH
			}
			if free := v - used; free > 0 {
				h += free
			}
		}
	}
	if h > 0 {
		counts["H"] = h
	}
	return hillFormula(counts)
}

func hillFormula(counts map[string]int) string {
	var b strings.Builder
	emit := func(sym string) {
		n := counts[sym]
		if n == 0 {
			return
		}
		b.WriteString(sym)
		if n > 1 {
			fmt.Fprintf(&b, "%d", n)
		}
		delete(counts, sym)
	}
	if counts["C"] > 0 {
		emit("C")
		emit("H")
	}
	rest := make([]string, 0, len(counts))
	for s := range counts {
		rest = append(rest, s)
	}
	sort.Strings(rest)
	for _, s := range rest {
		emit(s)
	}
	return b.String()
}

// ── inline 2D viewer (alt+e) ────────────────────────────────────────────────

func moleculeGlyph(it *item) (string, string) { return "⬡", cAccent }

// atomicWeight is the standard atomic weight for the common organic-subset
// elements, used for the best-effort MW readout in the info bar.
var atomicWeight = map[string]float64{
	"H": 1.008, "B": 10.81, "C": 12.011, "N": 14.007, "O": 15.999,
	"F": 18.998, "P": 30.974, "S": 32.06, "Cl": 35.45, "Br": 79.904, "I": 126.90,
}

// weight estimates the molecular weight (heavy atoms + implicit H from formula).
func (g *molGraph) weight() float64 {
	mw := 0.0
	for _, a := range g.atoms {
		mw += atomicWeight[a.sym]
	}
	// add implicit hydrogens parsed back out of the Hill formula.
	if f := g.formula(); strings.Contains(f, "H") {
		mw += float64(hydrogenCount(f)) * atomicWeight["H"]
	}
	return mw
}

// hydrogenCount reads the H multiplicity out of a Hill formula like "C2H6O".
func hydrogenCount(formula string) int {
	r := []rune(formula)
	for i := 0; i < len(r); i++ {
		if r[i] == 'H' {
			j := i + 1
			num := 0
			for j < len(r) && r[j] >= '0' && r[j] <= '9' {
				num = num*10 + int(r[j]-'0')
				j++
			}
			if j == i+1 {
				return 1 // bare "H" means one
			}
			return num
		}
	}
	return 0
}

// moleculeView is the molecule node's inline expanded view: a full-width framed
// panel — muted-gray top/bottom borders and a divider, an info bar, and a
// glyph-colored depth-shaded 2D node-link drawing — rendered as bands beneath
// the node (never a separate screen). Read-only; state is cached ephemerally.
type moleculeView struct{}

// state returns the info-bar text and the (uncached → cached) canvas lines for
// the given interior width, recomputing only when the text or width changes.
func (moleculeView) viewIndex(m *Model, it *item) int {
	vi, _ := m.nodeStore(it.uuid)["molView"].(int)
	n := molViewCount()
	return ((vi % n) + n) % n
}

func (v moleculeView) state(m *Model, it *item, innerW int) (string, []string) {
	d := m.nodeStore(it.uuid)
	vi := v.viewIndex(m, it)
	key := fmt.Sprintf("%d|%d|%s", vi, innerW, it.name)
	if d["molKey"] == key {
		info, _ := d["molInfo"].(string)
		lines, _ := d["molLines"].([]string)
		return info, lines
	}
	var info string
	var lines []string
	g, err := parseMolecule(it.name)
	if err != nil {
		info = "molecule · cannot parse · esc close"
		lines = []string{cRed + "  " + err.Error() + cReset}
	} else {
		name, rendered := molViewAt(vi, g, innerW)
		lines = rendered
		info = fmt.Sprintf("molecule · %s · %s · MW %.2f · %d atoms · view: %s [%d/%d] · tab switch · esc",
			g.format, g.formula(), g.weight(), len(g.atoms), name, vi+1, molViewCount())
	}
	d["molKey"] = key
	d["molInfo"] = info
	d["molLines"] = lines
	return info, lines
}

func (v moleculeView) Enter(m *Model, it *item) bool {
	return strings.TrimSpace(it.name) != ""
}

func (v moleculeView) Leave(m *Model, it *item) {
	d := m.nodeStore(it.uuid)
	delete(d, "molKey")
	delete(d, "molInfo")
	delete(d, "molLines")
	delete(d, "molTotal")
}

func (v moleculeView) Lines(m *Model, it *item, width int) int {
	if t, ok := m.nodeStore(it.uuid)["molTotal"].(int); ok {
		return t
	}
	_, lines := v.state(m, it, width-2)
	return molChrome + len(lines)
}

func (v moleculeView) cycle(m *Model, it *item, delta int) {
	d := m.nodeStore(it.uuid)
	vi, _ := d["molView"].(int)
	d["molView"] = vi + delta
	delete(d, "molKey") // force a re-render in the new view
	m.focusScroll = 0
}

func (v moleculeView) Key(m *Model, it *item, k tea.KeyMsg) (tea.Cmd, bool) {
	switch k.String() {
	case "down", "j":
		m.focusScroll++
		return nil, true
	case "up", "k":
		if m.focusScroll > 0 {
			m.focusScroll--
		}
		return nil, true
	case "tab", "right", "l":
		v.cycle(m, it, +1)
		return nil, true
	case "shift+tab", "left", "h":
		v.cycle(m, it, -1)
		return nil, true
	}
	return nil, false // esc / ctrl+c handled centrally
}

// molChrome is the fixed band count around the scrollable canvas: top border,
// info bar, divider, bottom border.
const molChrome = 4

// grayRule is a full-width muted-gray horizontal line (border / divider).
func grayRule(rail string, width int) string {
	n := width - visibleWidth(rail)
	if n < 1 {
		n = 1
	}
	return rail + cReset + cDim + strings.Repeat("─", n) + cReset
}

func (v moleculeView) Bands(m *Model, it *item, rail string, width, scroll, winH int, focused bool) []string {
	innerW := width - visibleWidth(rail)
	if innerW < 10 {
		innerW = width
	}
	info, canvas := v.state(m, it, innerW)
	m.nodeStore(it.uuid)["molTotal"] = molChrome + len(canvas)

	inner := winH - molChrome
	if inner < 1 {
		inner = 1
	}
	if scroll > len(canvas)-inner {
		scroll = len(canvas) - inner
	}
	if scroll < 0 {
		scroll = 0
	}
	if focused {
		m.focusScroll = scroll
	}
	end := scroll + inner
	if end > len(canvas) {
		end = len(canvas)
	}

	out := []string{grayRule(rail, width)}                           // top border
	out = append(out, clip(rail+cReset+cDim+" "+info+cReset, width)) // info bar
	out = append(out, grayRule(rail, width))                         // divider through
	for _, line := range canvas[scroll:end] {
		out = append(out, clip(rail+cReset+line, width))
	}
	out = append(out, grayRule(rail, width)) // bottom border
	return out
}
