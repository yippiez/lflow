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

// The circuit node ⠿ — a factory LANE drawn into the row itself, the image
// node's inline look taken minimal: the body IS a one-line BRAILLE strip —
// a faint dotted floor with machines as dot clusters, foreground color only,
// no filled blocks — nothing hangs beneath and nothing expands. Resting the
// cursor on the node focuses the lane like the Code node (no alt+e): ←/→
// walk the white cursor, space lays a CONVEYOR BELT flowing the way you last
// moved, r spins it (> v < ^), d places a DRILL, o a CORE, x erases. alt+r
// brings the lane to life: drills emit yellow items, items ride the belts
// one tile per beat with real backpressure while the belt dots march along
// the flow, cores collect and the tail chip tallies; alt+r again stops and
// restores the drawing.
//
// Machines COMPOSE vertically: contiguous circuit-typed SIBLINGS fuse their
// lanes into ONE board, one lane per row — a v belt hands its item to the
// sibling lane below, ^ to the one above, so a stack of one-line nodes is a
// little Mindustry floor. The drawing persists in node_output (local, never
// synced — like image pixels); items, drill timers and tallies are ephemeral
// package state, gone on restart.
//
// WARNING (invariant): the simulation never runs on its own — alt+r only.

// The lane: tileW tiles, one braille character each. Every lane is tileW
// wide so stacked nodes seam cleanly.
const tileW = 20

const (
	circTickEvery  = 110 * time.Millisecond
	circDrillEvery = 5 // beats between drill emissions
)

// tile values (also the persisted row runes). Belts carry their direction:
// > < flow along the lane, v ^ hand off to the sibling lane below/above.
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
var circDirs = map[byte][2]int{ // {dlane, dx}
	tileBeltR: {0, 1},
	tileBeltL: {0, -1},
	tileBeltU: {-1, 0},
	tileBeltD: {1, 0},
}

func circIsBelt(t byte) bool { _, ok := circDirs[t]; return ok }

// circRotate spins a belt clockwise (the r key).
var circRotate = map[byte]byte{tileBeltR: tileBeltD, tileBeltD: tileBeltL, tileBeltL: tileBeltU, tileBeltU: tileBeltR}

// The lane is drawn in BRAILLE dots — foreground color only, never a filled
// background, so a machine reads as a faint dotted schematic sitting directly
// on the page: a dim dotted floor, two-dot belts whose lit side is the flow
// direction (and whose dots MARCH while live), a dense ⣿ drill, a ⣶ core,
// a bright ⠶ item. Dark blue chrome + yellow items throughout.
const (
	fgFloor  = "\x1b[38;2;44;56;82m"    // the dotted floor line
	fgBelt   = "\x1b[38;2;92;126;184m"  // idle belt dots
	fgBeltHi = "\x1b[38;2;130;164;220m" // live, marching belt dots
	fgDrill  = "\x1b[38;2;150;175;225m" // drill cluster
	fgCore   = "\x1b[38;2;216;166;64m"  // core cluster
	fgItem   = "\x1b[38;2;255;215;95m"  // an item riding a belt
	fgCursor = "\x1b[38;2;245;245;245m" // the builder cursor
)

// Tile glyphs. Idle belts light the side they flow toward; live belts run a
// dot along the flow instead (marching frames below, phased by the beat and
// the tile so the whole lane ripples).
var (
	beltIdle   = map[byte]rune{tileBeltR: '⠘', tileBeltL: '⠃', tileBeltU: '⠉', tileBeltD: '⠤'}
	marchRight = []rune("⠂⠒⠐") // a dot sweeping the mid row →
	marchLeft  = []rune("⠐⠒⠂")
	marchDown  = []rune("⠁⠂⠄⡀") // a dot falling the left column ↓
	marchUp    = []rune("⡀⠄⠂⠁")
	floorDots  = []rune("⠄⠂") // the faint alternating floor texture
	glyphDrill = '⣿'
	glyphCore  = '⣶'
	glyphItem  = '⠶'
	glyphPad   = '⣀' // the cursor resting on empty floor
)

func init() {
	editor.RegisterNodePlugin(editor.NodePlugin{
		Key: database.TypeCircuit, Label: "Circuit",
		InlineEditable: false, // the body is the lane strip, not text
		AutoFocus:      true,  // rest the cursor on the row to build, like Code
		Glyph:          func() (string, string) { return "⠿", facBlue },
		Render:         circRender,
		Run:            circToggle,
		View:           circView{},
		ToContext:      circContext,
		OnRemove:       circOnRemove,
	})
}

// The family chrome: dark blue + yellow.
const (
	facBlue = "\x1b[38;2;100;148;220m" // steel blue — glyph + caption
	facDeep = "\x1b[38;2;58;92;150m"   // dark blue — chrome
)

// ── lane state ──────────────────────────────────────────────────────────────

// circGrids holds each node's LIVE lane (edits and simulation both act on
// it); circItems the items riding it; circScores what its cores collected
// this run; circCursors/circLastDir the builder cursor and its last
// horizontal move (belts lay along it); circFocus the lane being built on.
// All package state keyed by uuid — ephemeral, event-loop only.
var (
	circGrids   = map[string][]byte{}
	circItems   = map[string][]bool{}
	circScores  = map[string]int{}
	circCursors = map[string]int{}
	circLastDir = map[string]byte{}
	circFocus   = ""
	circRuns    = map[string]*circRun{}
)

// circData is the persisted drawing (node_output JSON).
type circData struct {
	W    int      `json:"w"`
	H    int      `json:"h"`
	Rows []string `json:"rows"`
}

func circBlank() []byte { return []byte(strings.Repeat(string(rune(tileEmpty)), tileW)) }

// circGridOf returns the node's live lane, loading the persisted drawing on
// first touch (blank lane when none). Normalized to tileW so stacks seam.
func circGridOf(h editor.NodeHost, uuid string) []byte {
	if g, ok := circGrids[uuid]; ok {
		return g
	}
	g := circBlank()
	if db := h.NodeDB(); db != nil {
		if raw, err := database.LoadNodeOutput(db, uuid); err == nil && raw != "" {
			var d circData
			if json.Unmarshal([]byte(raw), &d) == nil && len(d.Rows) > 0 {
				row := []byte(d.Rows[0])
				for x := 0; x < tileW && x < len(row); x++ {
					switch row[x] {
					case tileBeltR, tileBeltL, tileBeltU, tileBeltD, tileDrill, tileCore:
						g[x] = row[x]
					}
				}
			}
		}
	}
	circGrids[uuid] = g
	return g
}

func circItemsOf(uuid string) []bool {
	if it, ok := circItems[uuid]; ok {
		return it
	}
	it := make([]bool, tileW)
	circItems[uuid] = it
	return it
}

// circSave persists the node's live lane as its drawing.
func circSave(h editor.NodeHost, uuid string) {
	db := h.NodeDB()
	if db == nil {
		return
	}
	if raw, err := json.Marshal(circData{W: tileW, H: 1, Rows: []string{string(circGridOf(h, uuid))}}); err == nil {
		_ = database.SaveNodeOutput(db, uuid, string(raw))
	}
}

// ── the live factory ────────────────────────────────────────────────────────

// circStack returns the fused board's lanes: the maximal run of contiguous
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

// circRun is one live factory over a fused board (one lane per member).
type circRun struct {
	uuids []string          // stack members, top-down
	snaps map[string][]byte // the drawing as it was — restored on stop
	beat  int
	stop  bool
}

// circToggle (alt+r) brings the stack's lanes to life, or stops them and
// restores the drawing.
func circToggle(h editor.NodeHost, n editor.NodeRef) tea.Cmd {
	if r := circRuns[n.UUID()]; r != nil {
		circStopRun(r)
		h.NodeFlash("factory stopped · drawing restored")
		return nil
	}
	stack := circStack(n)
	r := &circRun{snaps: map[string][]byte{}}
	for _, m := range stack {
		u := m.UUID()
		circSave(h, u) // running is committing: the lane you see is the drawing you keep
		r.uuids = append(r.uuids, u)
		r.snaps[u] = append([]byte(nil), circGridOf(h, u)...)
		circItems[u] = make([]bool, tileW)
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
		circItems[u] = make([]bool, tileW)
		delete(circRuns, u)
	}
}

// circOnRemove stops the factory a removed node belongs to and drops its
// lane state.
func circOnRemove(h editor.NodeHost, uuid string) {
	if r := circRuns[uuid]; r != nil {
		circStopRun(r)
	}
	delete(circGrids, uuid)
	delete(circItems, uuid)
	delete(circScores, uuid)
	delete(circCursors, uuid)
	delete(circLastDir, uuid)
	if circFocus == uuid {
		circFocus = ""
	}
}

// circTickMsg is one factory beat (editor.NodePluginMsg).
type circTickMsg struct{ run *circRun }

func circTickCmd(r *circRun) tea.Cmd {
	return tea.Tick(circTickEvery, func(time.Time) tea.Msg { return circTickMsg{run: r} })
}

// HandleNodePlugin advances the fused board one beat and re-arms while the
// run lives.
func (msg circTickMsg) HandleNodePlugin(h editor.NodeHost) tea.Cmd {
	r := msg.run
	if r.stop {
		return nil
	}
	board := make([][]byte, len(r.uuids))
	items := make([][]bool, len(r.uuids))
	for i, u := range r.uuids {
		board[i] = circGridOf(h, u)
		items[i] = circItemsOf(u)
	}
	for lane, count := range circStepBoard(board, items, r.beat) {
		circScores[r.uuids[lane]] += count
	}
	r.beat++
	return circTickCmd(r)
}

// circStepBoard is one beat over a fused board (lane per row): items advance
// along their belt's direction when the next tile is free (multi-pass, so a
// convoy moves as one), a core consumes what reaches it, and every
// circDrillEvery-th beat each drill emits onto its free neighboring belts.
// Returns items collected per lane. Mutates board's item layers in place.
func circStepBoard(board [][]byte, items [][]bool, beat int) map[int]int {
	lanes := len(board)
	if lanes == 0 {
		return nil
	}
	ww := len(board[0])
	scored := map[int]int{}
	settled := make([][]bool, lanes) // an item moves at most one tile per beat
	for l := range settled {
		settled[l] = make([]bool, ww)
	}
	for pass, moved := 0, true; moved && pass < 8; pass++ {
		moved = false
		for l := 0; l < lanes; l++ {
			for x := 0; x < ww; x++ {
				if !items[l][x] || settled[l][x] {
					continue
				}
				d, ok := circDirs[board[l][x]]
				if !ok {
					continue // an item stranded off-belt just sits
				}
				nl, nx := l+d[0], x+d[1]
				if nl < 0 || nl >= lanes || nx < 0 || nx >= ww {
					continue // belts never dump items off the board — the line backs up
				}
				switch {
				case board[nl][nx] == tileCore:
					items[l][x] = false
					scored[nl]++
					moved = true
				case circIsBelt(board[nl][nx]) && !items[nl][nx]:
					items[l][x] = false
					items[nl][nx] = true
					settled[nl][nx] = true
					moved = true
				}
			}
		}
	}
	if beat%circDrillEvery == 0 {
		for l := 0; l < lanes; l++ {
			for x := 0; x < ww; x++ {
				if board[l][x] != tileDrill {
					continue
				}
				for _, d := range [][2]int{{-1, 0}, {0, 1}, {1, 0}, {0, -1}} {
					nl, nx := l+d[0], x+d[1]
					if nl >= 0 && nl < lanes && nx >= 0 && nx < ww && circIsBelt(board[nl][nx]) && !items[nl][nx] {
						items[nl][nx] = true
					}
				}
			}
		}
	}
	return scored
}

// ── the inline look ─────────────────────────────────────────────────────────

// circTileGlyph picks one tile's braille glyph and color. live belts march
// their dot along the flow, phased by beat and tile so the lane ripples.
func circTileGlyph(t byte, item, live bool, beat, tx int) (rune, string) {
	if item {
		return glyphItem, fgItem
	}
	switch t {
	case tileDrill:
		return glyphDrill, fgDrill
	case tileCore:
		return glyphCore, fgCore
	}
	if circIsBelt(t) {
		if !live {
			return beltIdle[t], fgBelt
		}
		switch t {
		case tileBeltR:
			return marchRight[((beat-tx)%3+3)%3], fgBeltHi
		case tileBeltL:
			return marchLeft[((beat-tx)%3+3)%3], fgBeltHi
		case tileBeltD:
			return marchDown[beat%4], fgBeltHi
		default: // tileBeltU
			return marchUp[beat%4], fgBeltHi
		}
	}
	return floorDots[tx%2], fgFloor
}

// circStrip renders the lane as one run of braille characters; cursor ≥ 0
// paints the builder's tile white (its own glyph, a ⣀ pad on bare floor).
func circStrip(g []byte, items []bool, cursor int, live bool, beat int) string {
	th := editor.NodeTheme()
	var b strings.Builder
	for tx := 0; tx < len(g); tx++ {
		ch, sgr := circTileGlyph(g[tx], items != nil && items[tx], live, beat, tx)
		if tx == cursor {
			sgr = fgCursor
			if g[tx] == tileEmpty {
				ch = glyphPad
			}
		}
		b.WriteString(sgr)
		b.WriteRune(ch)
	}
	b.WriteString(th.Reset)
	return b.String()
}

// circRender is the whole inline body — the lane strip plus an image-style
// dim tail: the live tally or the run hint, then the caption (the node name).
func circRender(h editor.NodeHost, n editor.NodeRef) string {
	th := editor.NodeTheme()
	u := n.UUID()
	cursor := -1
	if circFocus == u && circRuns[u] == nil {
		cursor = circCursors[u]
	}
	live, beat := false, 0
	if r := circRuns[u]; r != nil {
		live, beat = true, r.beat
	}
	line := circStrip(circGridOf(h, u), circItemsOf(u), cursor, live, beat)
	switch {
	case circRuns[u] != nil:
		line += " " + th.Yellow + fmt.Sprintf("live %d", circScores[u]) + th.Reset
	default:
		line += " " + th.Dim + "⌥r run" + th.Reset
	}
	if caption := strings.TrimSpace(n.Text()); caption != "" {
		line += th.Dim + " · " + th.Reset + facBlue + caption + th.Reset
	}
	return line
}

// circContext hands an agent the lane as its tile row (> < ^ v belts,
// D drill, O core, . floor).
func circContext(h editor.NodeHost, n editor.NodeRef) (string, string, string) {
	live := ""
	if circRuns[n.UUID()] != nil {
		live = fmt.Sprintf(` live="true" collected="%d"`, circScores[n.UUID()])
	}
	body := n.Text()
	if body != "" {
		body += "\n"
	}
	return "circuit", fmt.Sprintf(`w="%d"%s`, tileW, live), body + string(circGridOf(h, n.UUID()))
}

// ── building, inline (no expansion) ─────────────────────────────────────────

// circView is the inline builder: it renders NOTHING (Lines 0 — the row's own
// strip is the surface, the white cursor tile the only cue) and only captures
// keys while the cursor rests on the node (AutoFocus). ←/→ walk the lane,
// space lays a belt along the last move, r spins, d drill, o core, x erases;
// ↑/↓/esc fall through to the outline.
type circView struct{}

func (circView) Enter(h editor.NodeHost, n editor.NodeRef) bool {
	u := n.UUID()
	circGridOf(h, u) // make sure the lane is loaded
	if _, ok := circCursors[u]; !ok {
		circCursors[u] = tileW / 2
		circLastDir[u] = tileBeltR
	}
	circFocus = u
	return true
}

func (circView) Leave(h editor.NodeHost, n editor.NodeRef) {
	if circFocus == n.UUID() {
		circFocus = ""
	}
	if circRuns[n.UUID()] == nil { // a live lane is transient — don't save items mid-flight
		circSave(h, n.UUID())
	}
}

func (circView) Lines(h editor.NodeHost, n editor.NodeRef, width int) int { return 0 }

func (circView) Bands(h editor.NodeHost, n editor.NodeRef, rail string, width, scroll, winH int, focused bool) []string {
	return nil
}

func (circView) Key(h editor.NodeHost, n editor.NodeRef, k tea.KeyMsg) (tea.Cmd, bool) {
	u := n.UUID()
	cur := circCursors[u]
	place := func(v byte) {
		if circRuns[u] != nil {
			h.NodeFlash("factory is live · ⌥r stops it first")
			return
		}
		circGridOf(h, u)[cur] = v
	}
	switch k.String() {
	case "alt+r":
		return circToggle(h, n), true
	case "left":
		cur--
		circLastDir[u] = tileBeltL
	case "right":
		cur++
		circLastDir[u] = tileBeltR
	case " ", "b":
		place(circLastDir[u]) // a belt flowing the way you're moving
	case "r": // spin the belt under the cursor: > v < ^
		if t := circGridOf(h, u)[cur]; circIsBelt(t) {
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
		return nil, false // up/down/esc/enter … → the outline keeps them
	}
	if cur < 0 {
		cur = 0
	}
	if cur >= tileW {
		cur = tileW - 1
	}
	circCursors[u] = cur
	return nil, true
}
