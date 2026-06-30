package editor

import (
	"math"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lflow/lflow/pkg/tui/database"
)

// A napkin node renders a little 3D scene in the terminal, approximated as cubes.
// This is the foundation: a single wireframe cube you can spin. The next step is a
// TouchDesigner-style operator network — the node's CHILD nodes become operators
// (Box, Transform, Array, …) whose evaluation builds the scene — so the cube here
// is the default output of an empty network. alt+e focuses the node and the arrow
// keys orbit the camera; inline it shows a dim hint next to the node text.

const (
	napkinPxW = 80 // render pixel grid width  (braille → 40 cells)
	napkinPxH = 40 // render pixel grid height (braille → 10 rows)
)

// braille sub-cell bit per (row 0..3, col 0..1); a cell rune is U+2800 + the OR.
var brailleDots = [4][2]rune{
	{0x01, 0x08},
	{0x02, 0x10},
	{0x04, 0x20},
	{0x40, 0x80},
}

type vec3 struct{ x, y, z float64 }

// a unit cube centred on the origin: 8 corners, 12 edges.
var cubeVerts = []vec3{
	{-1, -1, -1}, {1, -1, -1}, {1, 1, -1}, {-1, 1, -1},
	{-1, -1, 1}, {1, -1, 1}, {1, 1, 1}, {-1, 1, 1},
}
var cubeEdges = [12][2]int{
	{0, 1}, {1, 2}, {2, 3}, {3, 0}, // back face
	{4, 5}, {5, 6}, {6, 7}, {7, 4}, // front face
	{0, 4}, {1, 5}, {2, 6}, {3, 7}, // connectors
}

// rotProject rotates a point by (ax, ay) and perspective-projects it to pixel
// coordinates on a W×H grid.
func rotProject(v vec3, ax, ay float64, W, H int) (int, int) {
	cy, sy := math.Cos(ay), math.Sin(ay)
	x := v.x*cy + v.z*sy
	z := -v.x*sy + v.z*cy
	cx, sx := math.Cos(ax), math.Sin(ax)
	y := v.y*cx - z*sx
	z = v.y*sx + z*cx

	const dist = 4.0
	f := float64(H) * 0.85
	zz := z + dist
	if zz < 0.1 {
		zz = 0.1
	}
	px := int(x*f/zz + float64(W)/2)
	py := int(-y*f/zz + float64(H)/2)
	return px, py
}

// cubeCanvas rasterizes the wireframe cube into a W×H boolean pixel grid.
func cubeCanvas(ax, ay float64, W, H int) []bool {
	g := make([]bool, W*H)
	set := func(x, y int) {
		if x >= 0 && x < W && y >= 0 && y < H {
			g[y*W+x] = true
		}
	}
	line := func(x0, y0, x1, y1 int) {
		dx, dy := absInt(x1-x0), -absInt(y1-y0)
		sx, sy := 1, 1
		if x0 > x1 {
			sx = -1
		}
		if y0 > y1 {
			sy = -1
		}
		e := dx + dy
		for {
			set(x0, y0)
			if x0 == x1 && y0 == y1 {
				return
			}
			e2 := 2 * e
			if e2 >= dy {
				e += dy
				x0 += sx
			}
			if e2 <= dx {
				e += dx
				y0 += sy
			}
		}
	}
	pts := make([][2]int, len(cubeVerts))
	for i, v := range cubeVerts {
		px, py := rotProject(v, ax, ay, W, H)
		pts[i] = [2]int{px, py}
	}
	for _, e := range cubeEdges {
		line(pts[e[0]][0], pts[e[0]][1], pts[e[1]][0], pts[e[1]][1])
	}
	return g
}

// brailleLines turns a pixel grid into colored braille rows (2×4 px per cell).
func brailleLines(g []bool, W, H int, color string) []string {
	rows := make([]string, 0, H/4)
	for cy := 0; cy < H; cy += 4 {
		var b strings.Builder
		b.WriteString(color)
		for cx := 0; cx < W; cx += 2 {
			var bits rune
			for dy := 0; dy < 4; dy++ {
				for dx := 0; dx < 2; dx++ {
					if yy, xx := cy+dy, cx+dx; yy < H && xx < W && g[yy*W+xx] {
						bits |= brailleDots[dy][dx]
					}
				}
			}
			b.WriteRune(0x2800 + bits)
		}
		b.WriteString(cReset)
		rows = append(rows, b.String())
	}
	return rows
}

// napkinGlyph is the dark-gray square shown for every napkin node.
func napkinGlyph(it *item) (string, string) { return glyphNapkin, cDim }

// napkinBodySuffix appends a napkin's inline hint to its rendered body; a no-op for
// other node types. The 3D render itself shows in the focused alt+e view.
func (m *Model) napkinBodySuffix(it *item, body string) string {
	if it.typ == database.TypeNapkin {
		return body + "  " + cDim + "⬡ 3D · ⌥e spin" + cReset
	}
	return body
}

// napkinAngles returns a node's stored orbit angles, or a default iso view.
func (m *Model) napkinAngles(uuid string) (ax, ay float64) {
	d := m.nodeStore(uuid)
	a, ok := d["napkinAX"].(float64)
	if !ok {
		return 0.5, 0.6
	}
	return a, d["napkinAY"].(float64)
}

func (m *Model) setNapkinAngles(uuid string, ax, ay float64) {
	d := m.nodeStore(uuid)
	d["napkinAX"], d["napkinAY"] = ax, ay
}

// napkinView is the alt+e 3D view: the cube renders as bands beneath the node and
// the arrow keys orbit it. State (the orbit angles) lives in the per-node store.
type napkinView struct{}

func (napkinView) Enter(m *Model, it *item) bool { return true }
func (napkinView) Leave(m *Model, it *item)      {}

func (napkinView) Lines(m *Model, it *item, width int) int { return napkinPxH/4 + 1 }

// Key orbits the camera; esc/ctrl+c fall through to central defocus.
func (napkinView) Key(m *Model, it *item, k tea.KeyMsg) (tea.Cmd, bool) {
	ax, ay := m.napkinAngles(it.uuid)
	const step = 0.2
	switch k.String() {
	case "left", "h":
		ay -= step
	case "right", "l":
		ay += step
	case "up", "k":
		ax -= step
	case "down", "j":
		ax += step
	default:
		return nil, false
	}
	m.setNapkinAngles(it.uuid, ax, ay)
	return nil, true
}

// Bands renders the header hint plus the cube's braille rows, self-windowed to
// [scroll, scroll+winH).
func (napkinView) Bands(m *Model, it *item, rail string, width, scroll, winH int, focused bool) []string {
	ax, ay := m.napkinAngles(it.uuid)
	g := cubeCanvas(ax, ay, napkinPxW, napkinPxH)
	content := make([]string, 0, napkinPxH/4+1)
	content = append(content, clip(rail+cReset+cDim+"  3D · ↑↓←→ orbit · esc close"+cReset, width))
	for _, r := range brailleLines(g, napkinPxW, napkinPxH, cCyan) {
		content = append(content, clip(rail+cReset+"  "+r, width))
	}
	if scroll > len(content)-winH {
		scroll = len(content) - winH
	}
	if scroll < 0 {
		scroll = 0
	}
	if focused {
		m.focusScroll = scroll
	}
	end := scroll + winH
	if end > len(content) {
		end = len(content)
	}
	return content[scroll:end]
}

func absInt(v int) int {
	if v < 0 {
		return -v
	}
	return v
}
