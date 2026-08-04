package editor

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/lflow/lflow/packages/database"
)

// vtGrid is a deliberately small virtual terminal: it replays the exact byte
// stream bubbletea's inline (no-alt-screen) standard renderer would emit for a
// frame and records what each cell ends up holding. It models only the control
// sequences that renderer produces — CursorUp, carriage return, newline,
// EraseLineRight (\x1b[K), EraseScreenBelow (\x1b[J) — plus printable runes, so
// a stale-cell overlay shows up as leftover text on a row.
type vtGrid struct {
	rows [][]rune
	cur  int // current row
	col  int
}

func newVTGrid(height int) *vtGrid {
	g := &vtGrid{rows: make([][]rune, height)}
	for i := range g.rows {
		g.rows[i] = []rune{}
	}
	return g
}

func (g *vtGrid) ensure(row int) {
	for row >= len(g.rows) {
		g.rows = append(g.rows, []rune{})
	}
}

func (g *vtGrid) putRune(r rune) {
	g.ensure(g.cur)
	for len(g.rows[g.cur]) <= g.col {
		g.rows[g.cur] = append(g.rows[g.cur], ' ')
	}
	g.rows[g.cur][g.col] = r
	g.col++
}

func (g *vtGrid) eraseLineRight() {
	g.ensure(g.cur)
	if g.col < len(g.rows[g.cur]) {
		g.rows[g.cur] = g.rows[g.cur][:g.col]
	}
}

func (g *vtGrid) eraseScreenBelow() {
	g.eraseLineRight()
	for r := g.cur + 1; r < len(g.rows); r++ {
		g.rows[r] = []rune{}
	}
}

// clearScreen models tea.ClearScreen: ESC[2J wipes every cell and ESC[H homes
// the cursor to the top-left. The inline renderer issues this exact pair when
// the resize handler returns tea.ClearScreen.
func (g *vtGrid) clearScreen() {
	for r := range g.rows {
		g.rows[r] = []rune{}
	}
	g.cur = 0
	g.col = 0
}

// write replays one rendered byte stream against the grid.
func (g *vtGrid) write(s string) {
	b := []byte(s)
	for i := 0; i < len(b); {
		if b[i] == '\x1b' && i+1 < len(b) && b[i+1] == '[' {
			// Parse a CSI sequence: ESC [ params final.
			j := i + 2
			for j < len(b) && (b[j] >= '0' && b[j] <= '9' || b[j] == ';') {
				j++
			}
			if j >= len(b) {
				break
			}
			params := string(b[i+2 : j])
			final := b[j]
			switch final {
			case 'A': // cursor up
				n := atoiOr(params, 1)
				g.cur -= n
				if g.cur < 0 {
					g.cur = 0
				}
			case 'K': // erase line right
				g.eraseLineRight()
			case 'J': // erase: 2 = whole screen (ClearScreen), else below cursor
				if atoiOr(params, 0) == 2 {
					g.clearScreen()
				} else {
					g.eraseScreenBelow()
				}
			case 'H': // cursor home (ClearScreen pairs ESC[2J with ESC[H)
				g.cur = 0
				g.col = 0
			case 'm': // SGR colour — no cell effect
			default:
				// CursorPosition etc. are unused by the inline renderer here.
			}
			i = j + 1
			continue
		}
		switch b[i] {
		case '\r':
			g.col = 0
		case '\n':
			g.cur++
			g.ensure(g.cur)
		default:
			// decode one rune
			r := rune(b[i])
			size := 1
			if b[i] >= 0x80 {
				rr := []rune(string(b[i:]))
				if len(rr) > 0 {
					r = rr[0]
					size = len(string(r))
				}
			}
			g.putRune(r)
			i += size
			continue
		}
		i++
	}
}

func atoiOr(s string, def int) int {
	if s == "" {
		return def
	}
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return def
		}
		n = n*10 + int(c-'0')
	}
	return n
}

// flushSim reproduces bubbletea's standardRenderer.flush for the alt-screen
// path: home the cursor to the top-left (not cursor-up over the previous
// frame's line count — altScreenActive always repositions with
// CursorHomePosition, so a reflow that changes how many physical rows the
// previous frame occupied can never desync it), then for each line truncate to
// the terminal width and emit it, appending EraseLineRight only when the
// visible content is narrower than the terminal, and erasing the screen below
// when the new frame has fewer lines than the last. A resize is modelled by
// passing a nil prev (bubbletea's renderer drops its line cache on every
// WindowSizeMsg — see standardRenderer's WindowSizeMsg case — independent of
// whatever Cmd the app's own Update returns).
type flushSim struct {
	g             *vtGrid
	prevLines     []string
	linesRendered int
	width         int
	firstRender   bool
}

func (s *flushSim) flush(frame string) {
	newLines := strings.Split(frame, "\n")
	s.g.write(ansi.CursorHomePosition)
	for i := 0; i < len(newLines); i++ {
		canSkip := len(s.prevLines) > i && s.prevLines[i] == newLines[i]
		if canSkip {
			if i < len(newLines)-1 {
				s.g.write("\n")
			}
			continue
		}
		if i == 0 && s.firstRender {
			s.g.write("\r")
		}
		line := newLines[i]
		if s.width > 0 {
			line = ansi.Truncate(line, s.width, "")
		}
		if ansi.StringWidth(line) < s.width {
			line = line + ansi.EraseLineRight
		}
		s.g.write(line)
		if i < len(newLines)-1 {
			s.g.write("\r\n")
		}
	}
	if len(s.prevLines) > len(newLines) {
		s.g.write(ansi.EraseScreenBelow)
	}
	s.g.write("\r")
	s.linesRendered = len(newLines)
	s.prevLines = newLines
	s.firstRender = false
}

// resize models a WindowSizeMsg: bubbletea repaints, dropping the line cache so
// every line is re-emitted at the new width.
func (s *flushSim) resize(w int) {
	s.width = w
	s.prevLines = nil
	s.firstRender = true
}

// TestFocusedViewKeepsConstantFrameHeight is the bash-expand bleed: toggling a
// node's inline expanded view (alt+e on a json node) must not change the frame's
// line count. The plain outline pads its frame to a constant height (rowBudget +
// status bar) so the inline renderer never strands stale lines, but the focused
// expanded view took a separate, unpadded path — its short body made the frame
// oscillate between the tall outline and the short view on every alt+e/esc. On a
// terminal whose frame sits near the bottom, the grow half of that cycle scrolls
// rows the renderer can no longer reach, stranding a ghost line and pushing the
// outline up one row per toggle. Every frame in the cycle must be the same height.
func TestFocusedViewKeepsConstantFrameHeight(t *testing.T) {
	root := &item{}
	tr := &tree{root: root, byUUID: map[string]*item{}, externalNames: map[string]string{}}
	parent := &item{name: "parent", parent: root, uuid: "p"}
	jn := &item{name: `{"a":1}`, parent: parent, uuid: "b", typ: database.TypeJSON}
	parent.children = append(parent.children, jn)
	root.children = append(root.children, parent)
	tr.byUUID["p"] = parent
	tr.byUUID["b"] = jn

	// zoom into parent so the JSON node is the sole visible row
	m := &Model{tree: tr, viewStack: []*item{root, parent}, width: 60, height: 24}
	m.refreshRows()
	m.cursor = 0

	height := func() int { return len(strings.Split(m.View(), "\n")) }
	want := height()

	for i := 0; i < 3; i++ {
		m.press("alt+e") // expand into the focused bash view
		if !m.focused {
			t.Fatalf("alt+e did not focus the json view on iteration %d", i)
		}
		if got := height(); got != want {
			t.Fatalf("focused frame is %d lines, want %d (matching the outline); the "+
				"oscillation strands a ghost row each toggle", got, want)
		}
		m.press("esc") // collapse back to the outline
		if m.focused {
			t.Fatalf("esc did not defocus the json view on iteration %d", i)
		}
		if got := height(); got != want {
			t.Fatalf("outline frame is %d lines after close, want %d", got, want)
		}
	}
}

// TestRenderedRowsHaveNoStaleOverlayAfterResize replays the editor's frames
// through a faithful model of bubbletea's inline renderer across a 60->40->60
// resize and asserts no terminal row ends up holding two node renders overlaid.
// This is the F6 break: the inline renderer rewrites rows in place, so a row
// that is shorter at 60 cols than it was at 40 cols keeps the 40-col tail unless
// the frame clears the row before painting it.
func TestRenderedRowsHaveNoStaleOverlayAfterResize(t *testing.T) {
	long := "alpha with a deliberately long name that wraps differently at sixty columns than at forty columns"
	m := newTestModel(60, long)

	sim := &flushSim{g: newVTGrid(24), firstRender: true, width: 60}
	apply := func(w int) {
		mm, _ := m.Update(tea.WindowSizeMsg{Width: w, Height: 24})
		m = mm.(*Model)
		sim.resize(w)
		sim.flush(m.View())
	}
	apply(60)
	apply(40)
	apply(60)

	// The node bullet glyph starts every node render. If a single terminal row
	// contains the bullet more than once, two renders are overlaid on it.
	for r, row := range sim.g.rows {
		if strings.Count(string(row), "○")+strings.Count(string(row), "●") > 1 {
			t.Fatalf("row %d has two node renders overlaid after 60->40->60: %q",
				r, strings.TrimRight(string(row), " "))
		}
	}
}

// TestViewLinesLeadWithClearAfterShrink is the F7 break: shrinking the terminal
// alone (60->40, no grow back) must erase the characters the wider frame painted
// to the right of the now-shorter line. The inline renderer rewrites rows in
// place; its own auto-EraseLineRight only fires when a line is narrower than the
// current width, so it cannot be relied on for a row that fills the shrunk width
// exactly. The frame's own leading clear-to-end-of-line is the guarantee that
// every row erases its prior, wider tail before being repainted. We assert it
// directly on the post-shrink frame: every emitted line must lead with the
// clear, independent of whether the renderer's own clear happens to fire.
func TestViewLinesLeadWithClearAfterShrink(t *testing.T) {
	long := "alpha with a deliberately long name that wraps differently at sixty columns than at forty columns"
	m := newTestModel(60, long)

	for _, w := range []int{60, 40} {
		mm, _ := m.Update(tea.WindowSizeMsg{Width: w, Height: 24})
		m = mm.(*Model)
	}

	lines := strings.Split(m.View(), "\n")
	for i, l := range lines {
		if !strings.HasPrefix(l, cClearEOL) {
			t.Fatalf("View line %d does not lead with a clear-to-end-of-line after "+
				"a 60->40 shrink; the previous 60-col line's tail would survive past "+
				"column 40: %q", i, l)
		}
	}
}

// TestWidthChangeNeedsNoClearCommand is the alt-screen close of the old F7
// break: on the pre-migration inline renderer, a width change reflowed the
// physical terminal before bubbletea repainted, so the renderer's cursor-up
// count — measured in old-width lines — landed one row off and stranded the
// wider row's first physical line above the fresh render, which the resize
// handler papered over by returning tea.ClearScreen. The alt-screen renderer
// always repositions with CursorHomePosition rather than cursor-up over the
// previous frame, so that desync cannot happen for width OR height changes —
// the handler needs no special-case command for either.
func TestWidthChangeNeedsNoClearCommand(t *testing.T) {
	m := newTestModel(60, "alpha")

	// establish a width so the next message is a real change, not the first size
	mm, _ := m.Update(tea.WindowSizeMsg{Width: 60, Height: 24})
	m = mm.(*Model)

	if _, cmd := m.Update(tea.WindowSizeMsg{Width: 40, Height: 24}); cmd != nil {
		t.Fatalf("width change returned a command %#v; the alt screen repaints from "+
			"home every frame, no manual clear needed", cmd())
	}

	if _, cmd := m.Update(tea.WindowSizeMsg{Width: 40, Height: 30}); cmd != nil {
		t.Fatalf("height change returned a command %#v; it should not force a clear", cmd())
	}
}

// TestSlashMenuResizeLeavesNoStaleLine is the alt-screen version of the old
// F11 break: with the slash menu open, narrowing the terminal (60->30) must
// not strand a ghost of the wider frame's menu below the redrawn one. Each
// menu line is wider at 60 cols than at 30 (the command's long description
// fills the row). Under the old inline renderer this needed an app-level
// tea.ClearScreen; the alt-screen renderer homes the cursor and drops its line
// cache on every WindowSizeMsg on its own (see flushSim's doc), so the resize
// handler needs no special-case command at all. We open the menu, replay the
// 60-col frame, then the 30-col frame, and assert the settled grid holds each
// menu command's name exactly once with no wider tail surviving.
func TestSlashMenuResizeLeavesNoStaleLine(t *testing.T) {
	m := newTestModel(60, "alpha")
	mm, _ := m.Update(tea.WindowSizeMsg{Width: 60, Height: 24})
	m = mm.(*Model)

	// open the slash menu so the frame carries every command line
	m.press("/")
	if m.mode != modeSlash {
		t.Fatalf("pressing / did not open the slash menu, mode=%v", m.mode)
	}

	sim := &flushSim{g: newVTGrid(24), firstRender: true, width: 60}
	sim.flush(m.View())

	// narrow the terminal: no clear command needed, the alt-screen frame homes
	// on its own
	mm, cmd := m.Update(tea.WindowSizeMsg{Width: 30, Height: 24})
	m = mm.(*Model)
	if cmd != nil {
		t.Fatalf("narrowing with the menu open returned a command %#v; the alt "+
			"screen needs no manual clear", cmd())
	}
	sim.resize(30)
	sim.flush(m.View())

	// every menu command name must appear exactly once in the settled grid: a
	// stranded wider-frame menu line would show a second copy of some name. Match
	// the name with a trailing space so "/mirror" does not also count "/mirror_to".
	for _, c := range slashCommands {
		got := 0
		for _, row := range sim.g.rows {
			got += strings.Count(string(row), c.name+" ")
		}
		if got > 1 {
			t.Fatalf("menu command %q appears %d times after a 60->30 resize; "+
				"the wider frame's menu line was stranded as a ghost", c.name, got)
		}
	}

	// and no row may carry content past the new 30-col width — that tail could
	// only be a leftover from the wider frame. The grid holds one rune per cell,
	// so the trimmed rune count is the occupied width.
	for r, row := range sim.g.rows {
		trimmed := []rune(strings.TrimRight(string(row), " "))
		if w := len(trimmed); w > 30 {
			t.Fatalf("row %d holds %d cells after a 60->30 resize, past the 30-col "+
				"width; a wider menu tail survived: %q", r, w, string(row))
		}
	}
}

// TestResizeStormRedrawsOneCleanFrame is the alt-screen version of the old F22
// break: rapidly cycling the terminal between a wide/tall geometry and a tiny
// one with keypresses interleaved must never leave stacked copies of the
// outline in the buffer.
//
// On the old inline renderer this needed an app-level tea.ClearScreen: a plain
// WindowSizeMsg repainted (dropped the line cache) but still moved the cursor
// up over only the *previous* frame's line count, so when a short 10x4 frame
// grew back to a tall 80x24 one the renderer couldn't reach the rows the
// earlier tall frame painted and the old outline stranded above the fresh one.
// The alt-screen renderer always repositions with CursorHomePosition instead
// of cursor-up, so that desync — and the app-level workaround for it — is
// gone. We assert no resize in the storm needs a command, then replay every
// frame against the virtual terminal and assert the settled grid holds each
// node's bullet exactly once.
func TestResizeStormRedrawsOneCleanFrame(t *testing.T) {
	m := newTestModel(80, "alpha", "beta", "gamma")

	sim := &flushSim{g: newVTGrid(24), firstRender: true, width: 80}

	// applySize feeds a WindowSizeMsg, asserts it needs no command, repaints the
	// renderer at the new width, then flushes the fresh frame.
	applySize := func(w, h int) {
		mm, cmd := m.Update(tea.WindowSizeMsg{Width: w, Height: h})
		m = mm.(*Model)
		if cmd != nil {
			t.Fatalf("resize to %dx%d returned a command %#v; the alt screen needs "+
				"no manual clear", w, h, cmd())
		}
		sim.resize(w)
		sim.flush(m.View())
	}
	pressKey := func(s string) {
		m.press(s)
		sim.flush(m.View())
	}

	applySize(80, 24)
	for i := 0; i < 4; i++ {
		applySize(10, 4)
		pressKey("down")
		applySize(80, 24)
		pressKey("up")
	}

	total := 0
	for r, row := range sim.g.rows {
		bullets := strings.Count(string(row), "○") + strings.Count(string(row), "●")
		if bullets > 1 {
			t.Fatalf("row %d has %d node bullets after a resize storm; the outline "+
				"is stacked instead of redrawn once: %q",
				r, bullets, strings.TrimRight(string(row), " "))
		}
		total += bullets
	}
	// every distinct node must survive exactly once in the settled frame
	if total != 3 {
		t.Fatalf("settled frame shows %d node bullets, want 3 (one per node); a "+
			"resize storm duplicated or dropped rows", total)
	}
}

// TestNavigationResetsScrollPin is the alt-screen-era replacement for the old
// inline-renderer scrollback-clear guarantee (\x1b[3J), which the alt screen
// made both unreachable (the terminal's real scrollback is never touched from
// inside it) and unnecessary (the whole screen repaints every frame). What
// still matters post-migration: zooming clears the pgup/pgdown viewport pin
// left over from the previous view, and no frame ever re-introduces \x1b[3J —
// emitting it from inside the alt screen would wipe the user's real shell
// history, not the app's own scrollback.
func TestNavigationResetsScrollPin(t *testing.T) {
	root := &item{}
	tr := &tree{root: root, byUUID: map[string]*item{}, externalNames: map[string]string{}}
	parent := &item{name: "parent", parent: root, uuid: "p"}
	child := &item{name: "child", parent: parent, uuid: "c"}
	parent.children = append(parent.children, child)
	root.children = append(root.children, parent)
	tr.byUUID["p"] = parent
	tr.byUUID["c"] = child

	m := &Model{tree: tr, viewStack: []*item{root}, width: 60, height: 24}
	m.refreshRows()
	m.scrolling = true // simulate a pending pgup/pgdown pin from the old view

	// zoom into the parent: any key other than pgup/pgdown drops the pin
	m.press("alt+right")
	if m.viewRoot() != parent {
		t.Fatalf("alt+right did not zoom into the parent node")
	}
	if m.scrolling {
		t.Fatal("zoom-in did not reset the viewport scroll pin")
	}
	if frame := m.View(); strings.Contains(frame, "\x1b[3J") {
		t.Fatalf("post-zoom frame wiped scrollback: %q", frame)
	}

	m.scrolling = true
	// zoom back out: same guarantee on the way out
	m.press("alt+left")
	if m.viewRoot() != root {
		t.Fatalf("alt+left did not zoom back out to the root")
	}
	if m.scrolling {
		t.Fatal("zoom-out did not reset the viewport scroll pin")
	}
	if frame := m.View(); strings.Contains(frame, "\x1b[3J") {
		t.Fatalf("post-zoom-out frame wiped scrollback: %q", frame)
	}
}
