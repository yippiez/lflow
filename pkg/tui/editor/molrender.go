package editor

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

// molrender.go is the molecule → ASCII renderer, kept separate from the parser
// (molecule.go) and the node view. It owns the 2D layout, the depth-shaded glyph
// canvas, and the bond-drawing styles. Everything here is pure: given a molGraph
// and a target width it returns colored terminal lines.

// molStyle selects how bonds are drawn between atoms.
type molStyle int

const (
	styleArc       molStyle = iota // 45° diagonal run + straight tail (skeletal)
	styleManhattan                 // orthogonal H/V traces with box corners (PCB)
	styleStraight                  // direct Bresenham segment (baseline)
	styleBraille                   // sub-pixel Braille canvas (smooth, clean doubles)
)

// molStyleDefault is the style the live viewer uses. This is a placeholder while
// the final look is chosen from the renderer gallery; styleManhattan renders
// cleanly everywhere (orthogonal traces, no stair-stepping).
var molStyleDefault = styleManhattan

// ── 3D layout (depth → glyph brightness) ─────────────────────────────────────

type vec3 struct{ x, y, z float64 }

// layoutMolecule places atoms with a deterministic 3D Fruchterman-Reingold
// spring layout. A mild flattening force pulls atoms toward the z=0 plane, so
// small molecules stay essentially planar while large, crowded ones bulge into
// the third dimension — which the viewer renders as dimmer glyphs (depth), the
// deeper the atom or trace sits.
func layoutMolecule(g *molGraph) []vec3 {
	n := len(g.atoms)
	pos := make([]vec3, n)
	if n <= 1 {
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

// ── colour (depth on the glyph foreground, never a background) ────────────────

func atomRGB(sym string) [3]int {
	switch sym {
	case "C":
		return [3]int{212, 212, 212}
	case "O":
		return [3]int{244, 71, 71}
	case "N":
		return [3]int{86, 156, 214}
	case "S":
		return [3]int{220, 220, 170}
	case "P":
		return [3]int{206, 145, 120}
	case "F", "Cl", "Br", "I":
		return [3]int{106, 153, 85}
	case "H":
		return [3]int{122, 122, 122}
	default:
		return [3]int{197, 134, 192}
	}
}

func bondRGB(b molBond) [3]int {
	switch {
	case b.arom:
		return [3]int{78, 201, 176} // cyan
	case b.order >= 3:
		return [3]int{244, 71, 71} // red
	case b.order == 2:
		return [3]int{220, 220, 170} // yellow
	default:
		return [3]int{96, 156, 146} // teal
	}
}

// depthFg maps a base colour and a depth in [0,1] (1 = nearest) to an SGR
// foreground escape, dimming deeper glyphs toward the dark board colour.
func depthFg(rgb [3]int, t float64) string {
	if t < 0 {
		t = 0
	}
	if t > 1 {
		t = 1
	}
	f := 0.32 + 0.68*t
	return fmt.Sprintf("\x1b[38;2;%d;%d;%dm", int(float64(rgb[0])*f), int(float64(rgb[1])*f), int(float64(rgb[2])*f))
}

// ── canvas ───────────────────────────────────────────────────────────────────

type molCanvas struct {
	w, h int
	ch   []rune
	fg   []string // pre-shaded SGR foreground per cell; "" = empty
}

func newMolCanvas(w, h int) *molCanvas {
	c := &molCanvas{w: w, h: h, ch: make([]rune, w*h), fg: make([]string, w*h)}
	for i := range c.ch {
		c.ch[i] = ' '
	}
	return c
}

func (c *molCanvas) set(col, row int, ch rune, fg string) {
	if col < 0 || col >= c.w || row < 0 || row >= c.h {
		return
	}
	i := row*c.w + col
	c.ch[i] = ch
	c.fg[i] = fg
}

// lines serializes the canvas to colored strings, resetting between runs so a
// bold atom never bleeds its attributes into the following bond.
func (c *molCanvas) lines() []string {
	out := make([]string, 0, c.h)
	for row := 0; row < c.h; row++ {
		var b strings.Builder
		cur := "\x00" // sentinel so the first cell always emits
		for col := 0; col < c.w; col++ {
			i := row*c.w + col
			if c.fg[i] != cur {
				b.WriteString(cReset)
				if c.fg[i] != "" {
					b.WriteString(c.fg[i])
				}
				cur = c.fg[i]
			}
			b.WriteRune(c.ch[i])
		}
		b.WriteString(cReset)
		out = append(out, b.String())
	}
	return out
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

func lerp(a, b float64, step, total int) float64 {
	if total <= 0 {
		return b
	}
	return a + (b-a)*float64(step)/float64(total)
}

// ── bond drawers (one per style) ─────────────────────────────────────────────

// drawStraight is a Bresenham segment with a single direction glyph.
func (c *molCanvas) drawStraight(c0, r0, c1, r1 int, rgb [3]int, t0, t1 float64) {
	ac, ar := abs(c1-c0), abs(r1-r0)
	var glyph rune
	switch {
	case ac*3 < ar:
		glyph = '│'
	case ar*3 < ac:
		glyph = '─'
	case (c1-c0 > 0) == (r1-r0 > 0):
		glyph = '╲'
	default:
		glyph = '╱'
	}
	dc, dr := ac, -ar
	sc, sr := sign(c1-c0), sign(r1-r0)
	err := dc + dr
	steps := ac
	if ar > steps {
		steps = ar
	}
	if steps == 0 {
		steps = 1
	}
	x, y, n := c0, r0, 0
	for {
		c.set(x, y, glyph, depthFg(rgb, lerp(t0, t1, n, steps)))
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

// drawArc is a clean 45° diagonal run plus a straight tail — no stair-stepping.
func (c *molCanvas) drawArc(c0, r0, c1, r1 int, rgb [3]int, t0, t1 float64) {
	sx, sy := sign(c1-c0), sign(r1-r0)
	adx, ady := abs(c1-c0), abs(r1-r0)
	diag := adx
	if ady < diag {
		diag = ady
	}
	total := adx
	if ady > total {
		total = ady
	}
	if total == 0 {
		return
	}
	dg := '╱'
	if sx == sy {
		dg = '╲'
	}
	x, y, step := c0, r0, 0
	// straight tail first (leaves the atom cleanly), then diagonal into the target.
	if adx > ady {
		for k := 0; k < adx-ady; k++ {
			x += sx
			step++
			c.set(x, y, '─', depthFg(rgb, lerp(t0, t1, step, total)))
		}
	} else {
		for k := 0; k < ady-adx; k++ {
			y += sy
			step++
			c.set(x, y, '│', depthFg(rgb, lerp(t0, t1, step, total)))
		}
	}
	for k := 0; k < diag; k++ {
		x += sx
		y += sy
		step++
		c.set(x, y, dg, depthFg(rgb, lerp(t0, t1, step, total)))
	}
}

// drawManhattan routes the bond orthogonally (horizontal then vertical) with a
// rounded box corner — the printed-circuit look.
func (c *molCanvas) drawManhattan(c0, r0, c1, r1 int, rgb [3]int, t0, t1 float64) {
	sx, sy := sign(c1-c0), sign(r1-r0)
	adx, ady := abs(c1-c0), abs(r1-r0)
	total := adx + ady
	if total == 0 {
		return
	}
	x, y, step := c0, r0, 0
	for k := 0; k < adx; k++ {
		x += sx
		step++
		c.set(x, y, '─', depthFg(rgb, lerp(t0, t1, step, total)))
	}
	if adx > 0 && ady > 0 {
		corner := '╮'
		switch {
		case sx > 0 && sy > 0:
			corner = '╮'
		case sx > 0 && sy < 0:
			corner = '╯'
		case sx < 0 && sy > 0:
			corner = '╭'
		case sx < 0 && sy < 0:
			corner = '╰'
		}
		c.set(x, y, corner, depthFg(rgb, lerp(t0, t1, step, total)))
	}
	for k := 0; k < ady; k++ {
		y += sy
		step++
		c.set(x, y, '│', depthFg(rgb, lerp(t0, t1, step, total)))
	}
}

func (c *molCanvas) drawBond(style molStyle, c0, r0, c1, r1 int, rgb [3]int, t0, t1 float64) {
	switch style {
	case styleManhattan:
		c.drawManhattan(c0, r0, c1, r1, rgb, t0, t1)
	case styleStraight:
		c.drawStraight(c0, r0, c1, r1, rgb, t0, t1)
	default:
		c.drawArc(c0, r0, c1, r1, rgb, t0, t1)
	}
}

// ── top-level render ─────────────────────────────────────────────────────────

// renderMolecule renders with the live default style.
func renderMolecule(g *molGraph, innerW int) []string {
	return renderMoleculeStyle(g, innerW, molStyleDefault)
}

// renderMoleculeStyle lays out and rasterizes the molecule into glyph-colored
// canvas lines (depth-shaded, centered within innerW columns) using the style.
func renderMoleculeStyle(g *molGraph, innerW int, style molStyle) []string {
	if style == styleBraille {
		return renderBraille(g, innerW)
	}
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

	cw := innerW - 2
	if cw < 20 {
		cw = 20
	}
	if cw > 110 {
		cw = 110
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
	depthT := make([]float64, n)
	for i, p := range pos {
		cols[i] = int((p.x-minX)*2*s) + 1
		rows[i] = int((p.y-minY)*s) + 1
		t := 1.0 // flat molecule → uniformly near (no fake depth)
		if spanZ > 1e-6 {
			t = (p.z - minZ) / spanZ // 0 = far (dim), 1 = near (bright)
		}
		depthT[i] = t
	}

	cv := newMolCanvas(w, h)

	// bonds first, far → near, so nearer traces overlay deeper ones.
	bonds := append([]molBond(nil), g.bonds...)
	sort.SliceStable(bonds, func(i, j int) bool {
		return depthT[bonds[i].a]+depthT[bonds[i].b] < depthT[bonds[j].a]+depthT[bonds[j].b]
	})
	for _, b := range bonds {
		if b.a == b.b {
			continue
		}
		ca, ra, ta := cols[b.a], rows[b.a], depthT[b.a]
		cb, rb, tb := cols[b.b], rows[b.b], depthT[b.b]
		rgb := bondRGB(b)
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
			cv.drawBond(style, ca+ox*o, ra+oy*o, cb+ox*o, rb+oy*o, rgb, ta, tb)
		}
	}

	// atoms on top, far → near.
	order := make([]int, n)
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(i, j int) bool { return depthT[order[i]] < depthT[order[j]] })
	for _, i := range order {
		a := g.atoms[i]
		fg := cBold + depthFg(atomRGB(a.sym), depthT[i])
		if a.sym == "C" {
			cv.set(cols[i], rows[i], '○', fg)
			continue
		}
		runes := []rune(a.sym)
		cv.set(cols[i], rows[i], runes[0], fg)
		if len(runes) > 1 {
			cv.set(cols[i]+1, rows[i], runes[1], fg)
		}
	}

	// center the drawing within the full-width frame.
	lines := cv.lines()
	if pad := (innerW - w) / 2; pad > 0 {
		prefix := strings.Repeat(" ", pad)
		for i := range lines {
			lines[i] = prefix + lines[i]
		}
	}
	return lines
}

// ── Braille sub-pixel renderer ───────────────────────────────────────────────

// brailleDots maps a sub-cell pixel (row 0..3, col 0..1) to its Braille bit, so
// each terminal cell packs a 2×4 dot grid (U+2800 + mask).
var brailleDots = [4][2]uint8{
	{0x01, 0x08},
	{0x02, 0x10},
	{0x04, 0x20},
	{0x40, 0x80},
}

// brailleCanvas is a cell grid where bonds are drawn at 2×4 sub-pixel resolution
// (smooth lines at any angle) and atoms are labeled glyphs layered on top.
type brailleCanvas struct {
	w, h   int
	pw, ph int
	mask   []uint8  // braille bits per cell
	col    []string // bond colour per cell (nearest pixel wins)
	depth  []float64
	ach    []rune   // atom glyph override per cell (0 = none)
	acol   []string // atom colour
}

func newBrailleCanvas(w, h int) *brailleCanvas {
	return &brailleCanvas{
		w: w, h: h, pw: w * 2, ph: h * 4,
		mask: make([]uint8, w*h), col: make([]string, w*h), depth: make([]float64, w*h),
		ach: make([]rune, w*h), acol: make([]string, w*h),
	}
}

func (c *brailleCanvas) pixel(px, py int, rgb [3]int, t float64) {
	if px < 0 || py < 0 || px >= c.pw || py >= c.ph {
		return
	}
	cx, cy := px/2, py/4
	i := cy*c.w + cx
	c.mask[i] |= brailleDots[py%4][px%2]
	if c.col[i] == "" || t >= c.depth[i] { // nearer pixel sets the cell colour
		c.col[i] = depthFg(rgb, t)
		c.depth[i] = t
	}
}

func (c *brailleCanvas) atom(px, py int, ch rune, col string) {
	cx, cy := px/2, py/4
	if cx < 0 || cy < 0 || cx >= c.w || cy >= c.h {
		return
	}
	c.ach[cy*c.w+cx] = ch
	c.acol[cy*c.w+cx] = col
}

// pline draws a sub-pixel Bresenham segment with depth-interpolated colour.
func (c *brailleCanvas) pline(x0, y0, x1, y1 int, rgb [3]int, t0, t1 float64) {
	dx, dy := abs(x1-x0), -abs(y1-y0)
	sx, sy := sign(x1-x0), sign(y1-y0)
	err := dx + dy
	steps := dx
	if -dy > steps {
		steps = -dy
	}
	if steps == 0 {
		steps = 1
	}
	x, y, n := x0, y0, 0
	for {
		c.pixel(x, y, rgb, lerp(t0, t1, n, steps))
		if x == x1 && y == y1 {
			break
		}
		e2 := 2 * err
		if e2 >= dy {
			err += dy
			x += sx
		}
		if e2 <= dx {
			err += dx
			y += sy
		}
		n++
		if n > steps+2 {
			break
		}
	}
}

func (c *brailleCanvas) lines() []string {
	out := make([]string, 0, c.h)
	for cy := 0; cy < c.h; cy++ {
		var b strings.Builder
		cur := "\x00"
		for cx := 0; cx < c.w; cx++ {
			i := cy*c.w + cx
			var glyph rune
			var fg string
			switch {
			case c.ach[i] != 0:
				glyph, fg = c.ach[i], c.acol[i]
			case c.mask[i] != 0:
				glyph, fg = rune(0x2800+int(c.mask[i])), c.col[i]
			default:
				glyph, fg = ' ', ""
			}
			if fg != cur {
				b.WriteString(cReset)
				if fg != "" {
					b.WriteString(fg)
				}
				cur = fg
			}
			b.WriteRune(glyph)
		}
		b.WriteString(cReset)
		out = append(out, b.String())
	}
	return out
}

// renderBraille draws the molecule on a Braille sub-pixel canvas: smooth bonds
// at any angle, double/triple bonds as clean parallel lines, atoms as labeled
// nodes layered on top. Centered within innerW columns.
func renderBraille(g *molGraph, innerW int) []string {
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

	cwCells := innerW - 2
	if cwCells < 20 {
		cwCells = 20
	}
	if cwCells > 108 {
		cwCells = 108
	}
	const maxHCells = 26
	// Braille sub-pixels are ~square, so map x/y with the same scale (no 2:1 fudge).
	pw := cwCells * 2
	ph := maxHCells * 4
	const margin = 3
	s := math.Min(float64(pw-2*margin)/spanX, float64(ph-2*margin)/spanY)
	if s <= 0 || math.IsInf(s, 0) {
		s = 1
	}

	px := make([]int, n)
	py := make([]int, n)
	depthT := make([]float64, n)
	maxPx, maxPy := 0, 0
	for i, p := range pos {
		px[i] = int((p.x-minX)*s) + margin
		py[i] = int((p.y-minY)*s) + margin
		if px[i] > maxPx {
			maxPx = px[i]
		}
		if py[i] > maxPy {
			maxPy = py[i]
		}
		t := 1.0
		if spanZ > 1e-6 {
			t = (p.z - minZ) / spanZ
		}
		depthT[i] = t
	}
	w := (maxPx+margin)/2 + 1
	h := (maxPy+margin)/4 + 1
	if w < 4 {
		w = 4
	}
	if h < 2 {
		h = 2
	}
	cv := newBrailleCanvas(w, h)

	// bonds far → near; double/triple drawn as parallel sub-pixel lines.
	bonds := append([]molBond(nil), g.bonds...)
	sort.SliceStable(bonds, func(i, j int) bool {
		return depthT[bonds[i].a]+depthT[bonds[i].b] < depthT[bonds[j].a]+depthT[bonds[j].b]
	})
	for _, b := range bonds {
		if b.a == b.b {
			continue
		}
		x0, y0, t0 := px[b.a], py[b.a], depthT[b.a]
		x1, y1, t1 := px[b.b], py[b.b], depthT[b.b]
		rgb := bondRGB(b)
		// unit perpendicular in pixel space, for parallel offsets.
		dx, dy := float64(x1-x0), float64(y1-y0)
		l := math.Hypot(dx, dy)
		if l < 1e-6 {
			l = 1
		}
		nx, ny := -dy/l, dx/l
		var offs []float64
		switch {
		case b.order >= 3:
			offs = []float64{-2, 0, 2}
		case b.order == 2:
			offs = []float64{-1.2, 1.2}
		default:
			offs = []float64{0}
		}
		for _, o := range offs {
			ox, oy := int(math.Round(nx*o)), int(math.Round(ny*o))
			cv.pline(x0+ox, y0+oy, x1+ox, y1+oy, rgb, t0, t1)
		}
	}

	// atoms on top, far → near.
	order := make([]int, n)
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(i, j int) bool { return depthT[order[i]] < depthT[order[j]] })
	for _, i := range order {
		a := g.atoms[i]
		col := cBold + depthFg(atomRGB(a.sym), depthT[i])
		glyph := '●'
		if a.sym != "C" {
			glyph = []rune(a.sym)[0]
		}
		cv.atom(px[i], py[i], glyph, col)
		if a.sym != "C" && len([]rune(a.sym)) > 1 {
			cv.atom(px[i]+2, py[i], []rune(a.sym)[1], col)
		}
	}

	lines := cv.lines()
	if pad := (innerW - w) / 2; pad > 0 {
		prefix := strings.Repeat(" ", pad)
		for i := range lines {
			lines[i] = prefix + lines[i]
		}
	}
	return lines
}
