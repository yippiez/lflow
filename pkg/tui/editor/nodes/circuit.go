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

// The circuit node ▚ — a drawn machine. The node's body is a half-block pixel
// canvas (the image node's pixelated look, hung beneath the row as always-on
// preview bands): you DRAW the machine on it — dark-blue conductor tracks on a
// deep-navy board — and the drawing acts as a LIVE circuit. alt+e opens the
// crosshair painter (arrows move, space paints conductor, f seeds a yellow
// electron, x erases); alt+r sets the electrons flowing under Wireworld rules
// (head → tail → conductor; a conductor cell fires when exactly 1 or 2 of its
// eight neighbors are heads), alt+r again stops and the drawing snaps back to
// how it was. Machines COMPOSE: contiguous circuit-typed SIBLINGS fuse their
// canvases top-to-bottom into ONE board, so electrons cross node seams and a
// stack of nodes is a single live system. The node text is a free label.
//
// The drawing persists locally in node_output (like the nlpcompute cell —
// local, never synced); the live simulation is ephemeral package state, gone
// on restart, and every run starts from — and stopping restores — the saved
// drawing.
//
// WARNING (invariant): the simulation never runs on its own — alt+r only.

// The board: fixed cells; every canvas is circW wide so stacked nodes seam
// cleanly. Two pixel rows per text row (the ▀ half-block).
const (
	circW = 44
	circH = 10
)

const circTickEvery = 90 * time.Millisecond

// cell values (also the persisted row runes).
const (
	circEmpty = '.'
	circCond  = 'C'
	circHead  = 'H'
	circTail  = 'T'
)

// The board palette — the blueprint look: deep-navy board, steel-blue
// conductor, yellow electron heads cooling through amber tails.
var circRGB = map[byte][3]int{
	circEmpty: {15, 22, 38},
	circCond:  {74, 112, 176},
	circHead:  {255, 215, 95},
	circTail:  {214, 148, 62},
}

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

// The family palette (shared with the row chrome): dark blue + yellow.
const (
	facBlue = "\x1b[38;2;100;148;220m" // steel blue — glyph + label
	facDeep = "\x1b[38;2;58;92;150m"   // dark blue — chrome
)

// ── canvas state ────────────────────────────────────────────────────────────

// circGrids holds each node's LIVE canvas (edits and simulation both act on
// it); circCursors the painter's crosshair; circRuns the running simulation a
// node belongs to. All package state keyed by uuid — ephemeral, event-loop
// only (the Prefix hook has no NodeHost, and the sim ticks arrive as plugin
// messages on the same loop).
var (
	circGrids   = map[string][][]byte{}
	circCursors = map[string][2]int{}
	circRuns    = map[string]*circRun{}
)

// circData is the persisted drawing (node_output JSON).
type circData struct {
	W    int      `json:"w"`
	H    int      `json:"h"`
	Rows []string `json:"rows"`
}

// circBlank returns an empty board.
func circBlank() [][]byte {
	g := make([][]byte, circH)
	for y := range g {
		g[y] = []byte(strings.Repeat(string(rune(circEmpty)), circW))
	}
	return g
}

// circGridOf returns the node's live canvas, loading the persisted drawing on
// first touch (blank board when none). Rows are normalized to circW×circH so
// stacked canvases always seam.
func circGridOf(h editor.NodeHost, uuid string) [][]byte {
	if g, ok := circGrids[uuid]; ok {
		return g
	}
	g := circBlank()
	if db := h.NodeDB(); db != nil {
		if raw, err := database.LoadNodeOutput(db, uuid); err == nil && raw != "" {
			var d circData
			if json.Unmarshal([]byte(raw), &d) == nil {
				for y := 0; y < circH && y < len(d.Rows); y++ {
					row := []byte(d.Rows[y])
					for x := 0; x < circW && x < len(row); x++ {
						switch row[x] {
						case circCond, circHead, circTail:
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

// circSave persists the node's live canvas as its drawing.
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
	if raw, err := json.Marshal(circData{W: circW, H: circH, Rows: rows}); err == nil {
		_ = database.SaveNodeOutput(db, uuid, string(raw))
	}
}

// circCopy deep-copies a canvas (run snapshots).
func circCopy(g [][]byte) [][]byte {
	out := make([][]byte, len(g))
	for y := range g {
		out[y] = append([]byte(nil), g[y]...)
	}
	return out
}

// ── the live simulation ─────────────────────────────────────────────────────

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

// circRun is one live simulation over a fused board.
type circRun struct {
	uuids []string            // stack members, top-down
	snaps map[string][][]byte // the drawing as it was — restored on stop
	stop  bool
}

// circToggle (alt+r) sets the stack's electrons flowing, or stops them and
// restores the drawing.
func circToggle(h editor.NodeHost, n editor.NodeRef) tea.Cmd {
	if r := circRuns[n.UUID()]; r != nil {
		circStopRun(r)
		h.NodeFlash("circuit stopped · drawing restored")
		return nil
	}
	stack := circStack(n)
	r := &circRun{snaps: map[string][][]byte{}}
	for _, m := range stack {
		u := m.UUID()
		circSave(h, u) // running is committing: the board you see is the drawing you keep
		r.uuids = append(r.uuids, u)
		r.snaps[u] = circCopy(circGridOf(h, u))
		circRuns[u] = r
	}
	h.NodeFlash("circuit live · ⌥r stops")
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
		delete(circRuns, u)
	}
}

// circOnRemove stops the simulation a removed node belongs to and drops its
// canvas state.
func circOnRemove(h editor.NodeHost, uuid string) {
	if r := circRuns[uuid]; r != nil {
		circStopRun(r)
	}
	delete(circGrids, uuid)
	delete(circCursors, uuid)
}

// circTickMsg is one simulation beat (editor.NodePluginMsg).
type circTickMsg struct{ run *circRun }

func circTickCmd(r *circRun) tea.Cmd {
	return tea.Tick(circTickEvery, func(time.Time) tea.Msg { return circTickMsg{run: r} })
}

// HandleNodePlugin advances the fused board one Wireworld generation and
// re-arms the beat while the run lives.
func (msg circTickMsg) HandleNodePlugin(h editor.NodeHost) tea.Cmd {
	r := msg.run
	if r.stop {
		return nil
	}
	// fuse member canvases top-to-bottom, step, split back — electrons cross
	// the node seams because the board steps as one grid
	var board [][]byte
	for _, u := range r.uuids {
		board = append(board, circGridOf(h, u)...)
	}
	board = circStep(board)
	for i, u := range r.uuids {
		circGrids[u] = board[i*circH : (i+1)*circH]
	}
	return circTickCmd(r)
}

// circStep is one Wireworld generation: H→T, T→C, and a conductor fires when
// exactly 1 or 2 of its eight neighbors are heads.
func circStep(g [][]byte) [][]byte {
	hh := len(g)
	if hh == 0 {
		return g
	}
	ww := len(g[0])
	out := make([][]byte, hh)
	for y := range g {
		out[y] = make([]byte, ww)
		for x, c := range g[y] {
			switch c {
			case circHead:
				out[y][x] = circTail
			case circTail:
				out[y][x] = circCond
			case circCond:
				heads := 0
				for dy := -1; dy <= 1; dy++ {
					for dx := -1; dx <= 1; dx++ {
						if dy == 0 && dx == 0 {
							continue
						}
						ny, nx := y+dy, x+dx
						if ny >= 0 && ny < hh && nx >= 0 && nx < ww && g[ny][nx] == circHead {
							heads++
						}
					}
				}
				if heads == 1 || heads == 2 {
					out[y][x] = circHead
				} else {
					out[y][x] = circCond
				}
			default:
				out[y][x] = circEmpty
			}
		}
	}
	return out
}

// ── the look ────────────────────────────────────────────────────────────────

// circPrefix chips the row while the machine is live.
func circPrefix(uuid string) string {
	if circRuns[uuid] != nil {
		th := editor.NodeTheme()
		return th.Yellow + "live " + th.Reset
	}
	return ""
}

// circBandLines renders a canvas as half-block pixel rows (two cells per
// character, like the image thumbnail); cursor ≥ 0 coordinates draw the
// painter's crosshair cell in white.
func circBandLines(g [][]byte, rail string, cx, cy int) []string {
	th := editor.NodeTheme()
	out := make([]string, 0, (len(g)+1)/2)
	for y := 0; y < len(g); y += 2 {
		var b strings.Builder
		b.WriteString(rail + th.Reset + "  ")
		for x := 0; x < len(g[y]); x++ {
			up := circRGB[g[y][x]]
			lo := circRGB[byte(circEmpty)]
			if y+1 < len(g) {
				lo = circRGB[g[y+1][x]]
			}
			if x == cx && y == cy {
				up = [3]int{245, 245, 245}
			}
			if x == cx && y+1 == cy {
				lo = [3]int{245, 245, 245}
			}
			fmt.Fprintf(&b, "\x1b[38;2;%d;%d;%dm\x1b[48;2;%d;%d;%dm▀",
				up[0], up[1], up[2], lo[0], lo[1], lo[2])
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
	lines := circBandLines(circGridOf(h, n.UUID()), rail, -1, -1)
	for i := range lines {
		lines[i] = editor.NodeClip(lines[i], maxLine)
	}
	return lines
}

// circContext hands an agent the machine as its cell rows (C conductor,
// H/T electron head/tail, . empty).
func circContext(h editor.NodeHost, n editor.NodeRef) (string, string, string) {
	g := circGridOf(h, n.UUID())
	rows := make([]string, len(g))
	for y := range g {
		rows[y] = string(g[y])
	}
	live := ""
	if circRuns[n.UUID()] != nil {
		live = ` live="true"`
	}
	body := n.Text()
	if body != "" {
		body += "\n"
	}
	return "circuit", fmt.Sprintf(`w="%d" h="%d"%s`, circW, circH, live), body + strings.Join(rows, "\n")
}

// ── the painter (alt+e) ─────────────────────────────────────────────────────

// circView is the crosshair painter: the same pixel bands with a white cursor
// cell, keys painting the board. Stateless; cursor in circCursors, canvas in
// circGrids, persisted on leave.
type circView struct{}

func (circView) Enter(h editor.NodeHost, n editor.NodeRef) bool {
	circGridOf(h, n.UUID()) // make sure the board is loaded
	if _, ok := circCursors[n.UUID()]; !ok {
		circCursors[n.UUID()] = [2]int{circW / 2, circH / 2}
	}
	return true
}

// Leave persists the drawing.
func (circView) Leave(h editor.NodeHost, n editor.NodeRef) {
	if circRuns[n.UUID()] == nil { // a live board is transient — don't save electrons mid-flight
		circSave(h, n.UUID())
	}
}

func (circView) Lines(h editor.NodeHost, n editor.NodeRef, width int) int {
	return 1 + (circH+1)/2
}

func (circView) Key(h editor.NodeHost, n editor.NodeRef, k tea.KeyMsg) (tea.Cmd, bool) {
	u := n.UUID()
	cur := circCursors[u]
	paint := func(v byte) {
		if circRuns[u] != nil {
			h.NodeFlash("circuit is live · ⌥r stops it first")
			return
		}
		circGridOf(h, u)[cur[1]][cur[0]] = v
	}
	switch k.String() {
	case "alt+r":
		return circToggle(h, n), true
	case "left":
		cur[0]--
	case "right":
		cur[0]++
	case "up":
		cur[1]--
	case "down":
		cur[1]++
	case " ", "d":
		paint(circCond)
	case "f":
		paint(circHead)
	case "x", "backspace":
		paint(circEmpty)
	default:
		if k.Type == tea.KeySpace {
			paint(circCond)
			return nil, true
		}
		return nil, false // esc, ctrl+c … → central
	}
	if cur[0] < 0 {
		cur[0] = 0
	}
	if cur[0] >= circW {
		cur[0] = circW - 1
	}
	if cur[1] < 0 {
		cur[1] = 0
	}
	if cur[1] >= circH {
		cur[1] = circH - 1
	}
	circCursors[u] = cur
	return nil, true
}

func (circView) Bands(h editor.NodeHost, n editor.NodeRef, rail string, width, scroll, winH int, focused bool) []string {
	th := editor.NodeTheme()
	u := n.UUID()
	hdr := "  circuit · space/d draw · f electron · x erase · ⌥r run · esc close"
	if circRuns[u] != nil {
		hdr = "  circuit · live · ⌥r stops · esc close"
	}
	cx, cy := -1, -1
	if focused && circRuns[u] == nil {
		c := circCursors[u]
		cx, cy = c[0], c[1]
	}
	content := []string{editor.NodeClip(rail+th.Reset+th.Dim+hdr+th.Reset, width)}
	for _, l := range circBandLines(circGridOf(h, u), rail, cx, cy) {
		content = append(content, editor.NodeClip(l, width))
	}
	return editor.NodeWindowBands(content, 0, winH)
}
