package editor

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// termScreen is the screen a shell command paints — a real (small) VT: a grid of
// styled cells, a cursor, a scroll region and a scrollback, fed the command's raw
// output bytes. It exists because a run band that just appends LINES cannot show
// what a terminal shows: `clear` would print an escape, a \r progress bar would
// pile up instead of overwriting itself, and anything that positions its cursor
// (a spinner, a status line, top) would smear. The expanded view renders this
// grid, so a command's output reads exactly as it would in a terminal.
//
// Scope is deliberate: everything a non-interactive command does to its own
// output — printing, wrapping, CR/LF/BS/HT, cursor moves, erase display/line,
// insert/delete lines and characters, scroll regions, SGR, save/restore cursor,
// the alternate screen — and nothing that needs input (no keyboard, no mouse, no
// DCS/OSC side effects; those sequences are consumed and dropped). The command
// runs on a PTY (see startBash), so programs behave as they do in a terminal
// rather than falling back to their pipe output.
//
// Rows that scroll off the top go to the scrollback, capped like the line band
// was, so the expanded view can still scroll back through a long run.

// termCell is one screen cell: its rune plus the SGR pen active when it was
// written ("" = default). A space with a pen keeps a colored background.
type termCell struct {
	r   rune
	sgr string
}

var termBlank = termCell{r: ' '}

// termScreen is the VT state machine plus its grid.
type termScreen struct {
	w, h       int
	grid       [][]termCell
	x, y       int    // cursor, 0-based
	pen        string // current SGR, "" = default
	top, bot   int    // scroll region rows, inclusive
	wrapNext   bool   // deferred wrap: a rune landed in the last column
	scrollback [][]termCell
	dropped    int // scrollback rows dropped off the head (see maxRunLines)

	saveX, saveY int
	savePen      string

	altGrid    [][]termCell // the primary grid, parked while the alt screen is up
	altX, altY int
	altOn      bool

	p *ansi.Parser
}

// newTermScreen builds a blank screen of the given size and wires the parser.
func newTermScreen(w, h int) *termScreen {
	if w < 1 {
		w = 80
	}
	if h < 1 {
		h = 24
	}
	s := &termScreen{w: w, h: h, top: 0, bot: h - 1}
	s.grid = blankGrid(w, h)
	s.p = ansi.NewParser()
	s.p.SetHandler(ansi.Handler{
		Print:     s.print,
		Execute:   s.execute,
		HandleCsi: s.csi,
		HandleEsc: s.esc,
	})
	return s
}

func blankGrid(w, h int) [][]termCell {
	g := make([][]termCell, h)
	for i := range g {
		g[i] = blankRow(w)
	}
	return g
}

func blankRow(w int) []termCell {
	r := make([]termCell, w)
	for i := range r {
		r[i] = termBlank
	}
	return r
}

// Write feeds output bytes to the screen. Safe to call with arbitrary chunk
// boundaries — the parser keeps its state between calls, so a sequence split
// across two reads still lands as one sequence.
func (s *termScreen) Write(b []byte) (int, error) {
	s.p.Parse(b)
	return len(b), nil
}

// ── writing ─────────────────────────────────────────────────────────────────

func (s *termScreen) print(r rune) {
	if s.wrapNext {
		s.x = 0
		s.index()
		s.wrapNext = false
	}
	if s.y < 0 || s.y >= s.h {
		return
	}
	if s.x >= s.w {
		s.x = s.w - 1
	}
	s.grid[s.y][s.x] = termCell{r: r, sgr: s.pen}
	s.x++
	if s.x >= s.w {
		// xterm's deferred wrap: the cursor stays on the last column until the
		// NEXT rune, so a line that exactly fills the width does not eat a blank
		// row below it.
		s.x = s.w - 1
		s.wrapNext = true
	}
}

func (s *termScreen) execute(b byte) {
	switch b {
	case '\n', '\v', '\f':
		s.index()
		s.wrapNext = false
	case '\r':
		s.x = 0
		s.wrapNext = false
	case '\b':
		if s.x > 0 {
			s.x--
		}
		s.wrapNext = false
	case '\t':
		next := (s.x/8 + 1) * 8
		if next >= s.w {
			next = s.w - 1
		}
		s.x = next
		s.wrapNext = false
	}
	// BEL and every other control byte: nothing to draw
}

// index moves the cursor down one row, scrolling the region when it is already on
// the last row of it (the LF that makes a terminal scroll).
func (s *termScreen) index() {
	if s.y == s.bot {
		s.scrollUp(1)
		return
	}
	if s.y < s.h-1 {
		s.y++
	}
}

// reverseIndex is index's opposite (ESC M): up one row, scrolling down at the top.
func (s *termScreen) reverseIndex() {
	if s.y == s.top {
		s.scrollDown(1)
		return
	}
	if s.y > 0 {
		s.y--
	}
}

// scrollUp moves the scroll region up by n rows. Rows leaving the TOP of a
// full-screen region become scrollback — that is the history a terminal keeps —
// while a partial region (an app's status area) just discards them, as a terminal
// does.
func (s *termScreen) scrollUp(n int) {
	if n <= 0 {
		return
	}
	full := s.top == 0 && s.bot == s.h-1
	for i := 0; i < n; i++ {
		row := s.grid[s.top]
		copy(s.grid[s.top:s.bot], s.grid[s.top+1:s.bot+1])
		s.grid[s.bot] = blankRow(s.w)
		if full && !s.altOn {
			s.pushScrollback(row)
		}
	}
}

func (s *termScreen) scrollDown(n int) {
	for i := 0; i < n; i++ {
		copy(s.grid[s.top+1:s.bot+1], s.grid[s.top:s.bot])
		s.grid[s.top] = blankRow(s.w)
	}
}

// pushScrollback keeps the newest maxRunLines rows of history, counting what it
// drops — the same guard the line band used, so a runaway command cannot balloon
// memory. Rows are trimmed to their content first: history is mostly short lines,
// and keeping thousands of full-width rows of blanks is pure waste.
func (s *termScreen) pushScrollback(row []termCell) {
	end := len(row)
	for end > 0 && row[end-1] == termBlank {
		end--
	}
	s.scrollback = append(s.scrollback, row[:end:end])
	if over := len(s.scrollback) - maxRunLines; over > 0 {
		s.scrollback = s.scrollback[over:]
		s.dropped += over
	}
}

// ── sequences ───────────────────────────────────────────────────────────────

func (s *termScreen) esc(cmd ansi.Cmd) {
	switch cmd.Final() {
	case 'M': // RI — reverse index
		s.reverseIndex()
	case 'D': // IND — index
		s.index()
	case 'E': // NEL — next line
		s.x = 0
		s.index()
	case '7': // DECSC — save cursor
		s.saveCursor()
	case '8': // DECRC — restore cursor
		s.restoreCursor()
	case 'c': // RIS — full reset
		s.reset()
	}
}

func (s *termScreen) csi(cmd ansi.Cmd, params ansi.Params) {
	p := func(i, def int) int {
		// Param already yields def for a missing index; 0 means "default" in CSI
		v, _, _ := params.Param(i, def)
		if v == 0 && def > 0 {
			return def
		}
		return v
	}
	switch cmd.Final() {
	case 'A': // CUU
		s.y = max(s.top, s.y-p(0, 1))
	case 'B', 'e': // CUD
		s.y = min(s.bot, s.y+p(0, 1))
	case 'C', 'a': // CUF
		s.x = min(s.w-1, s.x+p(0, 1))
		s.wrapNext = false
	case 'D': // CUB
		s.x = max(0, s.x-p(0, 1))
		s.wrapNext = false
	case 'E': // CNL
		s.y = min(s.bot, s.y+p(0, 1))
		s.x = 0
	case 'F': // CPL
		s.y = max(s.top, s.y-p(0, 1))
		s.x = 0
	case 'G', '`': // CHA — column
		s.x = clampInt(p(0, 1)-1, 0, s.w-1)
		s.wrapNext = false
	case 'd': // VPA — row
		s.y = clampInt(p(0, 1)-1, 0, s.h-1)
	case 'H', 'f': // CUP
		s.y = clampInt(p(0, 1)-1, 0, s.h-1)
		s.x = clampInt(p(1, 1)-1, 0, s.w-1)
		s.wrapNext = false
	case 'J': // ED — erase display
		switch p(0, 0) {
		case 0:
			s.eraseLineFrom(s.y, s.x, s.w)
			s.eraseRows(s.y+1, s.h-1)
		case 1:
			s.eraseRows(0, s.y-1)
			s.eraseLineFrom(s.y, 0, s.x+1)
		case 2:
			s.eraseRows(0, s.h-1)
		case 3:
			// xterm's "also erase scrollback" — `clear` sends this, and it is
			// exactly the history-wipe it asks for
			s.eraseRows(0, s.h-1)
			s.scrollback = nil
			s.dropped = 0
		}
	case 'K': // EL — erase line
		switch p(0, 0) {
		case 0:
			s.eraseLineFrom(s.y, s.x, s.w)
		case 1:
			s.eraseLineFrom(s.y, 0, s.x+1)
		case 2:
			s.eraseLineFrom(s.y, 0, s.w)
		}
	case 'L': // IL — insert lines at the cursor
		n := min(p(0, 1), s.bot-s.y+1)
		for i := 0; i < n; i++ {
			copy(s.grid[s.y+1:s.bot+1], s.grid[s.y:s.bot])
			s.grid[s.y] = blankRow(s.w)
		}
	case 'M': // DL — delete lines at the cursor
		n := min(p(0, 1), s.bot-s.y+1)
		for i := 0; i < n; i++ {
			copy(s.grid[s.y:s.bot], s.grid[s.y+1:s.bot+1])
			s.grid[s.bot] = blankRow(s.w)
		}
	case 'P': // DCH — delete characters
		n := clampInt(p(0, 1), 0, s.w-s.x)
		row := s.grid[s.y]
		copy(row[s.x:], row[s.x+n:])
		for i := s.w - n; i < s.w; i++ {
			row[i] = termBlank
		}
	case '@': // ICH — insert blanks
		n := clampInt(p(0, 1), 0, s.w-s.x)
		row := s.grid[s.y]
		copy(row[s.x+n:], row[s.x:s.w-n])
		for i := s.x; i < s.x+n; i++ {
			row[i] = termBlank
		}
	case 'X': // ECH — erase characters
		s.eraseLineFrom(s.y, s.x, s.x+p(0, 1))
	case 'S': // SU — scroll up
		s.scrollUp(p(0, 1))
	case 'T': // SD — scroll down
		s.scrollDown(p(0, 1))
	case 'r': // DECSTBM — scroll region
		t := clampInt(p(0, 1)-1, 0, s.h-1)
		b := clampInt(p(1, s.h)-1, 0, s.h-1)
		if t < b {
			s.top, s.bot = t, b
		} else {
			s.top, s.bot = 0, s.h-1
		}
		s.x, s.y = 0, s.top
	case 's': // SCOSC
		s.saveCursor()
	case 'u': // SCORC
		s.restoreCursor()
	case 'm': // SGR — the pen
		s.setPen(params)
	case 'h', 'l':
		if cmd.Prefix() == '?' {
			set := cmd.Final() == 'h'
			params.ForEach(0, func(_, v int, _ bool) {
				switch v {
				case 1049, 1047, 47: // the alternate screen
					s.setAlt(set)
				}
			})
		}
	}
}

// setPen updates the current SGR. Params are re-emitted verbatim, so truecolor
// and every attribute survive without the screen having to model them; a bare
// reset (or no params) clears the pen entirely.
func (s *termScreen) setPen(params ansi.Params) {
	if len(params) == 0 {
		s.pen = ""
		return
	}
	var parts []string
	reset := false
	params.ForEach(0, func(_, v int, _ bool) {
		if v == 0 {
			reset = true
			parts = parts[:0]
			return
		}
		parts = append(parts, fmt.Sprint(v))
	})
	if reset && len(parts) == 0 {
		s.pen = ""
		return
	}
	seq := "\x1b[" + strings.Join(parts, ";") + "m"
	if reset {
		s.pen = seq // the reset already zeroed what came before it
		return
	}
	s.pen += seq // attributes accumulate until a reset, as on a terminal
}

func (s *termScreen) saveCursor()    { s.saveX, s.saveY, s.savePen = s.x, s.y, s.pen }
func (s *termScreen) restoreCursor() { s.x, s.y, s.pen = s.saveX, s.saveY, s.savePen }

// setAlt enters or leaves the alternate screen: a full-screen app (top, vim) gets
// a blank grid to paint and the scrolling history stays untouched underneath.
func (s *termScreen) setAlt(on bool) {
	if on == s.altOn {
		return
	}
	if on {
		s.altGrid, s.altX, s.altY = s.grid, s.x, s.y
		s.grid = blankGrid(s.w, s.h)
		s.x, s.y = 0, 0
	} else {
		if s.altGrid != nil {
			s.grid, s.x, s.y = s.altGrid, s.altX, s.altY
		}
		s.altGrid = nil
	}
	s.altOn = on
	s.top, s.bot = 0, s.h-1
}

// reset returns the screen to power-on state, keeping the scrollback (RIS on a
// terminal leaves the history alone).
func (s *termScreen) reset() {
	s.grid = blankGrid(s.w, s.h)
	s.x, s.y, s.pen = 0, 0, ""
	s.top, s.bot = 0, s.h-1
	s.altOn, s.altGrid = false, nil
	s.wrapNext = false
}

func (s *termScreen) eraseRows(from, to int) {
	for y := max(0, from); y <= min(to, s.h-1); y++ {
		s.grid[y] = blankRow(s.w)
	}
}

func (s *termScreen) eraseLineFrom(y, from, to int) {
	if y < 0 || y >= s.h {
		return
	}
	for x := max(0, from); x < min(to, s.w); x++ {
		s.grid[y][x] = termBlank
	}
}

func clampInt(v, lo, hi int) int { return max(lo, min(v, hi)) }

// ── reading ─────────────────────────────────────────────────────────────────

// lines renders the screen as band lines: the scrollback first, then the live
// grid. Trailing blank rows are dropped so an `ls` band is three rows and not a
// 24-row screen — except on the alternate screen, where a full-screen app owns
// every row and the blanks are part of its layout.
func (s *termScreen) lines() []outLine {
	out := make([]outLine, 0, len(s.scrollback)+s.h)
	for _, row := range s.scrollback {
		out = append(out, outLine{text: renderTermRow(row)})
	}
	last := s.h - 1
	if !s.altOn {
		for last >= 0 && rowBlank(s.grid[last]) && last > s.y {
			last--
		}
	}
	for y := 0; y <= last; y++ {
		out = append(out, outLine{text: renderTermRow(s.grid[y])})
	}
	return out
}

func rowBlank(row []termCell) bool {
	for _, c := range row {
		if c.r != ' ' && c.r != 0 || c.sgr != "" {
			return false
		}
	}
	return true
}

// renderTermRow turns one row of cells into a styled string, re-emitting an SGR
// only where the pen changes and trimming the trailing default-styled blanks.
// Unstyled cells are painted in the editor's default foreground rather than left
// bare: a terminal shows a program's plain output in the normal text color, so the
// band must too (an unstyled band line is otherwise muted gray, which reads as
// chrome — see styleOutLine).
func renderTermRow(row []termCell) string {
	end := len(row)
	for end > 0 && (row[end-1].r == ' ' || row[end-1].r == 0) && row[end-1].sgr == "" {
		end--
	}
	if end == 0 {
		return ""
	}
	var b strings.Builder
	pen := "\x00" // not any real pen, so the first cell always emits
	for _, c := range row[:end] {
		if c.sgr != pen {
			if c.sgr == "" {
				b.WriteString(cReset + cFG)
			} else {
				b.WriteString(cReset + c.sgr)
			}
			pen = c.sgr
		}
		r := c.r
		if r == 0 {
			r = ' '
		}
		b.WriteRune(r)
	}
	b.WriteString(cReset)
	return b.String()
}
