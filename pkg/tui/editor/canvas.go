package editor

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lflow/lflow/pkg/tui/database"
)

// The canvas node: a character-grid drawing edited like a tiny CAD. alt+e
// opens the inline painter (bands beneath the node — never a separate screen):
// a blue crosshair cursor, a searchable named-glyph palette (p), rectangle
// objects (r) and constraint spans (s) anchored to objects, so a line drawn
// from one box to another follows when a box moves. Coordinates are absolute
// grid positions in the document — a cell placed at (12,4) stays at (12,4)
// whatever the terminal size; the viewport pans, the document never reflows.
//
// The document persists as JSON in node_blobs (mime canvasMime), like an
// image's pixels: local payload keyed by the node uuid, GC'd with the node.

const canvasMime = "application/x-lflow-canvas+json"

// canvasDoc is the persisted document. All coordinates are absolute.
type canvasDoc struct {
	W     int          `json:"w"`
	H     int          `json:"h"`
	Cells []canvasCell `json:"cells,omitempty"`
	Objs  []canvasObj  `json:"objects,omitempty"`
	Spans []canvasSpan `json:"spans,omitempty"`
}

// canvasCell is one painted cell: a glyph and optional colors.
type canvasCell struct {
	X  int    `json:"x"`
	Y  int    `json:"y"`
	Ch string `json:"ch"`
	Fg string `json:"fg,omitempty"`
	Bg string `json:"bg,omitempty"`
}

// canvasObj is a named rectangle — the thing spans constrain to. Drawn as a
// light box with its name at the top-left corner.
type canvasObj struct {
	Name string `json:"name"`
	X    int    `json:"x"`
	Y    int    `json:"y"`
	W    int    `json:"w"`
	H    int    `json:"h"`
}

// canvasAnchor is one span endpoint: absolute (Obj == "") at (X,Y), or
// object-relative — the object's origin plus (DX,DY) — so the endpoint
// follows the object when it moves. A vanished object falls back to (X,Y),
// which records the last resolved position on every save.
type canvasAnchor struct {
	Obj string `json:"obj,omitempty"`
	DX  int    `json:"dx,omitempty"`
	DY  int    `json:"dy,omitempty"`
	X   int    `json:"x"`
	Y   int    `json:"y"`
}

// canvasSpan is the constraint line: a run of one glyph from one anchor to
// the other, re-derived every render (horizontal-then-vertical L path).
type canvasSpan struct {
	From canvasAnchor `json:"from"`
	To   canvasAnchor `json:"to"`
	Ch   string       `json:"ch"`
	Fg   string       `json:"fg,omitempty"`
}

// canvasBrush is what stamping paints: a glyph and optional colors. A pure
// background pick keeps Ch empty and stamps a colored space.
type canvasBrush struct {
	Ch string
	Fg string
	Bg string
}

// canvasState is the per-node ephemeral editor state (nodeStore, key "canvas").
type canvasState struct {
	doc    *canvasDoc
	cx, cy int // crosshair cursor, document coordinates
	vx, vy int // viewport origin (top-left document coordinate on screen)
	vw, vh int // last rendered viewport size (Bands records; Key pans with it)

	brush canvasBrush

	mode    string // "" paint · "rect" second corner pending · "span" second anchor pending · "move" object grabbed
	ax, ay  int    // the pending first corner / anchor
	moveIdx int    // index into doc.Objs while mode == "move"

	pal    bool   // palette overlay open
	palQ   string // palette query
	palSel int
}

func canvasStateOf(m *Model, it *item) *canvasState {
	d := m.nodeStore(it.uuid)
	st, _ := d["canvas"].(*canvasState)
	if st == nil {
		st = &canvasState{brush: canvasBrush{Ch: "─"}}
		d["canvas"] = st
	}
	return st
}

// canvasDocOf is the cached read every render-path surface uses: the live (or
// last-saved) document when one is in the node store, else one blob load that
// then sticks as the cache. Enter() always reloads fresh.
func (m *Model) canvasDocOf(it *item) *canvasDoc {
	st := canvasStateOf(m, it)
	if st.doc == nil {
		st.doc = m.canvasLoad(it)
	}
	return st.doc
}

// canvasLoad reads the document from the blob (or starts a fresh one).
func (m *Model) canvasLoad(it *item) *canvasDoc {
	doc := &canvasDoc{W: 120, H: 32}
	if m.db == nil {
		return doc
	}
	b, ok, err := database.GetBlob(m.db, it.uuid)
	if err != nil || !ok || b.Mime != canvasMime {
		return doc
	}
	var d canvasDoc
	if json.Unmarshal(b.Bytes, &d) != nil || d.W <= 0 || d.H <= 0 {
		return doc
	}
	return &d
}

// canvasSave persists the document. Anchors bake their current resolution
// into X/Y first, so an object deleted later degrades to absolute cleanly.
func (m *Model) canvasSave(it *item, doc *canvasDoc) {
	if m.db == nil || doc == nil {
		return
	}
	for i := range doc.Spans {
		doc.Spans[i].From.X, doc.Spans[i].From.Y = doc.resolve(doc.Spans[i].From)
		doc.Spans[i].To.X, doc.Spans[i].To.Y = doc.resolve(doc.Spans[i].To)
	}
	bs, err := json.Marshal(doc)
	if err != nil {
		return
	}
	_ = database.PutBlob(m.db, database.Blob{UUID: it.uuid, Mime: canvasMime, Bytes: bs, W: doc.W, H: doc.H})
}

// resolve turns an anchor into its current document coordinates.
func (d *canvasDoc) resolve(a canvasAnchor) (int, int) {
	if a.Obj != "" {
		for _, o := range d.Objs {
			if o.Name == a.Obj {
				return o.X + a.DX, o.Y + a.DY
			}
		}
	}
	return a.X, a.Y
}

// objAt returns the index of the object whose box contains (x,y), or -1.
func (d *canvasDoc) objAt(x, y int) int {
	for i := len(d.Objs) - 1; i >= 0; i-- {
		o := d.Objs[i]
		if x >= o.X && x < o.X+o.W && y >= o.Y && y < o.Y+o.H {
			return i
		}
	}
	return -1
}

// anchorAt builds the anchor for a point: object-relative when the point sits
// on or immediately around an object (so the span follows it), else absolute.
func (d *canvasDoc) anchorAt(x, y int) canvasAnchor {
	for i := len(d.Objs) - 1; i >= 0; i-- {
		o := d.Objs[i]
		if x >= o.X-1 && x <= o.X+o.W && y >= o.Y-1 && y <= o.Y+o.H {
			return canvasAnchor{Obj: o.Name, DX: x - o.X, DY: y - o.Y, X: x, Y: y}
		}
	}
	return canvasAnchor{X: x, Y: y}
}

// styled is one composited cell ready to print.
type canvasStyled struct {
	ch     string
	fg, bg string
}

// composite flattens the document into a cell map: object boxes first, then
// spans (constraints re-resolved now), then painted cells on top.
func (d *canvasDoc) composite() map[[2]int]canvasStyled {
	g := map[[2]int]canvasStyled{}
	put := func(x, y int, ch, fg, bg string) {
		if x < 0 || y < 0 || x >= d.W || y >= d.H || ch == "" {
			return
		}
		g[[2]int{x, y}] = canvasStyled{ch: ch, fg: fg, bg: bg}
	}
	for _, o := range d.Objs {
		x2, y2 := o.X+o.W-1, o.Y+o.H-1
		for x := o.X + 1; x < x2; x++ {
			put(x, o.Y, "─", "", "")
			put(x, y2, "─", "", "")
		}
		for y := o.Y + 1; y < y2; y++ {
			put(o.X, y, "│", "", "")
			put(x2, y, "│", "", "")
		}
		put(o.X, o.Y, "┌", "", "")
		put(x2, o.Y, "┐", "", "")
		put(o.X, y2, "└", "", "")
		put(x2, y2, "┘", "", "")
		for i, r := range o.Name { // name label on the top border
			put(o.X+1+i, o.Y, string(r), "cyan", "")
		}
	}
	for _, s := range d.Spans {
		x1, y1 := d.resolve(s.From)
		x2, y2 := d.resolve(s.To)
		for _, p := range lPath(x1, y1, x2, y2) {
			put(p[0], p[1], s.Ch, s.Fg, "")
		}
	}
	for _, c := range d.Cells {
		put(c.X, c.Y, c.Ch, c.Fg, c.Bg)
	}
	return g
}

// lPath is the span's cell path: horizontal first, then vertical.
func lPath(x1, y1, x2, y2 int) [][2]int {
	var out [][2]int
	step := func(a, b int) int {
		if a < b {
			return 1
		}
		return -1
	}
	for x := x1; x != x2; x += step(x1, x2) {
		out = append(out, [2]int{x, y1})
	}
	for y := y1; y != y2; y += step(y1, y2) {
		out = append(out, [2]int{x2, y})
	}
	return append(out, [2]int{x2, y2})
}

// setCell stamps (replacing any cell already there); an empty brush erases.
func (d *canvasDoc) setCell(x, y int, b canvasBrush) {
	for i := range d.Cells {
		if d.Cells[i].X == x && d.Cells[i].Y == y {
			d.Cells[i].Ch, d.Cells[i].Fg, d.Cells[i].Bg = b.Ch, b.Fg, b.Bg
			return
		}
	}
	d.Cells = append(d.Cells, canvasCell{X: x, Y: y, Ch: b.Ch, Fg: b.Fg, Bg: b.Bg})
}

// eraseAt removes the painted cell at (x,y); failing that, the first span
// whose path covers it. Reports whether anything went.
func (d *canvasDoc) eraseAt(x, y int) bool {
	for i := range d.Cells {
		if d.Cells[i].X == x && d.Cells[i].Y == y {
			d.Cells = append(d.Cells[:i], d.Cells[i+1:]...)
			return true
		}
	}
	for i := range d.Spans {
		x1, y1 := d.resolve(d.Spans[i].From)
		x2, y2 := d.resolve(d.Spans[i].To)
		for _, p := range lPath(x1, y1, x2, y2) {
			if p[0] == x && p[1] == y {
				d.Spans = append(d.Spans[:i], d.Spans[i+1:]...)
				return true
			}
		}
	}
	return false
}

// nextObjName hands out A, B, … Z, A1, B1, … — never reusing a live name.
func (d *canvasDoc) nextObjName() string {
	used := map[string]bool{}
	for _, o := range d.Objs {
		used[o.Name] = true
	}
	for round := 0; ; round++ {
		for c := 'A'; c <= 'Z'; c++ {
			n := string(c)
			if round > 0 {
				n = fmt.Sprintf("%c%d", c, round)
			}
			if !used[n] {
				return n
			}
		}
	}
}

// ── colors ──────────────────────────────────────────────────────────────────

// canvasFg maps a palette color name to its themed foreground SGR.
func canvasFg(name string) string {
	if name == "" {
		return ""
	}
	return styleColorCode[name]
}

// canvasBg derives a background SGR from the themed foreground code — the
// palette's "background blue" is the same hue as its "foreground blue".
func canvasBg(name string) string {
	fg := styleColorCode[name]
	return strings.Replace(fg, "[38;", "[48;", 1)
}

// crosshair tints: the cursor row and column wear a dim blue wash, the cursor
// cell itself a bright one — the CAD crosshair.
const (
	canvasCrossBG  = "\x1b[48;2;24;40;74m"
	canvasCursorBG = "\x1b[48;2;60;100;190m"
)

// ── the nodeView ─────────────────────────────────────────────────────────────

// canvasView is the canvas node's inline expanded editor (alt+e).
type canvasView struct{}

// canvasViewH is the on-screen canvas window height (document pans within).
const canvasViewH = 20

// canvasPalRows is how many palette matches the overlay shows.
const canvasPalRows = 8

func (canvasView) Enter(m *Model, it *item) bool {
	st := canvasStateOf(m, it)
	st.doc = m.canvasLoad(it) // reload: pick up external edits since last look
	st.mode, st.pal = "", false
	return true
}

func (canvasView) Leave(m *Model, it *item) {
	st := canvasStateOf(m, it)
	if st.doc != nil {
		m.canvasSave(it, st.doc)
	}
	// st.doc stays — it is the saved document, and the render/preview/context
	// surfaces read it as a cache instead of re-hitting the blob every frame
	st.mode, st.pal = "", false
}

func (canvasView) Lines(m *Model, it *item, width int) int {
	st := canvasStateOf(m, it)
	n := 1 + canvasViewH
	if st.pal {
		n += 1 + canvasPalRows
	}
	return n
}

// Key drives the painter while focused.
func (v canvasView) Key(m *Model, it *item, k tea.KeyMsg) (tea.Cmd, bool) {
	st := canvasStateOf(m, it)
	if st.doc == nil {
		return nil, false
	}
	if st.pal {
		return nil, v.paletteKey(st, k)
	}

	key := k.String()
	// move mode: arrows carry the grabbed object; everything re-derives
	if st.mode == "move" && st.moveIdx >= 0 && st.moveIdx < len(st.doc.Objs) {
		o := &st.doc.Objs[st.moveIdx]
		switch key {
		case "up", "k":
			if o.Y > 0 {
				o.Y--
				st.cy--
			}
		case "down", "j":
			o.Y++
			st.cy++
		case "left", "h":
			if o.X > 0 {
				o.X--
				st.cx--
			}
		case "right", "l":
			o.X++
			st.cx++
		case "m", "enter", " ", "space":
			st.mode = "" // drop it here
		case "D":
			// delete the object; spans anchored to it keep their last position
			// (resolve() falls back to the baked X/Y)
			for i := range st.doc.Spans {
				st.doc.Spans[i].From.X, st.doc.Spans[i].From.Y = st.doc.resolve(st.doc.Spans[i].From)
				st.doc.Spans[i].To.X, st.doc.Spans[i].To.Y = st.doc.resolve(st.doc.Spans[i].To)
				if st.doc.Spans[i].From.Obj == o.Name {
					st.doc.Spans[i].From.Obj = ""
				}
				if st.doc.Spans[i].To.Obj == o.Name {
					st.doc.Spans[i].To.Obj = ""
				}
			}
			st.doc.Objs = append(st.doc.Objs[:st.moveIdx], st.doc.Objs[st.moveIdx+1:]...)
			st.mode = ""
		default:
			return nil, false // esc & friends → central (Leave saves)
		}
		v.pan(st)
		return nil, true
	}

	switch key {
	case "up", "k":
		if st.cy > 0 {
			st.cy--
		}
	case "down", "j":
		if st.cy < st.doc.H-1 {
			st.cy++
		}
	case "left", "h":
		if st.cx > 0 {
			st.cx--
		}
	case "right", "l":
		if st.cx < st.doc.W-1 {
			st.cx++
		}
	case "enter", " ", "space":
		switch st.mode {
		case "rect":
			x1, y1, x2, y2 := rectNorm(st.ax, st.ay, st.cx, st.cy)
			if x2-x1 >= 1 && y2-y1 >= 1 {
				st.doc.Objs = append(st.doc.Objs, canvasObj{
					Name: st.doc.nextObjName(), X: x1, Y: y1, W: x2 - x1 + 1, H: y2 - y1 + 1,
				})
			}
			st.mode = ""
		case "span":
			st.doc.Spans = append(st.doc.Spans, canvasSpan{
				From: st.doc.anchorAt(st.ax, st.ay),
				To:   st.doc.anchorAt(st.cx, st.cy),
				Ch:   firstNonEmptyStr(st.brush.Ch, "─"),
				Fg:   st.brush.Fg,
			})
			st.mode = ""
		default:
			b := st.brush
			if b.Ch == "" {
				b.Ch = " " // a pure background brush stamps a colored space
			}
			st.doc.setCell(st.cx, st.cy, b)
		}
	case "x", "backspace", "delete":
		st.doc.eraseAt(st.cx, st.cy)
	case "p", "/":
		st.pal, st.palQ, st.palSel = true, "", 0
	case "r":
		st.mode, st.ax, st.ay = "rect", st.cx, st.cy
	case "s":
		st.mode, st.ax, st.ay = "span", st.cx, st.cy
	case "m":
		if i := st.doc.objAt(st.cx, st.cy); i >= 0 {
			st.mode, st.moveIdx = "move", i
		} else {
			m.flash = "no object under the cursor"
		}
	case "q": // cancel a pending rect/span without leaving the canvas
		st.mode = ""
	default:
		return nil, false // esc, ctrl+c … → central handling; Leave saves
	}
	v.pan(st)
	return nil, true
}

// paletteKey drives the search overlay; returns handled.
func (v canvasView) paletteKey(st *canvasState, k tea.KeyMsg) bool {
	switch k.String() {
	case "esc":
		st.pal = false
		return true
	case "enter":
		hits := canvasPaletteSearch(st.palQ)
		if st.palSel >= 0 && st.palSel < len(hits) {
			e := hits[st.palSel]
			switch e.cat {
			case "background":
				st.brush.Bg = canvasBgValue(e)
			case "foreground":
				st.brush.Fg = e.color
			default:
				st.brush.Ch = e.ch
			}
		}
		st.pal = false
		return true
	case "up":
		if st.palSel > 0 {
			st.palSel--
		}
		return true
	case "down":
		if st.palSel < canvasPalRows-1 {
			st.palSel++
		}
		return true
	case "backspace":
		if r := []rune(st.palQ); len(r) > 0 {
			st.palQ = string(r[:len(r)-1])
			st.palSel = 0
		}
		return true
	}
	if k.Type == tea.KeySpace && !k.Alt {
		st.palQ += " "
		st.palSel = 0
		return true
	}
	if k.Type == tea.KeyRunes && !k.Alt {
		st.palQ += string(k.Runes)
		st.palSel = 0
		return true
	}
	return false
}

// canvasBgValue maps a background palette entry to the brush's Bg color name
// ("" for the reset entry).
func canvasBgValue(e canvasPaletteEntry) string {
	if e.color == "none" {
		return ""
	}
	return e.color
}

// pan keeps the crosshair inside the viewport.
func (canvasView) pan(st *canvasState) {
	vw, vh := st.vw, st.vh
	if vw <= 0 {
		vw = 60
	}
	if vh <= 0 {
		vh = canvasViewH
	}
	if st.cx < st.vx {
		st.vx = st.cx
	}
	if st.cx >= st.vx+vw {
		st.vx = st.cx - vw + 1
	}
	if st.cy < st.vy {
		st.vy = st.cy
	}
	if st.cy >= st.vy+vh {
		st.vy = st.cy - vh + 1
	}
	if st.vx < 0 {
		st.vx = 0
	}
	if st.vy < 0 {
		st.vy = 0
	}
}

// Bands renders header + canvas window (+ palette overlay).
func (v canvasView) Bands(m *Model, it *item, rail string, width, scroll, winH int, focused bool) []string {
	st := canvasStateOf(m, it)
	if st.doc == nil {
		st.doc = m.canvasLoad(it)
	}
	doc := st.doc

	// viewport size from the real width; remember it for Key's panning
	vw := width - visibleWidth(rail) - 3
	if vw < 10 {
		vw = 10
	}
	if vw > doc.W {
		vw = doc.W
	}
	st.vw, st.vh = vw, canvasViewH
	v.pan(st)

	mode := ""
	switch st.mode {
	case "rect":
		mode = " · rect: enter drops the corner"
	case "span":
		mode = " · span: enter lands the line"
	case "move":
		if st.moveIdx >= 0 && st.moveIdx < len(doc.Objs) {
			mode = " · moving " + doc.Objs[st.moveIdx].Name + " · m drops · D deletes"
		}
	}
	brush := st.brush.Ch
	if brush == "" {
		brush = "·bg"
	}
	head := fmt.Sprintf("  canvas %d×%d · (%d,%d) · brush %s%s · p palette · r rect · s span · m move · x erase · esc save",
		doc.W, doc.H, st.cx, st.cy, brush, mode)
	var content []string
	content = append(content, clip(rail+cReset+cDim+head+cReset, width))

	grid := doc.composite()
	// the pending rect/span previews live: composite the ghost too
	if focused && st.mode == "rect" {
		x1, y1, x2, y2 := rectNorm(st.ax, st.ay, st.cx, st.cy)
		ghost := canvasDoc{W: doc.W, H: doc.H, Objs: []canvasObj{{Name: "?", X: x1, Y: y1, W: x2 - x1 + 1, H: y2 - y1 + 1}}}
		for p, c := range ghost.composite() {
			if _, taken := grid[p]; !taken {
				c.fg = "blue"
				grid[p] = c
			}
		}
	}
	if focused && st.mode == "span" {
		for _, p := range lPath(st.ax, st.ay, st.cx, st.cy) {
			if _, taken := grid[[2]int{p[0], p[1]}]; !taken {
				grid[[2]int{p[0], p[1]}] = canvasStyled{ch: firstNonEmptyStr(st.brush.Ch, "─"), fg: "blue"}
			}
		}
	}

	for row := 0; row < canvasViewH; row++ {
		y := st.vy + row
		var b strings.Builder
		b.WriteString(rail + cReset + "  ")
		for col := 0; col < vw; col++ {
			x := st.vx + col
			if y >= doc.H || x >= doc.W {
				b.WriteString(" ")
				continue
			}
			cell, ok := grid[[2]int{x, y}]
			ch := cell.ch
			if !ok || ch == "" {
				ch = " "
			}
			var sgr string
			if cell.bg != "" {
				sgr += canvasBg(cell.bg)
			}
			// the crosshair: cursor row+column tinted, the cursor cell bright
			if focused {
				switch {
				case x == st.cx && y == st.cy:
					sgr = canvasCursorBG
				case cell.bg == "" && (x == st.cx || y == st.cy):
					sgr = canvasCrossBG
				}
			}
			if cell.fg != "" {
				sgr += canvasFg(cell.fg)
			}
			if sgr == "" {
				b.WriteString(ch)
			} else {
				b.WriteString(sgr + ch + "\x1b[49m" + cReset)
			}
		}
		content = append(content, clip(b.String(), width))
	}

	if st.pal {
		content = append(content, clip(rail+cReset+cDim+"  palette › "+cReset+cFG+st.palQ+cReset+cDim+"▏ enter picks · esc closes"+cReset, width))
		hits := canvasPaletteSearch(st.palQ)
		for i := 0; i < canvasPalRows; i++ {
			line := rail + cReset + "  "
			if i < len(hits) {
				e := hits[i]
				marker, style := "  ", cDim
				if i == st.palSel {
					marker, style = cAccent+"▸ "+cReset, cFG
				}
				sample := e.ch
				switch e.cat {
				case "background":
					sample = canvasBg(e.color) + "   " + cReset
					if e.color == "none" {
						sample = "   "
					}
				case "foreground":
					sample = canvasFg(e.color) + "██▌" + cReset
				default:
					sample = " " + e.ch + " "
				}
				line += marker + sample + " " + style + e.cat + " · " + e.name + cReset
			}
			content = append(content, clip(line, width))
		}
	}

	if scroll > len(content) {
		scroll = len(content)
	}
	end := scroll + winH
	if end > len(content) {
		end = len(content)
	}
	return content[scroll:end]
}

func rectNorm(x1, y1, x2, y2 int) (int, int, int, int) {
	if x2 < x1 {
		x1, x2 = x2, x1
	}
	if y2 < y1 {
		y1, y2 = y2, y1
	}
	return x1, y1, x2, y2
}

func firstNonEmptyStr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// ── outline surfaces ─────────────────────────────────────────────────────────

// canvasRender is the collapsed one-line body: caption + a dim size summary.
func (m *Model) canvasRender(it *item) string {
	doc := m.canvasDocOf(it)
	parts := fmt.Sprintf("%d×%d", doc.W, doc.H)
	if n := len(doc.Cells); n > 0 {
		parts += fmt.Sprintf(" · %d cells", n)
	}
	if n := len(doc.Objs); n > 0 {
		parts += fmt.Sprintf(" · %d objects", n)
	}
	if n := len(doc.Spans); n > 0 {
		parts += fmt.Sprintf(" · %d spans", n)
	}
	name := strings.TrimSpace(it.name)
	if name != "" {
		return name + " " + cDim + "▦ " + parts + cReset
	}
	return cDim + "▦ canvas " + parts + cReset
}

// canvasText renders the document as plain text lines (trailing space
// trimmed) — the agent-context body and the base of tests.
func (d *canvasDoc) canvasText() string {
	grid := d.composite()
	maxY := 0
	for p := range grid {
		if p[1] > maxY {
			maxY = p[1]
		}
	}
	var lines []string
	for y := 0; y <= maxY && y < d.H; y++ {
		var b strings.Builder
		for x := 0; x < d.W; x++ {
			if c, ok := grid[[2]int{x, y}]; ok && c.ch != "" {
				b.WriteString(c.ch)
			} else {
				b.WriteString(" ")
			}
		}
		lines = append(lines, strings.TrimRight(b.String(), " "))
	}
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return strings.Join(lines, "\n")
}

// canvasToContext ships the drawing itself — the text grid — as the element
// body, so an agent sees the picture, not a byte count.
func (m *Model) canvasToContext(it *item) contextXML {
	doc := m.canvasDocOf(it)
	return contextXML{tag: "canvas", attrs: fmt.Sprintf(`w="%d" h="%d"`, doc.W, doc.H), body: doc.canvasText()}
}

// canvasBandLines is the always-on inline preview (like the image thumbnail):
// the drawing rendered beneath the node, capped to its used height.
func (m *Model) canvasBandLines(r row, subtreeBelow bool, maxLine int) []string {
	if m.focused && m.cursorItem() == r.it {
		return nil // the focused editor already draws the canvas
	}
	doc := m.canvasDocOf(r.it)
	text := doc.canvasText()
	if strings.TrimSpace(text) == "" {
		return nil
	}
	rail := continuationPrefix(r, subtreeBelow)
	lines := strings.Split(text, "\n")
	const previewCap = 12
	if len(lines) > previewCap {
		lines = append(lines[:previewCap], "…")
	}
	out := make([]string, 0, len(lines))
	for _, l := range lines {
		out = append(out, clip(rail+cReset+"  "+cDim+l+cReset, maxLine))
	}
	return out
}

// ── palette ──────────────────────────────────────────────────────────────────

// canvasPaletteEntry is one named, searchable palette item: a glyph, or a
// color (background/foreground categories).
type canvasPaletteEntry struct {
	cat   string
	name  string
	ch    string
	color string // background/foreground entries
}

// canvasPaletteSearch fuzzy-filters the catalog: every query word must
// subsequence-match "cat name". Exact substring hits rank first.
func canvasPaletteSearch(q string) []canvasPaletteEntry {
	q = strings.ToLower(strings.TrimSpace(q))
	if q == "" {
		return canvasPalette[:min(len(canvasPalette), 64)]
	}
	words := strings.Fields(q)
	type scored struct {
		e     canvasPaletteEntry
		score int
	}
	var hits []scored
	for _, e := range canvasPalette {
		hay := e.cat + " " + e.name
		ok, sc := true, 0
		for _, w := range words {
			if strings.Contains(hay, w) {
				sc += 2
				continue
			}
			if fuzzyMatch(hay, w) {
				sc++
				continue
			}
			ok = false
			break
		}
		if ok {
			hits = append(hits, scored{e, sc})
		}
	}
	sort.SliceStable(hits, func(i, j int) bool { return hits[i].score > hits[j].score })
	out := make([]canvasPaletteEntry, 0, len(hits))
	for _, h := range hits {
		out = append(out, h.e)
	}
	return out
}
