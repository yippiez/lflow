package editor

import (
	"fmt"
	"math"
	"strings"
)

// molrender.go turns a parsed molecule into the drawing the viewer shows: the
// layout, the character canvas, and the pointillist painter. Pure — given a
// graph and a width it returns coloured terminal lines.

// ── layout ───────────────────────────────────────────────────────────────────

type vec3 struct{ x, y, z float64 }

// layoutMolecule places atoms with a deterministic 3D Fruchterman-Reingold
// spring layout. A mild flattening force pulls atoms toward the z=0 plane, so
// small molecules stay essentially planar while large, crowded ones bulge into
// the third dimension; the drawing projects x/y and ignores z.
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

// ── element colour ───────────────────────────────────────────────────────────

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

// projectGrid lays the molecule out (3D, projected to x/y) and fits it into a
// cell grid sized to innerW, returning the grid dims and each atom's cell. The
// 2× column factor compensates for the ~2:1 terminal cell aspect.
func projectGrid(g *molGraph, innerW int) (w, h int, acol, arow []int) {
	pos := layoutMolecule(g)
	n := len(pos)
	if n == 0 {
		return 1, 1, nil, nil
	}
	minX, maxX := pos[0].x, pos[0].x
	minY, maxY := pos[0].y, pos[0].y
	for _, p := range pos {
		minX, maxX = math.Min(minX, p.x), math.Max(maxX, p.x)
		minY, maxY = math.Min(minY, p.y), math.Max(maxY, p.y)
	}
	spanX := math.Max(maxX-minX, 1e-6)
	spanY := math.Max(maxY-minY, 1e-6)

	cw := innerW - 2
	if cw < 24 {
		cw = 24
	}
	if cw > 96 {
		cw = 96
	}
	const maxH = 24
	const pad = 4
	s := math.Min(float64(cw-pad)/(spanX*2), float64(maxH-pad)/spanY)
	if s <= 0 || math.IsInf(s, 0) {
		s = 1
	}
	w = int(spanX*2*s) + pad
	h = int(spanY*s) + pad
	if w < 6 {
		w = 6
	}
	if h < 4 {
		h = 4
	}
	if w > cw {
		w = cw
	}
	acol = make([]int, n)
	arow = make([]int, n)
	for i, p := range pos {
		acol[i] = int((p.x-minX)*2*s) + pad/2
		arow[i] = int((p.y-minY)*s) + pad/2
	}
	return w, h, acol, arow
}

func centerPad(lines []string, w, innerW int) []string {
	if pad := (innerW - w) / 2; pad > 0 {
		prefix := strings.Repeat(" ", pad)
		for i := range lines {
			lines[i] = prefix + lines[i]
		}
	}
	return lines
}

// ── colour helpers ───────────────────────────────────────────────────────────

func clampByte(v float64) int {
	if v < 0 {
		return 0
	}
	if v > 255 {
		return 255
	}
	return int(v)
}

func rgbFg(c [3]int, scale float64) string {
	return fmt.Sprintf("\x1b[38;2;%d;%d;%dm",
		clampByte(float64(c[0])*scale), clampByte(float64(c[1])*scale), clampByte(float64(c[2])*scale))
}

func lerpRGB(a, b [3]int, t float64) [3]int {
	return [3]int{
		int(float64(a[0])*(1-t) + float64(b[0])*t),
		int(float64(a[1])*(1-t) + float64(b[1])*t),
		int(float64(a[2])*(1-t) + float64(b[2])*t),
	}
}

// molHash is a tiny deterministic FNV-1a over two ints, for stable jitter
// (no RNG, so renders are byte-identical run to run).
func molHash(a, b int) uint32 {
	h := uint32(2166136261)
	for _, v := range [2]int{a, b} {
		h = (h ^ uint32(uint32(v)&0xffff)) * 16777619
	}
	return h
}

// ── the drawing: a pointillist particle field ────────────────────────────────
//
// The molecule is drawn as coloured dots: a dense, element-tinted cluster at
// each atom and a stream of dots along each bond, graded between the two atoms'
// hues. Dot density carries the structure, so the picture reads as chemistry
// without any line work — no glyph seams, no stair-stepped diagonals.

var pointRamp = []rune{'·', '⋅', '∙', '°', '∘', '•', '●'}

// renderMolecule lays the graph out and paints it, centered within innerW.
func renderMolecule(g *molGraph, innerW int) []string {
	w, h, acol, arow := projectGrid(g, innerW)
	if w <= 1 || acol == nil {
		return nil
	}
	cv := newMolCanvas(w, h)

	// bond streams first — atom clusters overpaint them.
	for bi, b := range g.bonds {
		if b.a == b.b {
			continue
		}
		ra, rb := atomRGB(g.atoms[b.a].sym), atomRGB(g.atoms[b.b].sym)
		const steps = 28
		for s := 0; s <= steps; s++ {
			t := float64(s) / float64(steps)
			x := float64(acol[b.a]) + float64(acol[b.b]-acol[b.a])*t
			y := float64(arow[b.a]) + float64(arow[b.b]-arow[b.a])*t
			// deterministic jitter (no RNG) so a stream looks scattered, not ruled
			hh := molHash(bi*131+s, b.a*7+b.b+1)
			jx, jy := int(hh%5)-2, int((hh/5)%3)-1
			gi := 1 + int((hh/15)%3)
			cv.set(int(x+0.5)+jx, int(y+0.5)+jy, pointRamp[gi], rgbFg(lerpRGB(ra, rb, t), 0.85))
		}
	}

	// atom clusters: a golden-angle disk, densest and brightest at the core.
	for i, a := range g.atoms {
		rgb := atomRGB(a.sym)
		nd := 9 + int(atomicWeight[a.sym]/6)
		for k := 0; k < nd; k++ {
			ang := float64(k) * 2.39996
			rr := math.Sqrt(float64(k)) * 1.05
			dc, dr := int(math.Cos(ang)*rr*2), int(math.Sin(ang)*rr) // 2× cols for aspect
			dens := 1 - float64(k)/float64(nd)
			gi := 2 + int(dens*float64(len(pointRamp)-3))
			if gi >= len(pointRamp) {
				gi = len(pointRamp) - 1
			}
			cv.set(acol[i]+dc, arow[i]+dr, pointRamp[gi], rgbFg(rgb, 0.6+0.4*dens))
		}
		core := '●'
		if a.sym != "C" {
			core = []rune(a.sym)[0] // heteroatoms keep their letter
		}
		cv.set(acol[i], arow[i], core, cBold+rgbFg(rgb, 1.0))
	}
	return centerPad(cv.lines(), w, innerW)
}
