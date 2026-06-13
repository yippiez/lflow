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
			case 'J': // erase screen below
				g.eraseScreenBelow()
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
