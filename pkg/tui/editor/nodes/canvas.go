package nodes

import (
	"encoding/json"
	"fmt"
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
//   - the DRAW plane paints freely from the searchable named palette (p):
//     glyphs, foreground and background colors, half blocks, shades, …
//   - the OBJECT plane paints solid color regions where a DISTINCT COLOR IS a
//     DISTINCT OBJECT. The only other thing the object plane does is TIE
//     constraints: object edge ↔ object edge, or object edge ↔ the canvas
//     border, each holding a gap. The solver translates objects to keep every
//     tie satisfied — adjust a gap and the shape layout re-flows.
//
// tab switches planes. The document persists as JSON in node_blobs; the
// outline shows a single summary line (never a multi-line preview).

const canvasMime = "application/x-lflow-canvas+json"

// canvasCell is one painted cell: a glyph and optional colors.
type canvasCell struct {
	X  int    `json:"x"`
	Y  int    `json:"y"`
	Ch string `json:"ch"`
	Fg string `json:"fg,omitempty"`
	Bg string `json:"bg,omitempty"`
}

// canvasEnd is one side of a tie: an object (a color name) or the border,
// plus which edge.
type canvasEnd struct {
	Obj  string `json:"obj"`  // color name, or "border"
	Edge string `json:"edge"` // left | right | top | bottom
}

// canvasTie is one constraint: From's edge sits Gap cells from To's edge
// along the edge axis (signed: fromEdge = toEdge + Gap). The solver moves
// From's object; the border never moves.
type canvasTie struct {
	From canvasEnd `json:"from"`
	To   canvasEnd `json:"to"`
	Gap  int       `json:"gap"`
}

// canvasDoc is the persisted document. All coordinates are absolute.
type canvasDoc struct {
	W       int          `json:"w"`
	H       int          `json:"h"`
	Cells   []canvasCell `json:"cells,omitempty"`   // draw plane
	Objects []canvasCell `json:"objects,omitempty"` // object plane: Fg IS the object
	Ties    []canvasTie  `json:"ties,omitempty"`
}

// canvasObj is a derived object: the bounding box of one color's cells.
type canvasObj struct {
	Color                  string
	MinX, MinY, MaxX, MaxY int
}

// objects groups the object plane by color.
func (d *canvasDoc) objects() []canvasObj {
	byColor := map[string]*canvasObj{}
	var order []string
	for _, c := range d.Objects {
		o := byColor[c.Fg]
		if o == nil {
			o = &canvasObj{Color: c.Fg, MinX: c.X, MinY: c.Y, MaxX: c.X, MaxY: c.Y}
			byColor[c.Fg] = o
			order = append(order, c.Fg)
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

// edgeCoord is an end's edge position along its axis. Border edges are the
// canvas bounds.
func (d *canvasDoc) edgeCoord(e canvasEnd) (coord int, ok bool) {
	if e.Obj == "border" {
		switch e.Edge {
		case "left":
			return 0, true
		case "right":
			return d.W - 1, true
		case "top":
			return 0, true
		case "bottom":
			return d.H - 1, true
		}
		return 0, false
	}
	o, found := d.objectByColor(e.Obj)
	if !found {
		return 0, false
	}
	switch e.Edge {
	case "left":
		return o.MinX, true
	case "right":
		return o.MaxX, true
	case "top":
		return o.MinY, true
	case "bottom":
		return o.MaxY, true
	}
	return 0, false
}

// edgeAxis: left/right constrain X, top/bottom constrain Y.
func edgeAxis(edge string) byte {
	if edge == "left" || edge == "right" {
		return 'x'
	}
	return 'y'
}

// translateObject shifts every cell of one color, clamped into the canvas.
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
		if d.Objects[i].Fg == color {
			d.Objects[i].X += dx
			d.Objects[i].Y += dy
		}
	}
}

// solve satisfies every tie by translating each tie's From object (the border
// never moves; a border From swaps ends). A few passes settle chains.
func (d *canvasDoc) solve() {
	for pass := 0; pass < 4; pass++ {
		moved := false
		for _, t := range d.Ties {
			from, to, gap := t.From, t.To, t.Gap
			if from.Obj == "border" {
				if to.Obj == "border" {
					continue
				}
				from, to, gap = to, from, -gap
			}
			fc, ok1 := d.edgeCoord(from)
			tc, ok2 := d.edgeCoord(to)
			if !ok1 || !ok2 || edgeAxis(from.Edge) != edgeAxis(to.Edge) {
				continue
			}
			delta := tc + gap - fc
			if delta == 0 {
				continue
			}
			moved = true
			if edgeAxis(from.Edge) == 'x' {
				d.translateObject(from.Obj, delta, 0)
			} else {
				d.translateObject(from.Obj, 0, delta)
			}
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

// objectColorAt returns the object color under a point, or "".
func (d *canvasDoc) objectColorAt(x, y int) string {
	for _, c := range d.Objects {
		if c.X == x && c.Y == y {
			return c.Fg
		}
	}
	return ""
}

// nearestEdge picks the bbox edge closest to a point.
func nearestEdge(o canvasObj, x, y int) string {
	best, edge := 1<<30, "left"
	for _, cand := range []struct {
		name string
		dist int
	}{
		{"left", abs(x - o.MinX)}, {"right", abs(x - o.MaxX)},
		{"top", abs(y - o.MinY)}, {"bottom", abs(y - o.MaxY)},
	} {
		if cand.dist < best {
			best, edge = cand.dist, cand.name
		}
	}
	return edge
}

// endAt resolves the tie end under the cursor: an object when the cursor sits
// on one, else the nearest border edge.
func (d *canvasDoc) endAt(x, y int) canvasEnd {
	if color := d.objectColorAt(x, y); color != "" {
		o, _ := d.objectByColor(color)
		return canvasEnd{Obj: color, Edge: nearestEdge(o, x, y)}
	}
	// border: whichever canvas edge is closest
	best, edge := x, "left"
	if d.W-1-x < best {
		best, edge = d.W-1-x, "right"
	}
	if y < best {
		best, edge = y, "top"
	}
	if d.H-1-y < best {
		edge = "bottom"
	}
	return canvasEnd{Obj: "border", Edge: edge}
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// endLabel renders a tie end for headers and context.
func endLabel(e canvasEnd) string { return e.Obj + "." + e.Edge }

// ── persistence ─────────────────────────────────────────────────────────────

type canvasState struct {
	doc    *canvasDoc
	plane  string // "draw" | "object"
	cx, cy int
	vx, vy int
	vw, vh int

	brush    canvasBrush // draw plane
	objColor string      // object plane: the color being painted = the object

	tieFrom *canvasEnd // pending tie start
	tieSel  int        // selected tie for +/-/D

	pal    bool
	palQ   string
	palSel int
}

type canvasBrush struct{ Ch, Fg, Bg string }

func canvasStateOf(h editor.NodeHost, uuid string) *canvasState {
	d := h.NodeStore(uuid)
	st, _ := d["canvas"].(*canvasState)
	if st == nil {
		st = &canvasState{brush: canvasBrush{Ch: "─"}, plane: "draw", objColor: "red", tieSel: -1}
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

// canvasDocOf is the cached read: the live (or last-saved) document when one
// is in the node store, else one blob load that sticks as the cache.
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

// canvasRender is the outline line — a SINGLE summary line, like an image's:
// caption + dim size/content counts. Never a multi-line preview.
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
	if t := len(doc.Ties); t > 0 {
		parts += fmt.Sprintf(" · %d ties", t)
	}
	name := strings.TrimSpace(n.Text())
	if name != "" {
		return name + " " + th.Dim + "▦ " + parts + th.Reset
	}
	return th.Dim + "▦ canvas " + parts + th.Reset
}

// canvasText renders the document as plain text (draw plane over object
// blocks) plus the tie list — what agents receive.
func canvasText(d *canvasDoc) string {
	grid := map[[2]int]string{}
	for _, c := range d.Objects {
		grid[[2]int{c.X, c.Y}] = "▓"
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
	for _, t := range d.Ties {
		lines = append(lines, fmt.Sprintf("tie: %s → %s gap %d", endLabel(t.From), endLabel(t.To), t.Gap))
	}
	return strings.Join(lines, "\n")
}

// ── the painter (alt+e) ─────────────────────────────────────────────────────

type canvasView struct{}

const (
	canvasViewH    = 20
	canvasPalRows  = 8
	canvasCrossBG  = "\x1b[48;2;24;40;74m"
	canvasCursorBG = "\x1b[48;2;60;100;190m"
)

func (canvasView) Enter(h editor.NodeHost, n editor.NodeRef) bool {
	st := canvasStateOf(h, n.UUID())
	st.doc = canvasLoad(h, n.UUID()) // reload: pick up external edits
	st.pal, st.tieFrom, st.tieSel = false, nil, -1
	return true
}

func (canvasView) Leave(h editor.NodeHost, n editor.NodeRef) {
	st := canvasStateOf(h, n.UUID())
	if st.doc != nil {
		canvasSave(h, n.UUID(), st.doc)
	}
	// st.doc stays as the render cache
	st.pal, st.tieFrom = false, nil
}

func (canvasView) Lines(h editor.NodeHost, n editor.NodeRef, width int) int {
	st := canvasStateOf(h, n.UUID())
	nLines := 1 + canvasViewH
	if st.pal {
		nLines += 1 + canvasPalRows
	}
	return nLines
}

func (v canvasView) Key(h editor.NodeHost, n editor.NodeRef, k tea.KeyMsg) (tea.Cmd, bool) {
	st := canvasStateOf(h, n.UUID())
	if st.doc == nil {
		return nil, false
	}
	if st.pal {
		return nil, v.paletteKey(h, st, k)
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
		st.tieFrom = nil
	case "p", "/":
		st.pal, st.palQ, st.palSel = true, "", 0
	case "enter", " ", "space":
		if st.plane == "object" {
			setCell(&doc.Objects, canvasCell{X: st.cx, Y: st.cy, Ch: "█", Fg: st.objColor})
			doc.solve()
		} else {
			b := st.brush
			if b.Ch == "" {
				b.Ch = " "
			}
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
			h.NodeFlash("ties live on the object plane · tab")
			break
		}
		end := doc.endAt(st.cx, st.cy)
		if st.tieFrom == nil {
			st.tieFrom = &end
			break
		}
		from := *st.tieFrom
		st.tieFrom = nil
		if from == end {
			break
		}
		if edgeAxis(from.Edge) != edgeAxis(end.Edge) {
			h.NodeFlash("tie edges must share an axis (left/right or top/bottom)")
			break
		}
		fc, ok1 := doc.edgeCoord(from)
		tc, ok2 := doc.edgeCoord(end)
		if !ok1 || !ok2 {
			break
		}
		doc.Ties = append(doc.Ties, canvasTie{From: from, To: end, Gap: fc - tc})
		st.tieSel = len(doc.Ties) - 1
		doc.solve()
	case "T": // cycle the selected tie
		if len(doc.Ties) > 0 {
			st.tieSel = (st.tieSel + 1) % len(doc.Ties)
		}
	case "+", "=":
		if st.tieSel >= 0 && st.tieSel < len(doc.Ties) {
			doc.Ties[st.tieSel].Gap++
			doc.solve()
		}
	case "-":
		if st.tieSel >= 0 && st.tieSel < len(doc.Ties) {
			doc.Ties[st.tieSel].Gap--
			doc.solve()
		}
	case "D":
		if st.tieSel >= 0 && st.tieSel < len(doc.Ties) {
			doc.Ties = append(doc.Ties[:st.tieSel], doc.Ties[st.tieSel+1:]...)
			st.tieSel = -1
		}
	default:
		return nil, false // esc & friends → central (Leave saves)
	}
	v.pan(st)
	return nil, true
}

func (v canvasView) paletteKey(h editor.NodeHost, st *canvasState, k tea.KeyMsg) bool {
	switch k.String() {
	case "esc":
		st.pal = false
		return true
	case "enter":
		hits := canvasPaletteSearch(st.palQ)
		if st.palSel >= 0 && st.palSel < len(hits) {
			e := hits[st.palSel]
			if st.plane == "object" {
				// the object plane speaks colors only — a color IS an object
				if e.color != "" && e.color != "none" {
					st.objColor = e.color
				} else {
					h.NodeFlash("object plane uses colors · pick foreground/background <color>")
				}
			} else {
				switch e.cat {
				case "background":
					if e.color == "none" {
						st.brush.Bg = ""
					} else {
						st.brush.Bg = e.color
					}
				case "foreground":
					st.brush.Fg = e.color
				default:
					st.brush.Ch = e.ch
				}
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

	// header: plane, position, brush/object color, the live tie
	head := fmt.Sprintf("  %s · (%d,%d)", st.plane, st.cx, st.cy)
	if st.plane == "object" {
		head += " · object " + editor.NodeColor(st.objColor) + st.objColor + th.Reset + th.Dim
		switch {
		case st.tieFrom != nil:
			head += " · tie from " + endLabel(*st.tieFrom) + " → t on target"
		case st.tieSel >= 0 && st.tieSel < len(doc.Ties):
			t := doc.Ties[st.tieSel]
			head += fmt.Sprintf(" · tie %s→%s gap %d · +/- adjust · D delete", endLabel(t.From), endLabel(t.To), t.Gap)
		default:
			head += " · t tie · T select · p color"
		}
	} else {
		brush := st.brush.Ch
		if brush == "" {
			brush = "·bg"
		}
		head += " · brush " + brush + " · p palette · x erase"
	}
	head += " · tab plane · esc save"
	var content []string
	content = append(content, editor.NodeClip(rail+th.Reset+th.Dim+head+th.Reset, width))

	// composite: object blocks under draw cells
	type cell struct{ ch, fg, bg string }
	grid := map[[2]int]cell{}
	for _, c := range doc.Objects {
		grid[[2]int{c.X, c.Y}] = cell{ch: "█", fg: c.Fg}
	}
	for _, c := range doc.Cells {
		if c.Ch != "" {
			grid[[2]int{c.X, c.Y}] = cell{ch: c.Ch, fg: c.Fg, bg: c.Bg}
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
			if focused {
				switch {
				case x == st.cx && y == st.cy:
					sgr = canvasCursorBG
				case c.bg == "" && (x == st.cx || y == st.cy):
					sgr = canvasCrossBG
				}
			}
			if c.fg != "" {
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

	if st.pal {
		content = append(content, editor.NodeClip(rail+th.Reset+th.Dim+"  palette › "+th.Reset+th.FG+st.palQ+th.Reset+th.Dim+"▏ enter picks · esc closes"+th.Reset, width))
		hits := canvasPaletteSearch(st.palQ)
		for i := 0; i < canvasPalRows; i++ {
			line := rail + th.Reset + "  "
			if i < len(hits) {
				e := hits[i]
				marker, style := "  ", th.Dim
				if i == st.palSel {
					marker, style = th.Accent+"▸ "+th.Reset, th.FG
				}
				sample := " " + e.ch + " "
				switch e.cat {
				case "background":
					sample = canvasBg(e.color) + "   " + th.Reset
					if e.color == "none" {
						sample = "   "
					}
				case "foreground":
					sample = editor.NodeColor(e.color) + "██▌" + th.Reset
				}
				line += marker + sample + " " + style + e.cat + " · " + e.name + th.Reset
			}
			content = append(content, editor.NodeClip(line, width))
		}
	}
	return editor.NodeWindowBands(content, scroll, winH)
}
