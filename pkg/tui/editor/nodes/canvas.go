package nodes

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lflow/lflow/pkg/tui/database"
	"github.com/lflow/lflow/pkg/tui/editor"
)

// The canvas node: a character-grid drawing on two planes, edited like a tiny
// CAD. alt+e opens the inline painter (bands — never a separate screen) with
// a blue crosshair over ABSOLUTE grid coordinates: a cell placed at (12,4)
// stays at (12,4) whatever the terminal size; the viewport pans, the document
// never reflows.
//
//   - PAINTER (green badge): free painting — glyphs, colors, half blocks.
//   - OBJECT (light purple badge): items place exactly the same, and colored
//     BACKGROUNDS define the objects — one background color is one object,
//     grouping every item painted on it. The one constraint is DISTANCE:
//     t on one object, t on another draws a white line between them labeled
//     with the distance as a percentage of the canvas width.
//
// tab switches planes. The palette is a GRID pinned to the bottom of the
// editor: p focuses it, arrows move through it, typing filters, enter picks.
// The document persists as JSON in node_blobs; the outline shows a single
// summary line (never a multi-line preview).

const canvasMime = "application/x-lflow-canvas+json"

// canvasCell is one painted cell: a glyph and optional colors. On the object
// plane Bg is the OBJECT — background cells define the region, and items
// stamped on a region carry its color so they travel with it.
type canvasCell struct {
	X  int    `json:"x"`
	Y  int    `json:"y"`
	Ch string `json:"ch"`
	Fg string `json:"fg,omitempty"`
	Bg string `json:"bg,omitempty"`
}

// canvasDist is the distance constraint: a white line between two objects'
// centers, labeled with the distance as % of the canvas width. Pct remembers
// the declared distance so a re-solve can restore it.
type canvasDist struct {
	A   string  `json:"a"`
	B   string  `json:"b"`
	Pct float64 `json:"pct"`
}

// canvasDoc is the persisted document. All coordinates are absolute.
type canvasDoc struct {
	W       int          `json:"w"`
	H       int          `json:"h"`
	Cells   []canvasCell `json:"cells,omitempty"`   // painter plane
	Objects []canvasCell `json:"objects,omitempty"` // object plane: Bg IS the object
	Dists   []canvasDist `json:"dists,omitempty"`
}

// canvasObj is a derived object: the bounding box of one background color's
// cells on the object plane.
type canvasObj struct {
	Color                  string
	MinX, MinY, MaxX, MaxY int
}

func (o canvasObj) center() (float64, float64) {
	return float64(o.MinX+o.MaxX) / 2, float64(o.MinY+o.MaxY) / 2
}

// objects groups the object plane's background regions by color.
func (d *canvasDoc) objects() []canvasObj {
	byColor := map[string]*canvasObj{}
	var order []string
	for _, c := range d.Objects {
		if c.Bg == "" {
			continue // an unattached item is not an object
		}
		o := byColor[c.Bg]
		if o == nil {
			byColor[c.Bg] = &canvasObj{Color: c.Bg, MinX: c.X, MinY: c.Y, MaxX: c.X, MaxY: c.Y}
			order = append(order, c.Bg)
			continue
		}
		o.MinX, o.MaxX = min(o.MinX, c.X), max(o.MaxX, c.X)
		o.MinY, o.MaxY = min(o.MinY, c.Y), max(o.MaxY, c.Y)
	}
	out := make([]canvasObj, 0, len(order))
	for _, c := range order {
		out = append(out, *byColor[c])
	}
	return out
}

func (d *canvasDoc) objectByColor(color string) (canvasObj, bool) {
	for _, o := range d.objects() {
		if o.Color == color {
			return o, true
		}
	}
	return canvasObj{}, false
}

// objectColorAt returns the object (background) color under a point, or "".
func (d *canvasDoc) objectColorAt(x, y int) string {
	for _, c := range d.Objects {
		if c.X == x && c.Y == y && c.Bg != "" {
			return c.Bg
		}
	}
	return ""
}

// distance returns a constraint's current center distance in cells and as a
// percentage of the canvas width.
func (d *canvasDoc) distance(t canvasDist) (cells, pct float64, ok bool) {
	a, okA := d.objectByColor(t.A)
	b, okB := d.objectByColor(t.B)
	if !okA || !okB || d.W == 0 {
		return 0, 0, false
	}
	ax, ay := a.center()
	bx, by := b.center()
	cells = math.Hypot(bx-ax, by-ay)
	return cells, cells / float64(d.W) * 100, true
}

// translateObject shifts one object — its background cells and every item
// riding it — clamped into the canvas.
func (d *canvasDoc) translateObject(color string, dx, dy int) {
	if dx == 0 && dy == 0 {
		return
	}
	o, ok := d.objectByColor(color)
	if !ok {
		return
	}
	if o.MinX+dx < 0 {
		dx = -o.MinX
	}
	if o.MaxX+dx > d.W-1 {
		dx = d.W - 1 - o.MaxX
	}
	if o.MinY+dy < 0 {
		dy = -o.MinY
	}
	if o.MaxY+dy > d.H-1 {
		dy = d.H - 1 - o.MaxY
	}
	for i := range d.Objects {
		if d.Objects[i].Bg == color {
			d.Objects[i].X += dx
			d.Objects[i].Y += dy
		}
	}
}

// solve nudges each constraint's B object along the AB line until the center
// distance matches the declared percentage again.
func (d *canvasDoc) solve() {
	for pass := 0; pass < 4; pass++ {
		moved := false
		for _, t := range d.Dists {
			a, okA := d.objectByColor(t.A)
			b, okB := d.objectByColor(t.B)
			if !okA || !okB {
				continue
			}
			ax, ay := a.center()
			bx, by := b.center()
			cur := math.Hypot(bx-ax, by-ay)
			want := t.Pct / 100 * float64(d.W)
			if cur == 0 || math.Abs(cur-want) < 0.75 {
				continue
			}
			ux, uy := (bx-ax)/cur, (by-ay)/cur
			dx := int(math.Round(ux * (want - cur)))
			dy := int(math.Round(uy * (want - cur)))
			if dx == 0 && dy == 0 {
				continue
			}
			moved = true
			d.translateObject(t.B, dx, dy)
		}
		if !moved {
			return
		}
	}
}

// setCell stamps into a plane, replacing any cell already there.
func setCell(cells *[]canvasCell, c canvasCell) {
	for i := range *cells {
		if (*cells)[i].X == c.X && (*cells)[i].Y == c.Y {
			(*cells)[i] = c
			return
		}
	}
	*cells = append(*cells, c)
}

// eraseCell removes a plane's cell at (x,y).
func eraseCell(cells *[]canvasCell, x, y int) bool {
	for i := range *cells {
		if (*cells)[i].X == x && (*cells)[i].Y == y {
			*cells = append((*cells)[:i], (*cells)[i+1:]...)
			return true
		}
	}
	return false
}

// linePoints is the straight cell path between two points (Bresenham).
func linePoints(x1, y1, x2, y2 int) [][2]int {
	dx, dy := abs(x2-x1), -abs(y2-y1)
	sx, sy := 1, 1
	if x1 > x2 {
		sx = -1
	}
	if y1 > y2 {
		sy = -1
	}
	err := dx + dy
	var out [][2]int
	x, y := x1, y1
	for {
		out = append(out, [2]int{x, y})
		if x == x2 && y == y2 {
			return out
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
	}
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// ── persistence ─────────────────────────────────────────────────────────────

type canvasBrush struct{ Ch, Fg, Bg string }

type canvasState struct {
	doc    *canvasDoc
	plane  string // "draw" | "object"
	cx, cy int
	vx, vy int
	vw, vh int

	brush canvasBrush

	distFrom string // pending distance constraint: the first object's color
	distSel  int    // selected constraint for D delete

	palFocus bool   // the bottom palette grid holds the keys
	palQ     string // type-to-filter query
	palSel   int    // index into the filtered entries
}

func canvasStateOf(h editor.NodeHost, uuid string) *canvasState {
	d := h.NodeStore(uuid)
	st, _ := d["canvas"].(*canvasState)
	if st == nil {
		st = &canvasState{brush: canvasBrush{Ch: "─"}, plane: "draw", distSel: -1}
		d["canvas"] = st
	}
	return st
}

func canvasLoad(h editor.NodeHost, uuid string) *canvasDoc {
	doc := &canvasDoc{W: 120, H: 32}
	db := h.NodeDB()
	if db == nil {
		return doc
	}
	b, ok, err := database.GetBlob(db, uuid)
	if err != nil || !ok || b.Mime != canvasMime {
		return doc
	}
	var d canvasDoc
	if json.Unmarshal(b.Bytes, &d) != nil || d.W <= 0 || d.H <= 0 {
		return doc
	}
	return &d
}

func canvasSave(h editor.NodeHost, uuid string, doc *canvasDoc) {
	db := h.NodeDB()
	if db == nil || doc == nil {
		return
	}
	if bs, err := json.Marshal(doc); err == nil {
		_ = database.PutBlob(db, database.Blob{UUID: uuid, Mime: canvasMime, Bytes: bs, W: doc.W, H: doc.H})
	}
}

func canvasDocOf(h editor.NodeHost, uuid string) *canvasDoc {
	st := canvasStateOf(h, uuid)
	if st.doc == nil {
		st.doc = canvasLoad(h, uuid)
	}
	return st.doc
}

// ── registration ────────────────────────────────────────────────────────────

func init() {
	editor.RegisterNodePlugin(editor.NodePlugin{
		Key: database.TypeCanvas, Label: "Canvas",
		Glyph:  func() (string, string) { return "▦", editor.NodeTheme().Cyan },
		Render: canvasRender,
		View:   canvasView{},
		ToContext: func(h editor.NodeHost, n editor.NodeRef) (string, string, string) {
			doc := canvasDocOf(h, n.UUID())
			return "canvas", fmt.Sprintf(`w="%d" h="%d"`, doc.W, doc.H), canvasText(doc)
		},
	})
}

// canvasRender is the outline line — a SINGLE summary line, like an image's.
func canvasRender(h editor.NodeHost, n editor.NodeRef) string {
	th := editor.NodeTheme()
	doc := canvasDocOf(h, n.UUID())
	parts := fmt.Sprintf("%d×%d", doc.W, doc.H)
	if c := len(doc.Cells); c > 0 {
		parts += fmt.Sprintf(" · %d cells", c)
	}
	if o := len(doc.objects()); o > 0 {
		parts += fmt.Sprintf(" · %d objects", o)
	}
	if t := len(doc.Dists); t > 0 {
		parts += fmt.Sprintf(" · %d distances", t)
	}
	name := strings.TrimSpace(n.Text())
	if name != "" {
		return name + " " + th.Dim + parts + th.Reset
	}
	return th.Dim + "canvas " + parts + th.Reset
}

// canvasText renders the document as plain text (painter cells over object
// items over region blocks) plus the constraint list — what agents receive.
func canvasText(d *canvasDoc) string {
	grid := map[[2]int]string{}
	for _, c := range d.Objects {
		ch := c.Ch
		if ch == "" || ch == " " {
			ch = "▓"
		}
		grid[[2]int{c.X, c.Y}] = ch
	}
	for _, c := range d.Cells {
		if c.Ch != "" && c.Ch != " " {
			grid[[2]int{c.X, c.Y}] = c.Ch
		}
	}
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
			if ch, ok := grid[[2]int{x, y}]; ok {
				b.WriteString(ch)
			} else {
				b.WriteString(" ")
			}
		}
		lines = append(lines, strings.TrimRight(b.String(), " "))
	}
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	for _, t := range d.Dists {
		if _, pct, ok := d.distance(t); ok {
			lines = append(lines, fmt.Sprintf("distance: %s ↔ %s = %.0f%% of width", t.A, t.B, pct))
		}
	}
	return strings.Join(lines, "\n")
}

// ── the painter (alt+e) ─────────────────────────────────────────────────────

type canvasView struct{}

const (
	canvasViewH    = 18
	canvasPalRows  = 4
	canvasCrossBG  = "\x1b[48;2;24;40;74m"
	canvasCursorBG = "\x1b[48;2;60;100;190m"
	// the mode badges: uppercase white on green (PAINTER) / light purple (OBJECT)
	canvasBadgePainter = "\x1b[1m\x1b[38;2;255;255;255m\x1b[48;2;35;134;54m PAINTER \x1b[0m"
	canvasBadgeObject  = "\x1b[1m\x1b[38;2;255;255;255m\x1b[48;2;155;126;222m OBJECT \x1b[0m"
	canvasWhite        = "\x1b[38;2;235;235;235m"
)

func (canvasView) Enter(h editor.NodeHost, n editor.NodeRef) bool {
	st := canvasStateOf(h, n.UUID())
	st.doc = canvasLoad(h, n.UUID()) // reload: pick up external edits
	st.palFocus, st.distFrom, st.distSel = false, "", -1
	return true
}

func (canvasView) Leave(h editor.NodeHost, n editor.NodeRef) {
	st := canvasStateOf(h, n.UUID())
	if st.doc != nil {
		canvasSave(h, n.UUID(), st.doc)
	}
	st.palFocus, st.distFrom = false, ""
}

func (v canvasView) Lines(h editor.NodeHost, n editor.NodeRef, width int) int {
	st := canvasStateOf(h, n.UUID())
	return len(v.headerLines(st, width)) + canvasViewH + 1 + canvasPalRows
}

func (v canvasView) Key(h editor.NodeHost, n editor.NodeRef, k tea.KeyMsg) (tea.Cmd, bool) {
	st := canvasStateOf(h, n.UUID())
	if st.doc == nil {
		return nil, false
	}
	if st.palFocus {
		return nil, v.paletteKey(st, k)
	}
	doc := st.doc

	switch k.String() {
	case "up", "k":
		if st.cy > 0 {
			st.cy--
		}
	case "down", "j":
		if st.cy < doc.H-1 {
			st.cy++
		}
	case "left", "h":
		if st.cx > 0 {
			st.cx--
		}
	case "right", "l":
		if st.cx < doc.W-1 {
			st.cx++
		}
	case "tab":
		if st.plane == "draw" {
			st.plane = "object"
		} else {
			st.plane = "draw"
		}
		st.distFrom = ""
	case "p":
		st.palFocus, st.palQ, st.palSel = true, "", 0
	case "enter", " ", "space":
		b := st.brush
		if b.Ch == "" {
			b.Ch = " "
		}
		if st.plane == "object" {
			cell := canvasCell{X: st.cx, Y: st.cy, Ch: b.Ch, Fg: b.Fg, Bg: b.Bg}
			if b.Bg == "" {
				// an item rides whatever region it lands on — that region's
				// color is its group
				cell.Bg = doc.objectColorAt(st.cx, st.cy)
			}
			setCell(&doc.Objects, cell)
			doc.solve()
		} else {
			setCell(&doc.Cells, canvasCell{X: st.cx, Y: st.cy, Ch: b.Ch, Fg: b.Fg, Bg: b.Bg})
		}
	case "x", "backspace", "delete":
		if st.plane == "object" {
			if eraseCell(&doc.Objects, st.cx, st.cy) {
				doc.solve()
			}
		} else {
			eraseCell(&doc.Cells, st.cx, st.cy)
		}
	case "t":
		if st.plane != "object" {
			h.NodeFlash("distances live on the object plane · tab")
			break
		}
		color := doc.objectColorAt(st.cx, st.cy)
		if color == "" {
			h.NodeFlash("no object under the cursor · paint a colored background first")
			break
		}
		if st.distFrom == "" {
			st.distFrom = color
			break
		}
		from := st.distFrom
		st.distFrom = ""
		if from == color {
			break
		}
		t := canvasDist{A: from, B: color}
		if _, pct, ok := doc.distance(t); ok {
			t.Pct = pct
			doc.Dists = append(doc.Dists, t)
			st.distSel = len(doc.Dists) - 1
		}
	case "T":
		if len(doc.Dists) > 0 {
			st.distSel = (st.distSel + 1) % len(doc.Dists)
		}
	case "+", "=":
		if st.distSel >= 0 && st.distSel < len(doc.Dists) {
			doc.Dists[st.distSel].Pct++
			doc.solve()
		}
	case "-":
		if st.distSel >= 0 && st.distSel < len(doc.Dists) {
			if doc.Dists[st.distSel].Pct > 1 {
				doc.Dists[st.distSel].Pct--
			}
			doc.solve()
		}
	case "D":
		if st.distSel >= 0 && st.distSel < len(doc.Dists) {
			doc.Dists = append(doc.Dists[:st.distSel], doc.Dists[st.distSel+1:]...)
			st.distSel = -1
		}
	default:
		return nil, false // esc & friends → central (Leave saves)
	}
	v.pan(st)
	return nil, true
}

// paletteKey drives the bottom grid while it holds focus.
func (v canvasView) paletteKey(st *canvasState, k tea.KeyMsg) bool {
	hits := canvasPaletteSearch(st.palQ)
	cols := st.palCols()
	switch k.String() {
	case "esc", "p":
		st.palFocus = false
		return true
	case "enter", " ", "space":
		if st.palSel >= 0 && st.palSel < len(hits) {
			e := hits[st.palSel]
			switch e.cat {
			case "background":
				if e.color == "none" {
					st.brush.Bg = ""
				} else {
					st.brush.Bg = e.color
					st.brush.Ch = " " // a background pick paints regions
				}
			case "foreground":
				st.brush.Fg = e.color
			default:
				st.brush.Ch = e.ch
				st.brush.Bg = "" // a glyph pick paints items, not regions
			}
		}
		st.palFocus = false
		return true
	case "left":
		if st.palSel > 0 {
			st.palSel--
		}
		return true
	case "right":
		if st.palSel < len(hits)-1 {
			st.palSel++
		}
		return true
	case "up":
		if st.palSel-cols >= 0 {
			st.palSel -= cols
		}
		return true
	case "down":
		if st.palSel+cols < len(hits) {
			st.palSel += cols
		}
		return true
	case "backspace":
		if r := []rune(st.palQ); len(r) > 0 {
			st.palQ = string(r[:len(r)-1])
			st.palSel = 0
		}
		return true
	}
	if k.Type == tea.KeyRunes && !k.Alt {
		st.palQ += string(k.Runes)
		st.palSel = 0
		return true
	}
	return false
}

// palCols is how many grid slots fit per palette row (each 4 cells wide).
func (st *canvasState) palCols() int {
	vw := st.vw
	if vw <= 0 {
		vw = 60
	}
	c := vw / 4
	if c < 1 {
		c = 1
	}
	return c
}

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

// canvasBg derives a background SGR from a themed foreground code.
func canvasBg(name string) string {
	return strings.Replace(editor.NodeColor(name), "[38;", "[48;", 1)
}

// headerLines composes the header and WRAPS it (never truncates): the badge
// plus " · " separated parts flow onto as many lines as they need.
func (v canvasView) headerLines(st *canvasState, width int) []string {
	th := editor.NodeTheme()
	doc := st.doc
	badge := canvasBadgePainter
	if st.plane == "object" {
		badge = canvasBadgeObject
	}
	parts := []string{fmt.Sprintf("(%d,%d)", st.cx, st.cy)}
	if st.plane == "object" {
		brush := "item " + editor.NodeFirstNonEmpty(st.brush.Ch, "─")
		if st.brush.Bg != "" {
			brush = "object " + editor.NodeColor(st.brush.Bg) + st.brush.Bg + th.Reset + th.Dim
		}
		parts = append(parts, brush)
		switch {
		case st.distFrom != "":
			parts = append(parts, "distance from "+st.distFrom+" → t on the other object")
		case doc != nil && st.distSel >= 0 && st.distSel < len(doc.Dists):
			t := doc.Dists[st.distSel]
			parts = append(parts, fmt.Sprintf("distance %s↔%s %.0f%%", t.A, t.B, t.Pct), "+/- adjust", "D delete")
		default:
			parts = append(parts, "t distance", "T select")
		}
	} else {
		brush := st.brush.Ch
		if brush == "" {
			brush = "·bg"
		}
		parts = append(parts, "brush "+brush, "x erase")
	}
	parts = append(parts, "p palette", "tab plane", "esc save")

	var lines []string
	cur := " " + badge + th.Dim
	curW := 1 + editor.NodeVisibleWidth(badge)
	for _, p := range parts {
		pw := editor.NodeVisibleWidth(p) + 3
		if curW+pw > width-2 && curW > 0 {
			lines = append(lines, cur+th.Reset)
			cur, curW = " "+th.Dim, 1
		}
		cur += " · " + p
		curW += pw
	}
	lines = append(lines, cur+th.Reset)
	return lines
}

func (v canvasView) Bands(h editor.NodeHost, n editor.NodeRef, rail string, width, scroll, winH int, focused bool) []string {
	th := editor.NodeTheme()
	st := canvasStateOf(h, n.UUID())
	if st.doc == nil {
		st.doc = canvasLoad(h, n.UUID())
	}
	doc := st.doc

	vw := width - editor.NodeVisibleWidth(rail) - 3
	if vw < 10 {
		vw = 10
	}
	if vw > doc.W {
		vw = doc.W
	}
	st.vw, st.vh = vw, canvasViewH
	v.pan(st)

	var content []string
	for _, hl := range v.headerLines(st, width-editor.NodeVisibleWidth(rail)) {
		content = append(content, editor.NodeClip(rail+th.Reset+hl, width))
	}

	// composite: object regions + items, then painter cells, then the white
	// distance lines with their % labels on top
	type cell struct{ ch, fg, bg string }
	grid := map[[2]int]cell{}
	for _, c := range doc.Objects {
		ch := c.Ch
		if ch == "" {
			ch = " "
		}
		grid[[2]int{c.X, c.Y}] = cell{ch: ch, fg: c.Fg, bg: c.Bg}
	}
	for _, c := range doc.Cells {
		if c.Ch != "" {
			grid[[2]int{c.X, c.Y}] = cell{ch: c.Ch, fg: c.Fg, bg: c.Bg}
		}
	}
	for i, t := range doc.Dists {
		a, okA := doc.objectByColor(t.A)
		b, okB := doc.objectByColor(t.B)
		if !okA || !okB {
			continue
		}
		ax, ay := a.center()
		bx, by := b.center()
		pts := linePoints(int(math.Round(ax)), int(math.Round(ay)), int(math.Round(bx)), int(math.Round(by)))
		for _, p := range pts {
			if c, taken := grid[p]; taken && c.bg != "" {
				continue // the line stops at the objects, not over them
			}
			grid[p] = cell{ch: "·", fg: "white"}
		}
		// the % label sits at the line's midpoint
		_, pct, _ := doc.distance(t)
		label := fmt.Sprintf("%.0f%%", pct)
		if i == st.distSel {
			label = "[" + label + "]"
		}
		mid := pts[len(pts)/2]
		for j, r := range label {
			grid[[2]int{mid[0] + j - len(label)/2, mid[1]}] = cell{ch: string(r), fg: "white"}
		}
	}

	for row := 0; row < canvasViewH; row++ {
		y := st.vy + row
		var b strings.Builder
		b.WriteString(rail + th.Reset + "  ")
		for col := 0; col < vw; col++ {
			x := st.vx + col
			if y >= doc.H || x >= doc.W {
				b.WriteString(" ")
				continue
			}
			c, ok := grid[[2]int{x, y}]
			ch := c.ch
			if !ok || ch == "" {
				ch = " "
			}
			var sgr string
			if c.bg != "" {
				sgr += canvasBg(c.bg)
			}
			if focused && !st.palFocus {
				switch {
				case x == st.cx && y == st.cy:
					sgr = canvasCursorBG
				case c.bg == "" && (x == st.cx || y == st.cy):
					sgr = canvasCrossBG
				}
			}
			if c.fg == "white" {
				sgr += canvasWhite
			} else if c.fg != "" {
				sgr += editor.NodeColor(c.fg)
			}
			if sgr == "" {
				b.WriteString(ch)
			} else {
				b.WriteString(sgr + ch + "\x1b[49m" + th.Reset)
			}
		}
		content = append(content, editor.NodeClip(b.String(), width))
	}

	content = append(content, v.paletteBands(st, rail, width)...)
	return editor.NodeWindowBands(content, scroll, winH)
}

// paletteBands renders the always-visible palette GRID pinned under the
// canvas: a filter line, then rows of glyph/color swatches. When focused the
// selection is highlighted and arrows walk the grid.
func (v canvasView) paletteBands(st *canvasState, rail string, width int) []string {
	th := editor.NodeTheme()
	hits := canvasPaletteSearch(st.palQ)
	cols := st.palCols()

	head := rail + th.Reset + th.Dim + "  palette"
	if st.palFocus {
		head += " › " + th.Reset + th.FG + st.palQ + th.Reset + th.Dim + "▏ type to filter · enter picks · esc back"
	} else {
		head += " · p to focus"
	}
	out := []string{editor.NodeClip(head+th.Reset, width)}

	// keep the selection's row visible inside the fixed grid height
	selRow := 0
	if cols > 0 {
		selRow = st.palSel / cols
	}
	topRow := 0
	if selRow >= canvasPalRows {
		topRow = selRow - canvasPalRows + 1
	}
	for r := 0; r < canvasPalRows; r++ {
		var b strings.Builder
		b.WriteString(rail + th.Reset + "  ")
		for c := 0; c < cols; c++ {
			i := (topRow+r)*cols + c
			if i >= len(hits) {
				b.WriteString("    ")
				continue
			}
			e := hits[i]
			sample := " " + e.ch + " "
			switch e.cat {
			case "background":
				if e.color == "none" {
					sample = " ∅ "
				} else {
					sample = canvasBg(e.color) + "   " + th.Reset
				}
			case "foreground":
				sample = editor.NodeColor(e.color) + " ■ " + th.Reset
			}
			if st.palFocus && i == st.palSel {
				b.WriteString(th.Accent + "▐" + th.Reset + sample + th.Accent + "▌" + th.Reset)
			} else {
				b.WriteString(" " + sample + " ")
			}
		}
		out = append(out, editor.NodeClip(b.String(), width))
	}
	return out
}
