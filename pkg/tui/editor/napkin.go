package editor

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lflow/lflow/pkg/tui/database"
)

// A napkin node is a little COLOR drawing surface. In the outline it reads as a
// dark-gray square glyph plus a small color thumbnail of the drawing next to the
// node text. alt+e opens a full-screen drawing editor (a dedicated editor screen
// that still lives in the terminal scrollback — never the OS alt-screen, the
// project's hard rule): a full-color canvas rendered with half-block pixels (two
// colors per cell). Keys 1-9 pick a palette color, 0 erases; move the cursor
// (arrows or hjkl) to paint a stroke, space lifts the pen so you can reposition,
// esc saves and closes. The top bar shows only the currently picked
// color. The pixel grid lives in a local file
// (~/.local/share/lflow/napkin/<uuid>.txt), never the synced DB — same rule as
// voice audio: binary/derived content stays on disk, recomputed on demand.

const (
	napkinW = 64 // canvas width in pixels (= half-block cells across)
	napkinH = 28 // canvas height in pixels (must be even; /2 = cell rows)
)

// napkinPalette maps a palette index (1..9) to an RGB color; 0 is "empty".
var napkinPalette = [10][3]int{
	{26, 26, 32},    // 0 empty — the canvas panel color
	{238, 238, 238}, // 1 white
	{244, 71, 71},   // 2 red
	{225, 150, 70},  // 3 orange
	{220, 205, 90},  // 4 yellow
	{120, 180, 90},  // 5 green
	{78, 201, 176},  // 6 cyan
	{86, 156, 214},  // 7 blue
	{197, 134, 192}, // 8 magenta
	{150, 150, 160}, // 9 gray
}

var napkinCursorRGB = [3]int{255, 255, 255} // the painting cursor pixel

func sgrFG(c [3]int) string { return fmt.Sprintf("\x1b[38;2;%d;%d;%dm", c[0], c[1], c[2]) }
func sgrBG(c [3]int) string { return fmt.Sprintf("\x1b[48;2;%d;%d;%dm", c[0], c[1], c[2]) }

// Palette colors are static, so their SGR escapes are built once into lookup
// tables rather than re-formatted per pixel on every render frame.
var (
	napkinFGCode = func() (t [10]string) {
		for i, c := range napkinPalette {
			t[i] = sgrFG(c)
		}
		return
	}()
	napkinBGCode = func() (t [10]string) {
		for i, c := range napkinPalette {
			t[i] = sgrBG(c)
		}
		return
	}()
	napkinCursorFG   = sgrFG(napkinCursorRGB)
	napkinBottomRule = cDim + strings.Repeat("─", napkinW) + cReset
)

// napkinCanvas is the in-memory drawing: a flat grid of palette indices plus the
// editor's cursor and current color. Cached in the per-node store, flushed to
// disk on Leave.
type napkinCanvas struct {
	px     []byte // napkinW*napkinH, palette index 0..9 (0 = empty)
	cx, cy int    // cursor pixel position
	cur    byte   // current paint color 1..9 (0 = eraser)
	lift   bool   // pen up: moving repositions without painting
}

func (c *napkinCanvas) at(x, y int) byte {
	if x < 0 || x >= napkinW || y < 0 || y >= napkinH {
		return 0
	}
	return c.px[y*napkinW+x]
}

func (c *napkinCanvas) set(x, y int, v byte) {
	if x < 0 || x >= napkinW || y < 0 || y >= napkinH {
		return
	}
	c.px[y*napkinW+x] = v
}

func (c *napkinCanvas) blank() bool {
	for _, p := range c.px {
		if p != 0 {
			return false
		}
	}
	return true
}

// line paints a Bresenham segment in color v so a single cursor jump still strokes
// continuously. v=0 erases along the path.
func (c *napkinCanvas) line(x0, y0, x1, y1 int, v byte) {
	dx, dy := napkinAbs(x1-x0), -napkinAbs(y1-y0)
	sx, sy := 1, 1
	if x0 > x1 {
		sx = -1
	}
	if y0 > y1 {
		sy = -1
	}
	err := dx + dy
	for {
		c.set(x0, y0, v)
		if x0 == x1 && y0 == y1 {
			return
		}
		e2 := 2 * err
		if e2 >= dy {
			err += dy
			x0 += sx
		}
		if e2 <= dx {
			err += dx
			y0 += sy
		}
	}
}

// hbRow renders cell-row cr (pixel rows 2*cr and 2*cr+1) with a dithered look:
// painted cells are stippled in their color over the panel — ▓ where both
// sub-pixels are painted, ▒ where one is — so fills read as textured shading
// rather than flat blocks. Empty cells are the solid panel; the cursor is white.
func (c *napkinCanvas) hbRow(cr int) string {
	top, bot := cr*2, cr*2+1
	panel := napkinBGCode[0]
	var b strings.Builder
	for x := 0; x < napkinW; x++ {
		if c.cx == x && (c.cy == top || c.cy == bot) {
			b.WriteString(napkinCursorFG + panel + "█") // cursor cell
			continue
		}
		t, bt := c.at(x, top), c.at(x, bot)
		switch {
		case t == 0 && bt == 0:
			b.WriteString(panel + " ")
		case t != 0 && bt != 0:
			b.WriteString(napkinFGCode[t] + panel + "▓")
		case t != 0:
			b.WriteString(napkinFGCode[t] + panel + "▒")
		default:
			b.WriteString(napkinFGCode[bt] + panel + "▒")
		}
	}
	b.WriteString(cReset)
	return b.String()
}

func (m *Model) napkinPath(uuid string) string {
	return filepath.Join(m.ctx.Paths.Data, "lflow", "napkin", uuid+".txt")
}

// napkinOf returns the cached canvas, lazily loading it from disk on first use.
func (m *Model) napkinOf(uuid string) *napkinCanvas {
	d := m.nodeStore(uuid)
	if c, ok := d["napkinCanvas"].(*napkinCanvas); ok {
		return c
	}
	c := m.napkinLoad(uuid)
	d["napkinCanvas"] = c
	return c
}

// napkinLoad reads the grid (one digit per pixel, 0 = empty) from disk; a blank
// canvas if absent. The current color defaults to cyan and the cursor to center
// (the cursor position isn't persisted).
func (m *Model) napkinLoad(uuid string) *napkinCanvas {
	c := &napkinCanvas{px: make([]byte, napkinW*napkinH), cur: 6, cx: napkinW / 2, cy: napkinH / 2}
	data, err := os.ReadFile(m.napkinPath(uuid))
	if err != nil {
		return c
	}
	lines := strings.Split(string(data), "\n")
	for y := 0; y < napkinH && y < len(lines); y++ {
		row := []rune(lines[y])
		for x := 0; x < napkinW && x < len(row); x++ {
			if row[x] >= '1' && row[x] <= '9' {
				c.px[y*napkinW+x] = byte(row[x] - '0')
			}
		}
	}
	return c
}

// napkinSave writes the grid to disk; a blank canvas removes the file.
func (m *Model) napkinSave(uuid string, c *napkinCanvas) {
	path := m.napkinPath(uuid)
	if c.blank() {
		os.Remove(path)
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		m.err = err
		return
	}
	var b strings.Builder
	for y := 0; y < napkinH; y++ {
		for x := 0; x < napkinW; x++ {
			b.WriteByte('0' + c.px[y*napkinW+x])
		}
		b.WriteByte('\n')
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		m.err = err
	}
}

// napkinThumb is the little inline preview next to the node text: a compact
// half-block strip of the drawing in color (transparent where empty), or a dim
// "⌥e draw" hint when the canvas is empty.
func (m *Model) napkinThumb(it *item) string {
	c := m.napkinOf(it.uuid)
	if c.blank() {
		return cDim + "⌥e draw" + cReset
	}
	const bx = 3                  // source pixels per thumbnail column
	wt := napkinW / bx            // ~21 cells wide
	band := napkinH / 2           // top half / bottom half
	dom := func(tx, b int) byte { // dominant palette index in a source block
		var cnt [10]int
		for yy := b * band; yy < (b+1)*band && yy < napkinH; yy++ {
			for xx := tx * bx; xx < (tx+1)*bx && xx < napkinW; xx++ {
				if v := c.at(xx, yy); v != 0 {
					cnt[v]++
				}
			}
		}
		best, bi := 0, byte(0)
		for i := 1; i < 10; i++ {
			if cnt[i] > best {
				best, bi = cnt[i], byte(i)
			}
		}
		return bi
	}
	var b strings.Builder
	for tx := 0; tx < wt; tx++ {
		t, bt := dom(tx, 0), dom(tx, 1)
		switch {
		case t == 0 && bt == 0:
			b.WriteString(" ")
		case t != 0 && bt != 0:
			b.WriteString(napkinFGCode[t] + "▓" + cReset)
		case t != 0:
			b.WriteString(napkinFGCode[t] + "▒" + cReset)
		default:
			b.WriteString(napkinFGCode[bt] + "▒" + cReset)
		}
	}
	return b.String()
}

// napkinBodySuffix appends a napkin's inline drawing thumbnail to its rendered
// body; a no-op for other node types. Centralizes the per-row hook so every
// render site (outline, final view, temp panel) stays in sync.
func (m *Model) napkinBodySuffix(it *item, body string) string {
	if it.typ == database.TypeNapkin {
		return body + "  " + m.napkinThumb(it)
	}
	return body
}

// napkinFocused reports whether the editor is focused into a napkin node — the
// signal for View to draw the full-screen drawing editor.
func (m *Model) napkinFocused() bool {
	if !m.focused {
		return false
	}
	cur := m.cursorItem()
	return cur != nil && cur.typ == database.TypeNapkin
}

// napkinSwatch is the only thing the top bar shows: a block in the current color
// (or a hollow eraser marker when erasing).
func napkinSwatch(cur byte) string {
	if cur == 0 {
		return cDim + "▱▱▱ erase" + cReset
	}
	return sgrFG(napkinPalette[cur]) + "███" + cReset
}

// viewDraw renders the full-screen napkin editor: a top bar showing only the
// picked color, the full-color canvas, and a thin bottom rule. Still terminal
// scrollback, never the OS alt-screen. Padded to a stable height, status bar last.
func (m *Model) viewDraw(maxLine int) []string {
	it := m.cursorItem()
	c := m.napkinOf(it.uuid)
	leftPad := (maxLine - napkinW) / 2
	if leftPad < 0 {
		leftPad = 0
	}
	ind := strings.Repeat(" ", leftPad)

	var content []string
	// top bar: the picked color swatch, then a muted rule filling to the canvas
	// width. Nothing else.
	sw := napkinSwatch(c.cur)
	rule := strings.Repeat("─", max(0, napkinW-visibleWidth(sw)-1))
	content = append(content, ind+sw+" "+cDim+rule+cReset)
	content = append(content, "")
	for cr := 0; cr < napkinH/2; cr++ {
		content = append(content, clip(ind+c.hbRow(cr), maxLine))
	}
	content = append(content, "")
	content = append(content, ind+napkinBottomRule)
	pen := "paint"
	if c.lift {
		pen = "move"
	}
	content = append(content, ind+cDim+fmt.Sprintf("%s · 1-9 color · 0 erase · space lift · esc save", pen)+cReset)

	budget := m.rowBudget()
	if len(content) > budget {
		content = content[:budget]
	}
	for len(content) < budget {
		content = append(content, "")
	}
	return append(content, m.bottomBar(maxLine))
}

// napkinGlyph is the dark-gray square shown for every napkin node.
func napkinGlyph(it *item) (string, string) { return glyphNapkin, cDim }

// napkinView is the alt+e drawing editor.
type napkinView struct{}

// Enter focuses the editor. The canvas defaults (cyan color, centered cursor) are
// seeded in napkinLoad, so there is nothing to set up here.
func (napkinView) Enter(m *Model, it *item) bool {
	m.napkinOf(it.uuid)
	return true
}

// Leave saves the drawing to its local file.
func (napkinView) Leave(m *Model, it *item) {
	m.napkinSave(it.uuid, m.napkinOf(it.uuid))
}

// Lines/Bands satisfy the nodeView interface but go unused: a focused napkin is
// drawn full-screen by View→viewDraw, not as inline bands beneath the node.
func (napkinView) Lines(_ *Model, _ *item, _ int) int { return 0 }

func (napkinView) Bands(_ *Model, _ *item, _ string, _, _, _ int, _ bool) []string {
	return nil
}

// Key drives the editor: 1-9 pick a color, 0 erases, arrows/hjkl paint (unless the
// pen is lifted), space lifts. esc/ctrl+c fall through to central handling (which
// calls Leave to save).
func (napkinView) Key(m *Model, it *item, k tea.KeyMsg) (tea.Cmd, bool) {
	c := m.napkinOf(it.uuid)
	if k.Type == tea.KeySpace { // toggle pen up/down
		c.lift = !c.lift
		return nil, true
	}
	move := func(dx, dy int) {
		nx := max(0, min(c.cx+dx, napkinW-1))
		ny := max(0, min(c.cy+dy, napkinH-1))
		if !c.lift {
			c.line(c.cx, c.cy, nx, ny, c.cur)
		}
		c.cx, c.cy = nx, ny
	}
	ks := k.String()
	if len(ks) == 1 && ks[0] >= '0' && ks[0] <= '9' {
		c.cur = ks[0] - '0' // 0 = eraser, 1-9 = palette color
		return nil, true
	}
	switch ks {
	case "left", "h":
		move(-1, 0)
	case "right", "l":
		move(1, 0)
	case "up", "k":
		move(0, -1)
	case "down", "j":
		move(0, 1)
	default:
		return nil, false // esc/ctrl+c → central
	}
	return nil, true
}

func napkinAbs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}
