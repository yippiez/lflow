package nodes

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lflow/lflow/pkg/tui/database"
	"github.com/lflow/lflow/pkg/tui/editor"
)

// The redstone node: a programmable Minecraft redstone machine. The circuit is
// a 2D character grid that IS the node's multi-line text (like the Code node's
// body), so mirrors, live sync, grep and the CLI all share it as plain text —
// and a built-in tick simulator runs it in place. The node wears the borderless
// code block (the grid source, auto-focused for editing like Code); alt+r on
// the focused block flips it into the SIMULATOR face: space steps one redstone
// tick, p auto-plays, digits flip levers / press buttons, x overlays dust power
// levels, r resets, esc returns to the source. Simulation state is ephemeral —
// never persisted or synced — only the grid text is shared.
//
// The cell alphabet (a flattened top-down Minecraft):
//
//	. or space  empty ground          #  solid block (dust weakly powers it)
//	- | +       redstone dust         *  block of redstone (always on)
//	T           torch: attached to any adjacent #, inverts it (1-tick delay)
//	> < ^ v     repeater facing that way (diode, 1-tick delay)
//	L           lever (digit-toggled)  B  button (digit-pressed, 6 ticks)
//	O           lamp (lit by any adjacent power)
//	;           starts a comment line
//
// Semantics kept from the game: dust decays 15 → 0, torches and repeaters
// switch one tick late (so feedback loops make real clocks), repeaters are
// diodes, dust weakly powers blocks (a weakly powered block drives torches and
// repeaters but never dust). Verticality, quasi-connectivity and comparators
// are out of scope.
//
// A node whose text is a single @directive line GENERATES its machine: alt+r
// expands @not, @or, @and, @latch or @clock [n] into a working circuit grid.

func init() {
	editor.RegisterNodePlugin(editor.NodePlugin{
		Key: database.TypeRedstone, Label: "Redstone",
		InlineEditable: false, // the grid block is the only editing surface
		AutoFocus:      true,  // rest the cursor on the block to edit, like Code
		Glyph:          func() (string, string) { return "⌁", editor.NodeTheme().Red },
		Render:         rsInlineRender, // compact fallback (the temp panel)
		Run:            runRedstone,
		View:           rsView{},
		BlockCode:      rsBlockCode,
		ToContext: func(h editor.NodeHost, n editor.NodeRef) (string, string, string) {
			body := strings.TrimRight(n.Text(), "\n")
			attrs := ""
			if c, err := rsParse(body); err == nil {
				attrs = fmt.Sprintf(`size="%dx%d"`, c.w, c.h)
			}
			return "redstone", attrs, body
		},
		OnRemove: func(h editor.NodeHost, uuid string) {
			delete(h.NodeStore(uuid), "redstone") // orphans any in-flight play tick
		},
	})
}

// ── the circuit ─────────────────────────────────────────────────────────────

type rsKind uint8

const (
	rsEmpty rsKind = iota
	rsDust
	rsBlock  // '#'
	rsSource // '*' block of redstone
	rsLever  // 'L'
	rsButton // 'B'
	rsTorch  // 'T'
	rsRep    // '>' '<' '^' 'v'
	rsLamp   // 'O'
)

// rsDirs is the 4-neighborhood; repeaters index into it for their facing.
var rsDirs = [4][2]int{{1, 0}, {-1, 0}, {0, -1}, {0, 1}} // > < ^ v

const (
	rsMaxW     = 96
	rsMaxH     = 48
	rsBtnTicks = 6   // a button press holds this many ticks
	rsProbeMax = 256 // headless settle/period search horizon
)

// rsCircuit is the parsed grid. Cells index as y*w+x.
type rsCircuit struct {
	w, h  int
	kind  []rsKind
	ch    []rune // the source character, for rendering
	dir   []int8 // repeater facing (index into rsDirs); -1 elsewhere
	inter []int  // levers and buttons in reading order (the digit keys)
}

func (c *rsCircuit) at(x, y int) int { return y*c.w + x }

// rsParse reads the grid DSL. Comment (';') lines vanish, blank edges trim,
// interior blank lines stay as empty rows so vertical spacing survives.
func rsParse(src string) (*rsCircuit, error) {
	var rows []string
	for _, l := range strings.Split(src, "\n") {
		if strings.HasPrefix(strings.TrimSpace(l), ";") {
			continue
		}
		rows = append(rows, strings.TrimRight(l, " "))
	}
	for len(rows) > 0 && rows[0] == "" {
		rows = rows[1:]
	}
	for len(rows) > 0 && rows[len(rows)-1] == "" {
		rows = rows[:len(rows)-1]
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("empty circuit — try @not, @or, @and, @latch or @clock")
	}
	w := 0
	for _, r := range rows {
		if n := len([]rune(r)); n > w {
			w = n
		}
	}
	h := len(rows)
	if w > rsMaxW || h > rsMaxH {
		return nil, fmt.Errorf("grid too large (%dx%d, max %dx%d)", w, h, rsMaxW, rsMaxH)
	}
	c := &rsCircuit{w: w, h: h, kind: make([]rsKind, w*h), ch: make([]rune, w*h), dir: make([]int8, w*h)}
	for i := range c.dir {
		c.dir[i] = -1
		c.ch[i] = '.'
	}
	for y, row := range rows {
		for x, r := range []rune(row) {
			i := c.at(x, y)
			c.ch[i] = r
			switch r {
			case '.', ' ':
				c.ch[i] = '.'
			case '-', '|', '+':
				c.kind[i] = rsDust
			case '#':
				c.kind[i] = rsBlock
			case '*':
				c.kind[i] = rsSource
			case 'L':
				c.kind[i] = rsLever
			case 'B':
				c.kind[i] = rsButton
			case 'T':
				c.kind[i] = rsTorch
			case 'O':
				c.kind[i] = rsLamp
			case '>', '<', '^', 'v':
				c.kind[i] = rsRep
				c.dir[i] = int8(strings.IndexRune("><^v", r))
			default:
				return nil, fmt.Errorf("row %d col %d: unknown cell %q", y+1, x+1, string(r))
			}
		}
	}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			i := c.at(x, y)
			if c.kind[i] == rsLever || c.kind[i] == rsButton {
				c.inter = append(c.inter, i)
			}
		}
	}
	return c, nil
}

// neighbors calls f with each in-grid 4-neighbor of cell i.
func (c *rsCircuit) neighbors(i int, f func(j int)) {
	x, y := i%c.w, i/c.w
	for _, d := range rsDirs {
		nx, ny := x+d[0], y+d[1]
		if nx >= 0 && nx < c.w && ny >= 0 && ny < c.h {
			f(c.at(nx, ny))
		}
	}
}

// facing returns the cell a repeater at i fires into, or -1 off-grid.
func (c *rsCircuit) facing(i int) int {
	if c.kind[i] != rsRep {
		return -1
	}
	d := rsDirs[c.dir[i]]
	x, y := i%c.w+d[0], i/c.w+d[1]
	if x < 0 || x >= c.w || y < 0 || y >= c.h {
		return -1
	}
	return c.at(x, y)
}

// ── the simulator ───────────────────────────────────────────────────────────

// rsSim holds one live run. The derived field (dust power, block power, lamps)
// always matches the component state; step() advances components one redstone
// tick from the current field, the game's 1-tick torch/repeater delay for free.
type rsSim struct {
	c        *rsCircuit
	torchLit []bool
	repOut   []bool
	leverOn  []bool
	btnLeft  []int // remaining held ticks per cell
	power    []int // dust power 0..15
	blockPow []bool
	lampLit  []bool
	tick     int
}

func newRsSim(c *rsCircuit) *rsSim {
	n := c.w * c.h
	s := &rsSim{
		c: c, torchLit: make([]bool, n), repOut: make([]bool, n),
		leverOn: make([]bool, n), btnLeft: make([]int, n),
		power: make([]int, n), blockPow: make([]bool, n), lampLit: make([]bool, n),
	}
	s.reset()
	return s
}

// emits reports whether cell j pours power onto an adjacent dust/lamp/block —
// the non-directional sources (repeaters are handled by facing).
func (s *rsSim) emits(j int) bool {
	switch s.c.kind[j] {
	case rsSource:
		return true
	case rsTorch:
		return s.torchLit[j]
	case rsLever:
		return s.leverOn[j]
	case rsButton:
		return s.btnLeft[j] > 0
	}
	return false
}

// repFiresInto reports a repeater at j firing into cell i.
func (s *rsSim) repFiresInto(j, i int) bool {
	return s.c.kind[j] == rsRep && s.repOut[j] && s.c.facing(j) == i
}

// recompute rebuilds the derived field from the component state: multi-source
// dust flood with 15→0 decay, then block and lamp power.
func (s *rsSim) recompute() {
	c := s.c
	var frontier []int
	for i := range s.power {
		s.power[i] = 0
		if c.kind[i] != rsDust {
			continue
		}
		seeded := false
		c.neighbors(i, func(j int) {
			if s.emits(j) || s.repFiresInto(j, i) {
				seeded = true
			}
		})
		if seeded {
			s.power[i] = 15
			frontier = append(frontier, i)
		}
	}
	for len(frontier) > 0 { // all seeds start at 15, so plain BFS decays correctly
		var next []int
		for _, i := range frontier {
			p := s.power[i] - 1
			if p <= 0 {
				continue
			}
			c.neighbors(i, func(j int) {
				if c.kind[j] == rsDust && s.power[j] < p {
					s.power[j] = p
					next = append(next, j)
				}
			})
		}
		frontier = next
	}
	for i := range s.blockPow {
		s.blockPow[i], s.lampLit[i] = false, false
	}
	for i := range s.blockPow { // blocks first: lamps below read fresh block power
		if c.kind[i] != rsBlock {
			continue
		}
		c.neighbors(i, func(j int) {
			// a torch never powers a block (in the game it powers the block
			// ABOVE, which this flat grid doesn't have)
			if (c.kind[j] == rsDust && s.power[j] > 0) ||
				(s.emits(j) && c.kind[j] != rsTorch) || s.repFiresInto(j, i) {
				s.blockPow[i] = true
			}
		})
	}
	for i := range s.lampLit {
		if c.kind[i] != rsLamp {
			continue
		}
		c.neighbors(i, func(j int) {
			if (c.kind[j] == rsDust && s.power[j] > 0) || s.emits(j) ||
				(c.kind[j] == rsBlock && s.blockPow[j]) || s.repFiresInto(j, i) {
				s.lampLit[i] = true
			}
		})
	}
}

// torchIn reports the torch at i reading power: any adjacent powered block
// (its attachment) or a repeater firing into it. Dust never drives a torch —
// in the game the dust would be powering the block the torch hangs on.
func (s *rsSim) torchIn(i int) bool {
	in := false
	s.c.neighbors(i, func(j int) {
		if (s.c.kind[j] == rsBlock && s.blockPow[j]) || s.repFiresInto(j, i) {
			in = true
		}
	})
	return in
}

// repIn reads the cell behind a repeater at i.
func (s *rsSim) repIn(i int) bool {
	c := s.c
	d := rsDirs[c.dir[i]]
	x, y := i%c.w-d[0], i/c.w-d[1]
	if x < 0 || x >= c.w || y < 0 || y >= c.h {
		return false
	}
	b := c.at(x, y)
	switch c.kind[b] {
	case rsDust:
		return s.power[b] > 0
	case rsBlock:
		return s.blockPow[b]
	case rsRep:
		return s.repOut[b] && c.facing(b) == i
	}
	return s.emits(b)
}

// step advances one redstone tick: torches and repeaters read the current
// field, switch together, and the field rebuilds.
func (s *rsSim) step() {
	c := s.c
	nextTorch := make([]bool, len(s.torchLit))
	nextRep := make([]bool, len(s.repOut))
	for i := range c.kind {
		switch c.kind[i] {
		case rsTorch:
			nextTorch[i] = !s.torchIn(i)
		case rsRep:
			nextRep[i] = s.repIn(i)
		}
	}
	s.torchLit, s.repOut = nextTorch, nextRep
	for i := range s.btnLeft {
		if s.btnLeft[i] > 0 {
			s.btnLeft[i]--
		}
	}
	s.tick++
	s.recompute()
}

// reset zeroes the run, then settles components one at a time in reading order
// — sequential relaxation finds a stable state when one exists (so a latch
// wakes latched instead of blinking), and leaves a clock free to oscillate.
func (s *rsSim) reset() {
	for i := range s.torchLit {
		s.torchLit[i], s.repOut[i] = false, false
		s.leverOn[i], s.btnLeft[i] = false, 0
	}
	s.tick = 0
	s.recompute()
	for sweep := 0; sweep < 32; sweep++ {
		changed := false
		for i := range s.c.kind {
			switch s.c.kind[i] {
			case rsTorch:
				if want := !s.torchIn(i); want != s.torchLit[i] {
					s.torchLit[i], changed = want, true
					s.recompute()
				}
			case rsRep:
				if want := s.repIn(i); want != s.repOut[i] {
					s.repOut[i], changed = want, true
					s.recompute()
				}
			}
		}
		if !changed {
			return
		}
	}
}

// toggle flips lever n / presses button n (0-based index into inter).
func (s *rsSim) toggle(n int) bool {
	if n < 0 || n >= len(s.c.inter) {
		return false
	}
	i := s.c.inter[n]
	if s.c.kind[i] == rsLever {
		s.leverOn[i] = !s.leverOn[i]
	} else {
		s.btnLeft[i] = rsBtnTicks
	}
	s.recompute()
	return true
}

// sig fingerprints the mutable state for settle/period detection.
func (s *rsSim) sig() string {
	var b strings.Builder
	for i := range s.torchLit {
		switch {
		case s.torchLit[i]:
			b.WriteByte('t')
		case s.repOut[i]:
			b.WriteByte('r')
		case s.btnLeft[i] > 0:
			b.WriteByte(strconv.Itoa(s.btnLeft[i])[0])
		default:
			b.WriteByte('.')
		}
	}
	return b.String()
}

func (s *rsSim) lamps() (lit, total int) {
	for i, k := range s.c.kind {
		if k == rsLamp {
			total++
			if s.lampLit[i] {
				lit++
			}
		}
	}
	return
}

// probe runs a shadow copy of the sim forward and reports how it behaves:
// settled ("stable" from here, at tick n) or oscillating with a period.
func (s *rsSim) probe() (settled bool, at, period int) {
	sh := newRsSim(s.c)
	copy(sh.torchLit, s.torchLit)
	copy(sh.repOut, s.repOut)
	copy(sh.leverOn, s.leverOn)
	copy(sh.btnLeft, s.btnLeft)
	sh.tick = 0
	sh.recompute()
	seen := map[string]int{sh.sig(): 0}
	for t := 1; t <= rsProbeMax; t++ {
		sh.step()
		sig := sh.sig()
		if first, ok := seen[sig]; ok {
			if p := t - first; p == 1 {
				return true, first, 0
			} else {
				return false, first, p
			}
		}
		seen[sig] = t
	}
	return false, rsProbeMax, 0
}

// ── generated machines (@directives) ────────────────────────────────────────

// rsTemplate expands a "@name [n]" directive into a circuit grid, or "" when
// the text is not a directive.
func rsTemplate(text string) (name, grid string, ok bool) {
	t := strings.TrimSpace(text)
	if !strings.HasPrefix(t, "@") || strings.Contains(t, "\n") {
		return "", "", false
	}
	f := strings.Fields(t[1:])
	if len(f) == 0 {
		return "", "", false
	}
	arg := 0
	if len(f) > 1 {
		arg, _ = strconv.Atoi(f[1])
	}
	switch f[0] {
	case "not": // lamp = NOT lever: dust powers the torch's block
		return "not", "L-#T-O", true
	case "or": // either lever powers the shared net
		return "or", strings.Join([]string{
			"L-+",
			"..|",
			"L-+-O",
		}, "\n"), true
	case "and": // two inverters onto a shared net, inverted again
		return "and", strings.Join([]string{
			"L-#T+",
			"....|",
			"L-#T+#T-O",
		}, "\n"), true
	case "latch": // RS latch: two inverters in a ring; lever 1 sets, 2 resets
		return "latch", strings.Join([]string{
			"L-#T+..",
			"..+.|.O",
			"..|.#T+",
			"..|.L.|",
			"..|...|",
			"..+---+",
		}, "\n"), true
	case "clock": // a torch feeding its own block back through n repeaters
		n := arg
		if n < 0 {
			n = 0
		}
		if n > 8 {
			n = 8
		}
		w := 3 + n
		return "clock", strings.Join([]string{
			"#T" + strings.Repeat(">", n) + "+",
			"|" + strings.Repeat(".", w-2) + "|",
			"+" + strings.Repeat("-", w-2) + "+",
		}, "\n"), true
	}
	return "", "", false
}

// ── the node face ───────────────────────────────────────────────────────────

// rsState is the ephemeral per-node state (NodeStore key "redstone"): the block
// edit buffer plus the live simulation — run state is never persisted.
type rsState struct {
	buf    string
	caret  int
	simOn  bool
	levels bool // dust cells show their power level
	play   bool
	sim    *rsSim
}

func rsStateOf(h editor.NodeHost, uuid string) *rsState {
	d := h.NodeStore(uuid)
	st, _ := d["redstone"].(*rsState)
	if st == nil {
		st = &rsState{}
		d["redstone"] = st
	}
	return st
}

// rsInlineRender is the compact one-line fallback body (the temp panel and any
// surface that can't hang the block).
func rsInlineRender(h editor.NodeHost, n editor.NodeRef) string {
	th := editor.NodeTheme()
	body := strings.TrimSpace(n.Text())
	if body == "" {
		return th.Dim + "redstone · empty" + th.Reset
	}
	if name, _, ok := rsTemplate(body); ok {
		return th.Dim + "redstone · @" + name + th.Reset
	}
	if c, err := rsParse(body); err == nil {
		return th.Dim + fmt.Sprintf("redstone · %dx%d", c.w, c.h) + th.Reset
	}
	return th.Dim + "redstone" + th.Reset
}

// rsBlockCode renders the unfocused node as the grid source block; the focused
// view drives its own bands (source with caret, or the simulator).
func rsBlockCode(h editor.NodeHost, n editor.NodeRef, focused bool) (string, int, bool) {
	st := rsStateOf(h, n.UUID())
	if focused {
		if st.simOn {
			return "", -1, false // the view's Bands draw the simulator
		}
		return st.buf, st.caret, true
	}
	return n.Text(), -1, true
}

// runRedstone is alt+r on an UNFOCUSED redstone node (the focused block routes
// alt+r through the view): expand a directive, else report the headless run.
func runRedstone(h editor.NodeHost, n editor.NodeRef) tea.Cmd {
	if name, grid, ok := rsTemplate(n.Text()); ok {
		n.SetText(grid)
		h.NodeFlash("generated @" + name + " · alt+r simulates")
		return nil
	}
	c, err := rsParse(n.Text())
	if err != nil {
		h.NodeFlash("redstone: " + err.Error())
		return nil
	}
	s := newRsSim(c)
	h.NodeFlash(rsProbeLine(s))
	return nil
}

// rsProbeLine words one probe as a status line.
func rsProbeLine(s *rsSim) string {
	settled, at, period := s.probe()
	lit, total := s.lamps()
	lamps := ""
	if total > 0 {
		lamps = fmt.Sprintf(" · %d/%d lamps lit", lit, total)
	}
	switch {
	case settled:
		return fmt.Sprintf("stable after %d ticks%s", at, lamps)
	case period > 0:
		return fmt.Sprintf("oscillates · period %d ticks (from tick %d)", period, at)
	default:
		return "no steady state within " + strconv.Itoa(rsProbeMax) + " ticks"
	}
}

// rsPlayMsg is the auto-play heartbeat (editor.NodePluginMsg).
type rsPlayMsg struct{ uuid string }

func rsPlayTick(uuid string) tea.Cmd {
	return tea.Tick(200*time.Millisecond, func(time.Time) tea.Msg { return rsPlayMsg{uuid: uuid} })
}

// HandleNodePlugin advances one auto-play tick and re-arms while playing.
func (msg rsPlayMsg) HandleNodePlugin(h editor.NodeHost) tea.Cmd {
	st := rsStateOf(h, msg.uuid)
	if !st.play || !st.simOn || st.sim == nil {
		return nil
	}
	st.sim.step()
	return rsPlayTick(msg.uuid)
}

// ── the focused view: source editor ⇄ simulator ─────────────────────────────

// rsView is the focused block. Source face: the shared gray code block, edited
// exactly like the Code node. alt+r flips to the sim face; there space steps,
// p plays, digits work the levers/buttons, x shows power levels, r resets,
// alt+r/esc return to the source.
type rsView struct{}

func (rsView) Enter(h editor.NodeHost, n editor.NodeRef) bool {
	st := rsStateOf(h, n.UUID())
	if !st.simOn {
		t := n.Text()
		st.buf, st.caret = t, len([]rune(t))
	}
	return true
}

// Leave flushes source edits back to the node text; the sim (and its lever
// state) survives in the store so cursoring away doesn't kill a running clock.
func (rsView) Leave(h editor.NodeHost, n editor.NodeRef) {
	st := rsStateOf(h, n.UUID())
	if !st.simOn && st.buf != n.Text() {
		n.SetText(st.buf)
	}
}

func (rsView) Lines(h editor.NodeHost, n editor.NodeRef, width int) int {
	st := rsStateOf(h, n.UUID())
	if st.simOn && st.sim != nil {
		return st.sim.c.h + 2
	}
	return 2 + len(strings.Split(st.buf, "\n"))
}

func (v rsView) Key(h editor.NodeHost, n editor.NodeRef, k tea.KeyMsg) (tea.Cmd, bool) {
	st := rsStateOf(h, n.UUID())
	if st.simOn {
		return v.simKey(h, n, st, k)
	}
	switch k.String() {
	case "alt+r":
		if name, grid, ok := rsTemplate(st.buf); ok {
			st.buf, st.caret = grid, 0
			n.SetText(grid)
			h.NodeFlash("generated @" + name + " · alt+r simulates")
			return nil, true
		}
		c, err := rsParse(st.buf)
		if err != nil {
			h.NodeFlash("redstone: " + err.Error())
			return nil, true
		}
		if st.buf != n.Text() {
			n.SetText(st.buf) // entering the sim commits the grid
		}
		st.sim, st.simOn = newRsSim(c), true
		h.NodeFlash(rsProbeLine(st.sim))
		return nil, true
	}
	return v.editKey(st, k)
}

// editKey is the source face's caret editing — the Code node's key table.
func (rsView) editKey(st *rsState, k tea.KeyMsg) (tea.Cmd, bool) {
	buf, caret := st.buf, st.caret
	rl := []rune(buf)
	switch k.String() {
	case "left":
		if caret > 0 {
			caret--
		}
	case "right":
		if caret < len(rl) {
			caret++
		}
	case "up":
		if line, _ := editor.NodeCaretLineCol(buf, caret); line == 0 {
			return nil, false // cross to the previous outline row
		}
		caret = editor.NodeCaretVMove(buf, caret, -1)
	case "down":
		if line, _ := editor.NodeCaretLineCol(buf, caret); line == strings.Count(buf, "\n") {
			return nil, false // cross to the next outline row
		}
		caret = editor.NodeCaretVMove(buf, caret, +1)
	case "home":
		line, _ := editor.NodeCaretLineCol(buf, caret)
		caret = editor.NodeCaretAt(buf, line, 0)
	case "end":
		line, _ := editor.NodeCaretLineCol(buf, caret)
		caret = editor.NodeCaretAt(buf, line, 1<<30)
	case "enter":
		buf = string(rl[:caret]) + "\n" + string(rl[caret:])
		caret++
	case "tab":
		buf = string(rl[:caret]) + "  " + string(rl[caret:])
		caret += 2
	case "backspace":
		if caret > 0 {
			buf = string(rl[:caret-1]) + string(rl[caret:])
			caret--
		}
	default:
		switch {
		case k.Type == tea.KeySpace && !k.Alt:
			buf = string(rl[:caret]) + " " + string(rl[caret:])
			caret++
		case k.Type == tea.KeyRunes && !k.Alt:
			s := string(k.Runes)
			buf = string(rl[:caret]) + s + string(rl[caret:])
			caret += len(k.Runes)
		default:
			return nil, false // esc, alt+e, ctrl+c … → central
		}
	}
	st.buf, st.caret = buf, caret
	return nil, true
}

// simKey works the simulator face.
func (rsView) simKey(h editor.NodeHost, n editor.NodeRef, st *rsState, k tea.KeyMsg) (tea.Cmd, bool) {
	if k.Type == tea.KeySpace && !k.Alt {
		st.sim.step()
		return nil, true
	}
	switch ks := k.String(); ks {
	case "esc", "alt+r":
		st.simOn, st.play = false, false
		st.buf, st.caret = n.Text(), len([]rune(n.Text()))
		return nil, true
	case "t":
		st.sim.step()
		return nil, true
	case "p":
		st.play = !st.play
		if st.play {
			return rsPlayTick(n.UUID()), true
		}
		return nil, true
	case "r":
		st.sim.reset()
		h.NodeFlash(rsProbeLine(st.sim))
		return nil, true
	case "x":
		st.levels = !st.levels
		return nil, true
	default:
		if len(ks) == 1 && ks[0] >= '1' && ks[0] <= '9' {
			if !st.sim.toggle(int(ks[0]-'1')) {
				h.NodeFlash("no lever/button " + ks)
			}
			return nil, true
		}
	}
	return nil, false // arrows, esc-alternatives … → central
}

func (rsView) Bands(h editor.NodeHost, n editor.NodeRef, rail string, width, scroll, winH int, focused bool) []string {
	st := rsStateOf(h, n.UUID())
	if !st.simOn || st.sim == nil {
		caret := st.caret
		if !focused {
			caret = -1
		}
		return editor.CodeBlockBands(st.buf, caret, focused, rail, width, scroll, winH)
	}
	content := rsSimBands(st, width-editor.NodeVisibleWidth(rail))
	out := make([]string, len(content))
	for i, l := range content {
		out[i] = rail + editor.NodeClip(l, width-editor.NodeVisibleWidth(rail))
	}
	if winH <= 0 {
		return out
	}
	return editor.NodeWindowBands(out, scroll, winH)
}

// rsSimBands renders the simulator: a header, the live grid, a key-help footer.
func rsSimBands(st *rsState, width int) []string {
	th := editor.NodeTheme()
	s := st.sim
	out := make([]string, 0, s.c.h+2)

	head := th.Red + "⌁ redstone" + th.Reset + th.Dim + fmt.Sprintf(" · tick %d", s.tick) + th.Reset
	if lit, total := s.lamps(); total > 0 {
		head += th.Dim + " · " + th.Reset + th.Yellow + fmt.Sprintf("%d/%d ●", lit, total) + th.Reset
	}
	if st.play {
		head += th.Dim + " · playing" + th.Reset
	}
	out = append(out, head)

	for y := 0; y < s.c.h; y++ {
		var b strings.Builder
		b.WriteString("  ")
		for x := 0; x < s.c.w; x++ {
			b.WriteString(rsCellFace(st, s.c.at(x, y)))
		}
		out = append(out, b.String())
	}

	var tags []string
	for n, i := range s.c.inter {
		lab := fmt.Sprintf("%d:%s", n+1, string(s.c.ch[i]))
		if s.emits(i) {
			tags = append(tags, th.Green+lab+" on"+th.Reset)
		} else {
			tags = append(tags, th.Dim+lab+" off"+th.Reset)
		}
	}
	foot := th.Dim + "space tick · p play · r reset · x levels · esc source" + th.Reset
	if len(tags) > 0 {
		foot = strings.Join(tags, th.Dim+" · "+th.Reset) + th.Dim + " ── " + th.Reset + foot
	}
	return append(out, foot)
}

// rsCellFace draws one grid cell with its live state color.
func rsCellFace(st *rsState, i int) string {
	th := editor.NodeTheme()
	s := st.sim
	c := s.c
	ch := string(c.ch[i])
	switch c.kind[i] {
	case rsEmpty:
		return th.Dim + "·" + th.Reset
	case rsDust:
		if p := s.power[i]; p > 0 {
			if st.levels {
				ch = strings.ToUpper(strconv.FormatInt(int64(p), 16))
			}
			return th.Red + ch + th.Reset
		}
		if st.levels {
			ch = "0"
		}
		return th.Dim + ch + th.Reset
	case rsBlock:
		if s.blockPow[i] {
			return th.Accent + ch + th.Reset
		}
		return th.FG + ch + th.Reset
	case rsSource:
		return th.Red + ch + th.Reset
	case rsTorch:
		if s.torchLit[i] {
			return th.Red + ch + th.Reset
		}
		return th.Dim + ch + th.Reset
	case rsRep:
		if s.repOut[i] {
			return th.Red + ch + th.Reset
		}
		return th.Dim + ch + th.Reset
	case rsLever, rsButton:
		if s.emits(i) {
			return th.Green + ch + th.Reset
		}
		return th.FG + ch + th.Reset
	case rsLamp:
		if s.lampLit[i] {
			return th.Yellow + "●" + th.Reset
		}
		return th.Dim + "○" + th.Reset
	}
	return ch
}
