package editor

import (
	"fmt"
	"math"
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

// ── 3D layout (depth → color) ────────────────────────────────────────────

type vec3 struct{ x, y, z float64 }

// layoutMolecule places atoms with a deterministic 3D Fruchterman-Reingold
// spring layout. A mild flattening force pulls atoms toward the z=0 plane, so
// small molecules stay essentially planar while large, crowded ones bulge into
// the third dimension — which the viewer renders as a darker background (depth),
// giving the "circuit board" look the deeper the trace runs.
func layoutMolecule(g *molGraph) []vec3 {
	n := len(g.atoms)
	pos := make([]vec3, n)
	if n == 0 {
		return pos
	}
	if n == 1 {
		return pos
	}

	// deterministic seed: a golden-spiral spherical shell (no RNG).
	radius := 0.6 * math.Sqrt(float64(n))
	gold := math.Pi * (3 - math.Sqrt(5))
	for i := range pos {
		off := 2.0 / float64(n)
		y := float64(i)*off - 1 + off/2
		rr := math.Sqrt(math.Max(0, 1-y*y))
		phi := float64(i) * gold
		pos[i] = vec3{math.Cos(phi) * rr * radius, y * radius, math.Sin(phi) * rr * radius}
	}

	const (
		k      = 1.0  // ideal bond length
		iters  = 600  // small graphs converge fast; molecules are small
		kFlat  = 0.06 // strength of the pull toward the z=0 plane
		zDamp  = 0.55 // out-of-plane moves are resisted (keeps small mols flat)
		repulC = 1.0
	)
	temp := 0.6
	disp := make([]vec3, n)
	for it := 0; it < iters; it++ {
		for i := range disp {
			disp[i] = vec3{}
		}
		// repulsion between every pair
		for i := 0; i < n; i++ {
			for j := i + 1; j < n; j++ {
				dx, dy, dz := pos[i].x-pos[j].x, pos[i].y-pos[j].y, pos[i].z-pos[j].z
				d := math.Sqrt(dx*dx + dy*dy + dz*dz)
				if d < 1e-4 {
					d = 1e-4
					dx = 1e-4 * float64((i%3)-1)
					dy = 1e-4 * float64((j%3)-1)
				}
				f := repulC * k * k / d
				ux, uy, uz := dx/d, dy/d, dz/d
				disp[i].x += ux * f
				disp[i].y += uy * f
				disp[i].z += uz * f
				disp[j].x -= ux * f
				disp[j].y -= uy * f
				disp[j].z -= uz * f
			}
		}
		// attraction along bonds
		for _, b := range g.bonds {
			i, j := b.a, b.b
			if i == j {
				continue
			}
			dx, dy, dz := pos[i].x-pos[j].x, pos[i].y-pos[j].y, pos[i].z-pos[j].z
			d := math.Sqrt(dx*dx + dy*dy + dz*dz)
			if d < 1e-4 {
				d = 1e-4
			}
			f := d * d / k
			ux, uy, uz := dx/d, dy/d, dz/d
			disp[i].x -= ux * f
			disp[i].y -= uy * f
			disp[i].z -= uz * f
			disp[j].x += ux * f
			disp[j].y += uy * f
			disp[j].z += uz * f
		}
		t := temp * (1 - float64(it)/float64(iters))
		for i := 0; i < n; i++ {
			disp[i].z = disp[i].z*zDamp - pos[i].z*kFlat // resist + flatten z
			d := math.Sqrt(disp[i].x*disp[i].x + disp[i].y*disp[i].y + disp[i].z*disp[i].z)
			if d < 1e-9 {
				continue
			}
			lim := math.Min(d, t)
			pos[i].x += disp[i].x / d * lim
			pos[i].y += disp[i].y / d * lim
			pos[i].z += disp[i].z / d * lim
		}
	}
	return pos
}

// ── depth-shaded canvas ─────────────────────────────────────────────────────

// molDepthBG is the per-depth background palette (darkest → nearest), a dark
// teal gradient evoking a circuit board; the deepest level fills the board.
var molDepthBG = []string{
	"\x1b[48;2;12;17;23m",
	"\x1b[48;2;15;27;31m",
	"\x1b[48;2;19;38;42m",
	"\x1b[48;2;25;51;55m",
	"\x1b[48;2;33;67;71m",
}

const (
	colSingle = "\x1b[38;2;96;156;146m" // teal trace
	colDouble = "\x1b[38;2;220;220;170m"
	colTriple = "\x1b[38;2;244;71;71m"
	colArom   = "\x1b[38;2;78;201;176m"
)

func atomColor(sym string) string {
	switch sym {
	case "C":
		return cFG
	case "O":
		return styleColorCode["red"]
	case "N":
		return styleColorCode["blue"]
	case "S":
		return styleColorCode["yellow"]
	case "P":
		return styleColorCode["orange"]
	case "F", "Cl", "Br", "I":
		return styleColorCode["green"]
	case "H":
		return cDim
	default:
		return styleColorCode["purple"]
	}
}

func bondColor(b molBond) string {
	switch {
	case b.arom:
		return colArom
	case b.order >= 3:
		return colTriple
	case b.order == 2:
		return colDouble
	default:
		return colSingle
	}
}

type molCanvas struct {
	w, h int
	ch   []rune
	fg   []string
	bg   []int // depth level index into molDepthBG
}

func newMolCanvas(w, h int) *molCanvas {
	c := &molCanvas{w: w, h: h, ch: make([]rune, w*h), fg: make([]string, w*h), bg: make([]int, w*h)}
	for i := range c.ch {
		c.ch[i] = ' '
	}
	return c
}

func (c *molCanvas) set(col, row int, ch rune, fg string, depth int) {
	if col < 0 || col >= c.w || row < 0 || row >= c.h {
		return
	}
	i := row*c.w + col
	c.ch[i] = ch
	c.fg[i] = fg
	c.bg[i] = depth
}

// lines serializes the canvas into colored strings with run-length-grouped SGR.
func (c *molCanvas) lines() []string {
	out := make([]string, 0, c.h)
	for row := 0; row < c.h; row++ {
		var b strings.Builder
		curFg, curBg := "", -1
		for col := 0; col < c.w; col++ {
			i := row*c.w + col
			bg := molDepthBG[c.bg[i]]
			fg := c.fg[i]
			if c.bg[i] != curBg {
				b.WriteString(bg)
				curBg = c.bg[i]
				curFg = "" // bg reset may drop fg; force re-emit
			}
			if fg != curFg {
				if fg == "" {
					b.WriteString(cFG)
				} else {
					b.WriteString(fg)
				}
				curFg = fg
			}
			b.WriteRune(c.ch[i])
		}
		b.WriteString(cReset)
		out = append(out, b.String())
	}
	return out
}

// bondGlyph picks a straight-line glyph from the bond's screen direction,
// snapping near-axis bonds to │ / ─ so they don't look ragged.
func bondGlyph(dcol, drow int) rune {
	ac, ar := abs(dcol), abs(drow)
	switch {
	case ac*3 < ar:
		return '│'
	case ar*3 < ac:
		return '─'
	case (dcol > 0) == (drow > 0):
		return '╲'
	default:
		return '╱'
	}
}

func depthLevel(t float64) int {
	if t < 0 {
		t = 0
	}
	if t > 1 {
		t = 1
	}
	l := int(math.Round(t * float64(len(molDepthBG)-1)))
	if l < 0 {
		l = 0
	}
	if l >= len(molDepthBG) {
		l = len(molDepthBG) - 1
	}
	return l
}

// drawLine rasterizes a depth-shaded segment (Bresenham), interpolating the
// depth level between endpoints so a trace darkens as it runs into the board.
func (c *molCanvas) drawLine(c0, r0, c1, r1 int, ch rune, fg string, d0, d1 int) {
	dc := abs(c1 - c0)
	dr := -abs(r1 - r0)
	sc, sr := sign(c1-c0), sign(r1-r0)
	err := dc + dr
	steps := dc
	if -dr > steps {
		steps = -dr
	}
	if steps == 0 {
		steps = 1
	}
	x, y, n := c0, r0, 0
	for {
		t := float64(n) / float64(steps)
		depth := int(math.Round(float64(d0)*(1-t) + float64(d1)*t))
		c.set(x, y, ch, fg, depth)
		if x == c1 && y == r1 {
			break
		}
		e2 := 2 * err
		if e2 >= dr {
			err += dr
			x += sc
		}
		if e2 <= dc {
			err += dc
			y += sr
		}
		n++
		if n > steps+2 {
			break
		}
	}
}

func abs(a int) int {
	if a < 0 {
		return -a
	}
	return a
}

func sign(a int) int {
	switch {
	case a > 0:
		return 1
	case a < 0:
		return -1
	}
	return 0
}

// renderMolecule lays out and rasterizes the molecule into depth-shaded canvas
// lines that fit within availW columns.
func renderMolecule(g *molGraph, availW int) []string {
	pos := layoutMolecule(g)
	n := len(pos)
	if n == 0 {
		return nil
	}

	minX, maxX := pos[0].x, pos[0].x
	minY, maxY := pos[0].y, pos[0].y
	minZ, maxZ := pos[0].z, pos[0].z
	for _, p := range pos {
		minX, maxX = math.Min(minX, p.x), math.Max(maxX, p.x)
		minY, maxY = math.Min(minY, p.y), math.Max(maxY, p.y)
		minZ, maxZ = math.Min(minZ, p.z), math.Max(maxZ, p.z)
	}
	spanX := math.Max(maxX-minX, 1e-6)
	spanY := math.Max(maxY-minY, 1e-6)
	spanZ := maxZ - minZ

	cw := availW - 4
	if cw < 20 {
		cw = 20
	}
	if cw > 76 {
		cw = 76
	}
	const maxH = 22
	// terminal cells are ~twice as tall as wide → 2 columns per x-unit, 1 row per y-unit.
	sByW := float64(cw-3) / (spanX * 2)
	sByH := float64(maxH-3) / spanY
	s := math.Min(sByW, sByH)
	if s <= 0 || math.IsInf(s, 0) {
		s = 1
	}
	w := int(spanX*2*s) + 3
	h := int(spanY*s) + 3
	if w < 5 {
		w = 5
	}
	if h < 3 {
		h = 3
	}
	if w > cw {
		w = cw
	}

	cols := make([]int, n)
	rows := make([]int, n)
	depths := make([]int, n)
	for i, p := range pos {
		cols[i] = int((p.x-minX)*2*s) + 1
		rows[i] = int((p.y-minY)*s) + 1
		t := 0.0
		if spanZ > 1e-6 {
			t = (p.z - minZ) / spanZ // 0 = far (dark), 1 = near (bright)
		}
		depths[i] = depthLevel(t)
	}

	cv := newMolCanvas(w, h)

	// bonds first, far → near, so nearer traces overlay deeper ones.
	border := append([]molBond(nil), g.bonds...)
	sort.SliceStable(border, func(i, j int) bool {
		zi := (depths[border[i].a] + depths[border[i].b])
		zj := (depths[border[j].a] + depths[border[j].b])
		return zi < zj
	})
	for _, b := range border {
		if b.a == b.b {
			continue
		}
		ca, ra, da := cols[b.a], rows[b.a], depths[b.a]
		cb, rb, db := cols[b.b], rows[b.b], depths[b.b]
		glyph := bondGlyph(cb-ca, rb-ra)
		fg := bondColor(b)
		// parallel offsets render bond multiplicity legibly in plain text too.
		var offs []int
		switch {
		case b.order >= 3:
			offs = []int{-1, 0, 1}
		case b.order == 2:
			offs = []int{0, 1}
		default:
			offs = []int{0}
		}
		ox, oy := 0, 1 // offset perpendicular: horizontalish → stack rows
		if abs(cb-ca) < abs(rb-ra) {
			ox, oy = 1, 0 // verticalish → stack cols
		}
		for _, o := range offs {
			cv.drawLine(ca+ox*o, ra+oy*o, cb+ox*o, rb+oy*o, glyph, fg, da, db)
		}
	}

	// atoms on top, far → near.
	order := make([]int, n)
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(i, j int) bool { return depths[order[i]] < depths[order[j]] })
	for _, i := range order {
		a := g.atoms[i]
		fg := cBold + atomColor(a.sym)
		if a.sym == "C" {
			cv.set(cols[i], rows[i], '○', fg, depths[i])
			continue
		}
		runes := []rune(a.sym)
		cv.set(cols[i], rows[i], runes[0], fg, depths[i])
		if len(runes) > 1 {
			cv.set(cols[i]+1, rows[i], runes[1], fg, depths[i])
		}
	}
	return cv.lines()
}

// ── inline 2D viewer (alt+e) ────────────────────────────────────────────────

func moleculeGlyph(it *item) (string, string) { return "⬡", cAccent }

// moleculeView is the molecule node's inline expanded view: a depth-shaded 2D
// node-link drawing rendered as bands beneath the node (never a separate
// screen). It is read-only — the molecule comes from the node text — so Key only
// scrolls and Leave just clears the render cache.
type moleculeView struct{}

func (moleculeView) content(m *Model, it *item, width int) []string {
	d := m.nodeStore(it.uuid)
	key := fmt.Sprintf("%d|%s", width, it.name)
	if d["molKey"] == key {
		if lines, ok := d["molLines"].([]string); ok {
			return lines
		}
	}
	lines := moleculeContent(it.name, width)
	d["molKey"] = key
	d["molLines"] = lines
	return lines
}

// moleculeContent builds the full (unwindowed) band content for a notation
// string: a header, the depth-shaded canvas, and a legend.
func moleculeContent(name string, width int) []string {
	g, err := parseMolecule(name)
	if err != nil {
		return []string{
			cDim + "  molecule · esc close" + cReset,
			cRed + "  cannot parse: " + err.Error() + cReset,
		}
	}
	header := fmt.Sprintf("  molecule · %s · %s · %d atoms · depth shaded · ↑↓ scroll · esc",
		g.format, g.formula(), len(g.atoms))
	out := []string{cDim + clip(header, width-1) + cReset}
	out = append(out, renderMolecule(g, width)...)
	legend := "  ○ C · letters heteroatoms · ─│╱╲ bonds (teal·single yellow·double red·triple cyan·aromatic) · darker = deeper"
	out = append(out, cDim+clip(legend, width-1)+cReset)
	return out
}

func (v moleculeView) Enter(m *Model, it *item) bool {
	return strings.TrimSpace(it.name) != ""
}

func (v moleculeView) Leave(m *Model, it *item) {
	d := m.nodeStore(it.uuid)
	delete(d, "molKey")
	delete(d, "molLines")
}

func (v moleculeView) Lines(m *Model, it *item, width int) int {
	return len(v.content(m, it, width))
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
	}
	return nil, false // esc / ctrl+c handled centrally
}

func (v moleculeView) Bands(m *Model, it *item, rail string, width, scroll, winH int, focused bool) []string {
	content := v.content(m, it, width)
	if scroll < 0 {
		scroll = 0
	}
	if scroll > len(content) {
		scroll = len(content)
	}
	end := scroll + winH
	if end > len(content) {
		end = len(content)
	}
	out := make([]string, 0, end-scroll)
	for _, line := range content[scroll:end] {
		out = append(out, clip(rail+cReset+line, width))
	}
	return out
}
