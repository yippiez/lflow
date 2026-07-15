package nodes

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lflow/lflow/pkg/tui/database"
	"github.com/lflow/lflow/pkg/tui/editor"
)

// The circuit node ▚ — a drawn factory floor, the Mindustry look. The node
// hangs a chunky tile canvas beneath its row (the image node's pixelated
// preview, always on): a checkered navy floor you build machines on in
// alt+e's painter — directional CONVEYOR BELTS laid along the direction you
// drag (space paints a belt pointing the way the cursor last moved), steel
// DRILLS that produce items, amber CORES that collect them. alt+r brings the
// floor to life: drills emit yellow items onto neighboring belts, items ride
// the belts one tile per beat, queue up when the line jams (belt
// backpressure), and vanish into cores — the row chip counts what the node's
// cores have collected. alt+r again stops and restores the drawing.
//
// Machines COMPOSE: contiguous circuit-typed SIBLINGS fuse their floors
// top-to-bottom into ONE board, so belts carry items across node seams and a
// stack of nodes is a single live factory. The drawing persists in
// node_output (local, never synced — like image pixels); items, drill timers
// and core tallies are ephemeral package state, gone on restart.
//
// WARNING (invariant): the simulation never runs on its own — alt+r only.

// The floor: tiles, each drawn 2×2 half-block pixels (one text character is
// 1 tile wide, one text row is 1 tile tall). Every canvas is tileW wide so
// stacked nodes seam cleanly.
const (
	tileW = 22
	tileH = 5
)

const (
	circTickEvery  = 110 * time.Millisecond
	circDrillEvery = 5 // beats between drill emissions
)

// tile values (also the persisted row runes). Belts carry their direction.
const (
	tileEmpty = '.'
	tileBeltR = '>'
	tileBeltL = '<'
	tileBeltU = '^'
	tileBeltD = 'v'
	tileDrill = 'D'
	tileCore  = 'O'
)

// circDirs maps a belt tile to its flow direction.
var circDirs = map[byte][2]int{ // {dy, dx}
	tileBeltR: {0, 1},
	tileBeltL: {0, -1},
	tileBeltU: {-1, 0},
	tileBeltD: {1, 0},
}

func circIsBelt(t byte) bool { _, ok := circDirs[t]; return ok }

// circRotate spins a belt clockwise (the painter's r key).
var circRotate = map[byte]byte{tileBeltR: tileBeltD, tileBeltD: tileBeltL, tileBeltL: tileBeltU, tileBeltU: tileBeltR}

// The floor palette — dark blue metal + yellow items.
var (
	pxFloorA = [3]int{13, 19, 33} // the checkered floor
	pxFloorB = [3]int{17, 24, 41}
	pxBelt   = [3]int{42, 60, 92}    // belt base
	pxBeltHi = [3]int{96, 130, 190}  // belt leading edge (the direction cue)
	pxDrill  = [3]int{140, 165, 215} // drill housing
	pxCore   = [3]int{216, 166, 64}  // core block
	pxItem   = [3]int{255, 215, 95}  // an item riding a belt
	pxCursor = [3]int{245, 245, 245} // the painter crosshair
)

func init() {
	editor.RegisterNodePlugin(editor.NodePlugin{
		Key: database.TypeCircuit, Label: "Circuit",
		InlineEditable: true, // the row text is the machine's label
		Glyph:          func() (string, string) { return "▚", facBlue },
		BaseColor:      func() string { return facBlue },
		Prefix:         circPrefix,
		Run:            circToggle,
		View:           circView{},
		Preview:        circPreview,
		ToContext:      circContext,
		OnRemove:       circOnRemove,
	})
}

// The family palette (the row chrome): dark blue + yellow.
const (
	facBlue = "\x1b[38;2;100;148;220m" // steel blue — glyph + label
	facDeep = "\x1b[38;2;58;92;150m"   // dark blue — chrome
)

// ── floor state ─────────────────────────────────────────────────────────────

// circGrids holds each node's LIVE floor (edits and simulation both act on
// it); circItems the items riding its belts; circScores what its cores have
// collected this run; circCursors/circLastDir the painter's crosshair and its
// last movement (belts are laid along it). All package state keyed by uuid —
// ephemeral, event-loop only.
var (
	circGrids   = map[string][][]byte{}
	circItems   = map[string][][]bool{}
	circScores  = map[string]int{}
	circCursors = map[string][2]int{}
	circLastDir = map[string]byte{}
	circRuns    = map[string]*circRun{}
)

// circData is the persisted drawing (node_output JSON).
type circData struct {
	W    int      `json:"w"`
	H    int      `json:"h"`
	Rows []string `json:"rows"`
}

func circBlank() [][]byte {
	g := make([][]byte, tileH)
	for y := range g {
		g[y] = []byte(strings.Repeat(string(rune(tileEmpty)), tileW))
	}
	return g
}

func circNoItems() [][]bool {
	it := make([][]bool, tileH)
	for y := range it {
		it[y] = make([]bool, tileW)
	}
	return it
}

// circGridOf returns the node's live floor, loading the persisted drawing on
// first touch (blank floor when none). Rows normalize to tileW×tileH so
// stacked floors always seam.
func circGridOf(h editor.NodeHost, uuid string) [][]byte {
	if g, ok := circGrids[uuid]; ok {
		return g
	}
	g := circBlank()
	if db := h.NodeDB(); db != nil {
		if raw, err := database.LoadNodeOutput(db, uuid); err == nil && raw != "" {
			var d circData
			if json.Unmarshal([]byte(raw), &d) == nil {
				for y := 0; y < tileH && y < len(d.Rows); y++ {
					row := []byte(d.Rows[y])
					for x := 0; x < tileW && x < len(row); x++ {
						switch row[x] {
						case tileBeltR, tileBeltL, tileBeltU, tileBeltD, tileDrill, tileCore:
							g[y][x] = row[x]
						}
					}
				}
			}
		}
	}
	circGrids[uuid] = g
	return g
}

func circItemsOf(uuid string) [][]bool {
	if it, ok := circItems[uuid]; ok {
		return it
	}
	it := circNoItems()
	circItems[uuid] = it
	return it
}

// circSave persists the node's live floor as its drawing.
func circSave(h editor.NodeHost, uuid string) {
	db := h.NodeDB()
	if db == nil {
		return
	}
	g := circGridOf(h, uuid)
	rows := make([]string, len(g))
	for y := range g {
		rows[y] = string(g[y])
	}
	if raw, err := json.Marshal(circData{W: tileW, H: tileH, Rows: rows}); err == nil {
		_ = database.SaveNodeOutput(db, uuid, string(raw))
	}
}

func circCopy(g [][]byte) [][]byte {
	out := make([][]byte, len(g))
	for y := range g {
		out[y] = append([]byte(nil), g[y]...)
	}
	return out
}

// ── the live factory ────────────────────────────────────────────────────────

// circStack returns the fused board's members: the maximal run of contiguous
// circuit-typed siblings around n, top-down.
func circStack(n editor.NodeRef) []editor.NodeRef {
	sibs := n.Siblings()
	i := -1
	for j, s := range sibs {
		if s.Is(n) {
			i = j
			break
		}
	}
	if i < 0 {
		return []editor.NodeRef{n}
	}
	lo, hi := i, i
	for lo > 0 && sibs[lo-1].Type() == database.TypeCircuit {
		lo--
	}
	for hi+1 < len(sibs) && sibs[hi+1].Type() == database.TypeCircuit {
		hi++
	}
	return sibs[lo : hi+1]
}

// circRun is one live factory over a fused board.
type circRun struct {
	uuids []string            // stack members, top-down
	snaps map[string][][]byte // the drawing as it was — restored on stop
	beat  int
	stop  bool
}

// circToggle (alt+r) brings the stack's floor to life, or stops it and
// restores the drawing.
func circToggle(h editor.NodeHost, n editor.NodeRef) tea.Cmd {
	if r := circRuns[n.UUID()]; r != nil {
		circStopRun(r)
		h.NodeFlash("factory stopped · drawing restored")
		return nil
	}
	stack := circStack(n)
	r := &circRun{snaps: map[string][][]byte{}}
	for _, m := range stack {
		u := m.UUID()
		circSave(h, u) // running is committing: the floor you see is the drawing you keep
		r.uuids = append(r.uuids, u)
		r.snaps[u] = circCopy(circGridOf(h, u))
		circItems[u] = circNoItems()
		circScores[u] = 0
		circRuns[u] = r
	}
	h.NodeFlash("factory live · ⌥r stops")
	return circTickCmd(r)
}

func circStopRun(r *circRun) {
	if r.stop {
		return
	}
	r.stop = true
	for _, u := range r.uuids {
		if snap, ok := r.snaps[u]; ok {
			circGrids[u] = snap
		}
		circItems[u] = circNoItems()
		delete(circRuns, u)
	}
}

// circOnRemove stops the factory a removed node belongs to and drops its
// floor state.
func circOnRemove(h editor.NodeHost, uuid string) {
	if r := circRuns[uuid]; r != nil {
		circStopRun(r)
	}
	delete(circGrids, uuid)
	delete(circItems, uuid)
	delete(circScores, uuid)
	delete(circCursors, uuid)
	delete(circLastDir, uuid)
}

// circTickMsg is one factory beat (editor.NodePluginMsg).
type circTickMsg struct{ run *circRun }

func circTickCmd(r *circRun) tea.Cmd {
	return tea.Tick(circTickEvery, func(time.Time) tea.Msg { return circTickMsg{run: r} })
}

// HandleNodePlugin advances the fused board one beat and re-arms while the
// run lives: items ride belts (backpressure included), cores collect, drills
// emit.
func (msg circTickMsg) HandleNodePlugin(h editor.NodeHost) tea.Cmd {
	r := msg.run
	if r.stop {
		return nil
	}
	// fuse member floors and item layers top-to-bottom — belts carry across
	// the node seams because the board steps as one grid
	var board [][]byte
	var items [][]bool
	for _, u := range r.uuids {
		board = append(board, circGridOf(h, u)...)
		items = append(items, circItemsOf(u)...)
	}
	scored := circStepBoard(board, items, r.beat)
	for at, count := range scored {
		u := r.uuids[at/tileH]
		circScores[u] += count
	}
	r.beat++
	for i, u := range r.uuids {
		circItems[u] = items[i*tileH : (i+1)*tileH]
	}
	return circTickCmd(r)
}

// circStepBoard is one beat over a fused board: items advance along their
// belt's direction when the next tile is free (multi-pass, so a convoy moves
// as one), a core consumes what reaches it, and every circDrillEvery-th beat
// each drill emits onto its free neighboring belts. Returns items collected
// per core row (keyed by the core's board row, for score attribution).
func circStepBoard(board [][]byte, items [][]bool, beat int) map[int]int {
	hh := len(board)
	if hh == 0 {
		return nil
	}
	ww := len(board[0])
	scored := map[int]int{}
	settled := make([][]bool, hh) // an item moves at most one tile per beat
	for y := range settled {
		settled[y] = make([]bool, ww)
	}
	for pass, moved := 0, true; moved && pass < 8; pass++ {
		moved = false
		for y := 0; y < hh; y++ {
			for x := 0; x < ww; x++ {
				if !items[y][x] || settled[y][x] {
					continue
				}
				d, ok := circDirs[board[y][x]]
				if !ok {
					continue // an item stranded off-belt just sits
				}
				ny, nx := y+d[0], x+d[1]
				if ny < 0 || ny >= hh || nx < 0 || nx >= ww {
					continue // belts never dump items off the board — the line backs up
				}
				switch {
				case board[ny][nx] == tileCore:
					items[y][x] = false
					scored[ny]++
					moved = true
				case circIsBelt(board[ny][nx]) && !items[ny][nx]:
					items[y][x] = false
					items[ny][nx] = true
					settled[ny][nx] = true
					moved = true
				}
			}
		}
	}
	if beat%circDrillEvery == 0 {
		for y := 0; y < hh; y++ {
			for x := 0; x < ww; x++ {
				if board[y][x] != tileDrill {
					continue
				}
				for _, d := range [][2]int{{-1, 0}, {0, 1}, {1, 0}, {0, -1}} {
					ny, nx := y+d[0], x+d[1]
					if ny >= 0 && ny < hh && nx >= 0 && nx < ww && circIsBelt(board[ny][nx]) && !items[ny][nx] {
						items[ny][nx] = true
					}
				}
			}
		}
	}
	return scored
}

// ── the look ────────────────────────────────────────────────────────────────

// circPrefix chips the row while the factory is live — with the tally its
// cores have collected.
func circPrefix(uuid string) string {
	if circRuns[uuid] == nil {
		return ""
	}
	th := editor.NodeTheme()
	chip := "live"
	if s := circScores[uuid]; s > 0 {
		chip = fmt.Sprintf("live %d", s)
	}
	return th.Yellow + chip + " " + th.Reset
}

// circTilePx returns a tile's four pixels ([col][row], 2×2) — the chunky
// block look: checkered floor, belts with a bright leading edge pointing
// their direction, steel drills, amber cores, items as full yellow squares.
func circTilePx(t byte, item bool, tx, ty int) [2][2][3]int {
	var px [2][2][3]int
	floor := pxFloorA
	if (tx+ty)%2 == 1 {
		floor = pxFloorB
	}
	fill := func(c [3]int) {
		px[0][0], px[0][1], px[1][0], px[1][1] = c, c, c, c
	}
	switch {
	case item:
		fill(pxItem)
	case t == tileDrill:
		fill(pxDrill)
		px[1][1] = pxBelt // a darker notch so drills read as machines, not blobs
	case t == tileCore:
		fill(pxCore)
	case circIsBelt(t):
		fill(pxBelt)
		switch t {
		case tileBeltR:
			px[1][0], px[1][1] = pxBeltHi, pxBeltHi
		case tileBeltL:
			px[0][0], px[0][1] = pxBeltHi, pxBeltHi
		case tileBeltU:
			px[0][0], px[1][0] = pxBeltHi, pxBeltHi
		case tileBeltD:
			px[0][1], px[1][1] = pxBeltHi, pxBeltHi
		}
	default:
		fill(floor)
	}
	return px
}

// circBandLines renders a floor as half-block tile rows (one text row per
// tile row, two pixels per cell); cursor ≥ 0 tile coordinates paint the
// painter's crosshair tile white.
func circBandLines(g [][]byte, items [][]bool, rail string, cx, cy int) []string {
	th := editor.NodeTheme()
	out := make([]string, 0, len(g))
	for ty := range g {
		var b strings.Builder
		b.WriteString(rail + th.Reset + "  ")
		for tx := range g[ty] {
			item := items != nil && items[ty][tx]
			px := circTilePx(g[ty][tx], item, tx, ty)
			if tx == cx && ty == cy {
				px[0][0], px[0][1], px[1][0], px[1][1] = pxCursor, pxCursor, pxCursor, pxCursor
			}
			for col := 0; col < 2; col++ {
				up, lo := px[col][0], px[col][1]
				fmt.Fprintf(&b, "\x1b[38;2;%d;%d;%dm\x1b[48;2;%d;%d;%dm▀",
					up[0], up[1], up[2], lo[0], lo[1], lo[2])
			}
		}
		b.WriteString(th.Reset)
		out = append(out, b.String())
	}
	return out
}

// circPreview is the always-on pixelated preview beneath the closed row (the
// image-node look); the painter's bands take over while the view is focused.
func circPreview(h editor.NodeHost, n editor.NodeRef, rail string, maxLine int, focused bool) []string {
	if focused {
		return nil
	}
	lines := circBandLines(circGridOf(h, n.UUID()), circItemsOf(n.UUID()), rail, -1, -1)
	for i := range lines {
		lines[i] = editor.NodeClip(lines[i], maxLine)
	}
	return lines
}

// circContext hands an agent the floor as its tile rows (> < ^ v belts,
// D drill, O core, . floor).
func circContext(h editor.NodeHost, n editor.NodeRef) (string, string, string) {
	g := circGridOf(h, n.UUID())
	rows := make([]string, len(g))
	for y := range g {
		rows[y] = string(g[y])
	}
	live := ""
	if circRuns[n.UUID()] != nil {
		live = fmt.Sprintf(` live="true" collected="%d"`, circScores[n.UUID()])
	}
	body := n.Text()
	if body != "" {
		body += "\n"
	}
	return "circuit", fmt.Sprintf(`w="%d" h="%d"%s`, tileW, tileH, live), body + strings.Join(rows, "\n")
}

// ── the painter (alt+e) ─────────────────────────────────────────────────────

// circView is the builder: the same tile bands with a white cursor tile.
// Belts are laid the Mindustry way — space paints one pointing the direction
// the cursor last moved, so dragging a path and tapping space leaves a
// flowing line. Stateless; cursor and floor in package state, persisted on
// leave.
type circView struct{}

func (circView) Enter(h editor.NodeHost, n editor.NodeRef) bool {
	circGridOf(h, n.UUID()) // make sure the floor is loaded
	if _, ok := circCursors[n.UUID()]; !ok {
		circCursors[n.UUID()] = [2]int{tileW / 2, tileH / 2}
		circLastDir[n.UUID()] = tileBeltR
	}
	return true
}

// Leave persists the drawing.
func (circView) Leave(h editor.NodeHost, n editor.NodeRef) {
	if circRuns[n.UUID()] == nil { // a live floor is transient — don't save items mid-flight
		circSave(h, n.UUID())
	}
}

func (circView) Lines(h editor.NodeHost, n editor.NodeRef, width int) int {
	return 1 + tileH
}

func (circView) Key(h editor.NodeHost, n editor.NodeRef, k tea.KeyMsg) (tea.Cmd, bool) {
	u := n.UUID()
	cur := circCursors[u]
	place := func(v byte) {
		if circRuns[u] != nil {
			h.NodeFlash("factory is live · ⌥r stops it first")
			return
		}
		circGridOf(h, u)[cur[1]][cur[0]] = v
	}
	switch k.String() {
	case "alt+r":
		return circToggle(h, n), true
	case "left":
		cur[0]--
		circLastDir[u] = tileBeltL
	case "right":
		cur[0]++
		circLastDir[u] = tileBeltR
	case "up":
		cur[1]--
		circLastDir[u] = tileBeltU
	case "down":
		cur[1]++
		circLastDir[u] = tileBeltD
	case " ", "b", "enter":
		place(circLastDir[u]) // a belt flowing the way you're dragging
	case "r": // spin the belt under the cursor
		if t := circGridOf(h, u)[cur[1]][cur[0]]; circIsBelt(t) {
			place(circRotate[t])
		}
	case "d":
		place(tileDrill)
	case "o":
		place(tileCore)
	case "x", "backspace":
		place(tileEmpty)
	default:
		if k.Type == tea.KeySpace {
			place(circLastDir[u])
			return nil, true
		}
		return nil, false // esc, ctrl+c … → central
	}
	if cur[0] < 0 {
		cur[0] = 0
	}
	if cur[0] >= tileW {
		cur[0] = tileW - 1
	}
	if cur[1] < 0 {
		cur[1] = 0
	}
	if cur[1] >= tileH {
		cur[1] = tileH - 1
	}
	circCursors[u] = cur
	return nil, true
}

func (circView) Bands(h editor.NodeHost, n editor.NodeRef, rail string, width, scroll, winH int, focused bool) []string {
	th := editor.NodeTheme()
	u := n.UUID()
	hdr := "  factory · space belt (flows with your drag) · d drill · o core · r spin · x erase · ⌥r run"
	if circRuns[u] != nil {
		hdr = "  factory · live · ⌥r stops · esc close"
	}
	cx, cy := -1, -1
	if focused && circRuns[u] == nil {
		c := circCursors[u]
		cx, cy = c[0], c[1]
	}
	content := []string{editor.NodeClip(rail+th.Reset+th.Dim+hdr+th.Reset, width)}
	for _, l := range circBandLines(circGridOf(h, u), circItemsOf(u), rail, cx, cy) {
		content = append(content, editor.NodeClip(l, width))
	}
	return editor.NodeWindowBands(content, 0, winH)
}
