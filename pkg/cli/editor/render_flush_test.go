/* Copyright 2025 Lflow Authors
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package editor

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// vtGrid is a deliberately small virtual terminal: it replays the exact byte
// stream bubbletea's inline (no-alt-screen) standard renderer would emit for a
// frame and records what each cell ends up holding. It models only the control
// sequences that renderer produces — CursorUp, carriage return, newline,
// EraseLineRight (\x1b[K), EraseScreenBelow (\x1b[J) — plus printable runes, so
// a stale-cell overlay shows up as leftover text on a row.
type vtGrid struct {
	rows       [][]rune
	cur        int // current row
	col        int
	maxLinesUp int // how far the renderer believes it can move the cursor up
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

// flushSim reproduces bubbletea's standardRenderer.flush for the inline path:
// move the cursor up over the previous frame, then for each line truncate to the
// terminal width and emit it, appending EraseLineRight only when the visible
// content is narrower than the terminal, and erasing the screen below when the
// new frame has fewer lines than the last. A resize is modelled by passing a nil
// prev (repaint clears the renderer's line cache).
type flushSim struct {
	g             *vtGrid
	prevLines     []string
	linesRendered int
	width         int
	firstRender   bool
}

func (s *flushSim) flush(frame string) {
	newLines := strings.Split(frame, "\n")
	if s.linesRendered > 1 {
		s.g.write(ansi.CursorUp(s.linesRendered - 1))
	}
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

// clearScreen models bubbletea executing the tea.ClearScreen command the resize
// handler returns: the terminal is wiped (ESC[2J ESC[H) and the renderer forgets
// the previous frame's height, so the next flush starts from a known-empty grid
// with no stale rows to move the cursor up over.
func (s *flushSim) clearScreen() {
	s.g.write(ansi.EraseDisplay(2))
	s.g.write(ansi.CursorOrigin)
	s.linesRendered = 0
	s.prevLines = nil
	s.firstRender = true
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

// TestWidthChangeClearsScreen is the rest of the F7 break: clearing each frame
// line's own tail is not enough. When the width changes the physical terminal
// reflows before bubbletea repaints, so a 60-col row that filled one physical
// line wraps to two at 40 — and the inline renderer's cursor-up count, measured
// in old-width lines, lands one row off and strands the wider row's first
// physical line above the fresh render. Per-line clear-to-EOL can never reach a
// row the renderer doesn't revisit. The resize handler must therefore return
// tea.ClearScreen so the whole terminal is wiped and the next frame repaints
// from a known-empty screen. We assert the command the resize produces is
// exactly bubbletea's ClearScreen, and that a pure height change does not force
// the (visually jarring) full clear.
func TestWidthChangeClearsScreen(t *testing.T) {
	m := newTestModel(60, "alpha")

	// establish a width so the next message is a real change, not the first size
	mm, _ := m.Update(tea.WindowSizeMsg{Width: 60, Height: 24})
	m = mm.(*Model)

	// a width change must clear the screen
	mm, cmd := m.Update(tea.WindowSizeMsg{Width: 40, Height: 24})
	m = mm.(*Model)
	if cmd == nil {
		t.Fatal("width change returned no command; the strand-above-the-frame leftover survives without a full screen clear")
	}
	if got, want := cmd(), tea.ClearScreen(); got != want {
		t.Fatalf("width change command = %#v, want tea.ClearScreen %#v", got, want)
	}

	// a pure height change must not clear the screen (no reflow, no leftover)
	if _, cmd := m.Update(tea.WindowSizeMsg{Width: 40, Height: 30}); cmd != nil {
		t.Fatalf("height-only change returned a command %#v; it should not force a clear", cmd())
	}
}

// TestSlashMenuResizeLeavesNoStaleLine is the F11 break: with the slash menu
// open, narrowing the terminal (60->30) must not strand a ghost of the wider
// frame's menu below the redrawn one. Each menu line is wider at 60 cols than at
// 30 (the command's long description fills the row), so the inline renderer's
// in-place rewrite would leave the 60-col tail past column 30 — and a row that
// reflowed differently would survive entirely. The guarantee is the same one the
// resize handler returns for every width change: tea.ClearScreen wipes the whole
// display before the narrower frame repaints. We open the menu, replay the
// 60-col frame, then the clear + the 30-col frame, and assert the settled grid
// holds each menu command's name exactly once with no wider tail surviving.
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

	// narrow the terminal: a width change must clear the whole screen
	mm, cmd := m.Update(tea.WindowSizeMsg{Width: 30, Height: 24})
	m = mm.(*Model)
	if cmd == nil || cmd() != tea.ClearScreen() {
		t.Fatalf("narrowing with the menu open did not return tea.ClearScreen; "+
			"the wider menu lines would be stranded below the redrawn menu")
	}
	sim.clearScreen()
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

// TestResizeStormRedrawsOneCleanFrame is the F22 break: rapidly cycling the
// terminal between a wide/tall geometry and a tiny one with keypresses
// interleaved must never leave stacked copies of the outline in the buffer.
//
// The mechanism that prevents the stacking is the full clear bubbletea's inline
// renderer can only manage with tea.ClearScreen: on a plain WindowSizeMsg it
// repaints (drops the line cache) but still moves the cursor up over only the
// *previous* frame's line count and erases line-by-line from there, so when a
// short 10x4 frame grows back to a tall 80x24 one the renderer cannot reach the
// rows the earlier tall frame painted and the old outline is stranded above the
// fresh one. tea.ClearScreen wipes the whole display and homes the cursor, so
// the next frame repaints from an empty grid.
//
// We assert the load-bearing guarantee at the command level — every width change
// in the storm must yield tea.ClearScreen — and then replay every frame and
// every clear against the virtual terminal and assert the settled grid holds
// each node's bullet exactly once. Drop the per-width clear and the grow-back
// frames stack; this test catches that.
func TestResizeStormRedrawsOneCleanFrame(t *testing.T) {
	m := newTestModel(80, "alpha", "beta", "gamma")

	sim := &flushSim{g: newVTGrid(24), firstRender: true, width: 80}
	prevWidth := 80

	// applySize feeds a WindowSizeMsg, requires a width change to clear the whole
	// screen, replays that clear, repaints the renderer at the new width, then
	// flushes the fresh frame.
	applySize := func(w, h int) {
		mm, cmd := m.Update(tea.WindowSizeMsg{Width: w, Height: h})
		m = mm.(*Model)
		if w != prevWidth {
			if cmd == nil || cmd() != tea.ClearScreen() {
				t.Fatalf("resize to %dx%d did not return tea.ClearScreen; a storm of "+
					"width changes will stack frames without a full clear", w, h)
			}
			sim.clearScreen()
		}
		prevWidth = w
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
