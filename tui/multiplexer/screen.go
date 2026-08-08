// Package multiplexer hosts lflow's background terminal sessions: a PTY, the
// VT screen its output paints, and — for agent sessions — a status read off
// that screen. The editor owns a Manager; sessions die with the editor and
// agent conversations are picked back up through their CLIs' native resume.
package multiplexer

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// Screen is the screen a session paints — a real (small) VT: a grid of styled
// cells, a cursor, a scroll region and a scrollback, fed the process's raw
// output bytes. It exists because a band that just appends LINES cannot show
// what a terminal shows: `clear` would print an escape, a \r progress bar would
// pile up instead of overwriting itself, and anything that positions its cursor
// (a spinner, a status line, top) would smear.
//
// Scope: everything a program does to its own output — printing, wrapping,
// CR/LF/BS/HT, cursor moves, erase display/line, insert/delete lines and
// characters, scroll regions, SGR, save/restore cursor, the alternate screen —
// plus the read-side state an interactive host needs: the OSC 0/2 title, the
// cursor's visibility (DECTCEM), bracketed paste (DECSET 2004) and application
// cursor keys (DECCKM). DCS and the exotic OSCs are consumed and dropped.
//
// Screen is NOT goroutine-safe: the editor feeds it from bubbletea's single
// event-loop goroutine and reads it from the render path, the same discipline
// the run bands use.

// cell is one screen cell: its rune plus the SGR pen active when it was
// written ("" = default). A space with a pen keeps a colored background.
type cell struct {
	r   rune
	sgr string
}

var blankCell = cell{r: ' '}

// Reset and PlainFG are the SGR bookends renderRow emits: Reset between pen
// changes, PlainFG for unstyled cells — a program's plain output should wear
// the host's normal text color, not the terminal default. The editor points
// PlainFG at its own foreground on startup.
var (
	Reset   = "\x1b[0m"
	PlainFG = "\x1b[0m"
)

// Screen is the VT state machine plus its grid.
type Screen struct {
	w, h       int
	grid       [][]cell
	x, y       int    // cursor, 0-based
	pen        string // current SGR, "" = default
	top, bot   int    // scroll region rows, inclusive
	wrapNext   bool   // deferred wrap: a rune landed in the last column
	scrollback [][]cell
	sbCap      int // scrollback cap; rows dropped off the head are counted
	dropped    int

	saveX, saveY int
	savePen      string

	altGrid    [][]cell // the primary grid, parked while the alt screen is up
	altX, altY int
	altOn      bool

	title          string // OSC 0/2 — what the program calls itself right now
	cursorHidden   bool   // DECTCEM ?25l
	bracketedPaste bool   // DECSET ?2004h
	appCursor      bool   // DECCKM ?1h

	// the title scanner's state — see scanTitle
	tState int
	tBuf   []byte

	p *ansi.Parser
}

// NewScreen builds a blank screen of the given size and wires the parser.
// sbCap bounds the scrollback (rows); rows dropped past it are counted.
func NewScreen(w, h, sbCap int) *Screen {
	if w < 1 {
		w = 80
	}
	if h < 1 {
		h = 24
	}
	s := &Screen{w: w, h: h, top: 0, bot: h - 1, sbCap: sbCap}
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

func blankGrid(w, h int) [][]cell {
	g := make([][]cell, h)
	for i := range g {
		g[i] = blankRow(w)
	}
	return g
}

func blankRow(w int) []cell {
	r := make([]cell, w)
	for i := range r {
		r[i] = blankCell
	}
	return r
}

// Write feeds output bytes to the screen. Safe to call with arbitrary chunk
// boundaries — the parser keeps its state between calls, so a sequence split
// across two reads still lands as one sequence.
func (s *Screen) Write(b []byte) (int, error) {
	s.scanTitle(b)
	s.p.Parse(b)
	return len(b), nil
}

// scanTitle harvests OSC 0/2 titles with its own byte scanner rather than the
// parser's OSC hook: the parser reads the raw byte 0x9c as an 8-bit C1 String
// Terminator even mid-UTF-8-rune, and Claude Code's idle glyph ✳ (U+2733,
// e2 9c b3) contains exactly that byte — the parser hands back a truncated
// title. This scanner terminates only on BEL or ESC \, is incremental across
// chunk boundaries, and leaves every other sequence to the parser.
func (s *Screen) scanTitle(b []byte) {
	const (
		tGround = iota
		tEsc
		tOsc    // collecting OSC data
		tOscEsc // saw ESC inside the data: '\' ends it, anything else aborts
	)
	finish := func() {
		t := string(s.tBuf)
		if rest, ok := strings.CutPrefix(t, "0;"); ok {
			t = rest
		} else if rest, ok := strings.CutPrefix(t, "2;"); ok {
			t = rest
		} else {
			return // some other OSC — not ours
		}
		s.title = strings.Map(func(r rune) rune {
			if r < ' ' {
				return -1
			}
			return r
		}, t)
	}
	for i := 0; i < len(b); i++ {
		c := b[i]
		switch s.tState {
		case tGround:
			if c == 0x1b {
				s.tState = tEsc
			}
		case tEsc:
			switch c {
			case ']':
				s.tState, s.tBuf = tOsc, s.tBuf[:0]
			case 0x1b:
				// stay: another ESC restarts the sequence
			default:
				s.tState = tGround
			}
		case tOsc:
			switch {
			case c == 0x07:
				finish()
				s.tState = tGround
			case c == 0x1b:
				s.tState = tOscEsc
			case len(s.tBuf) < 4096:
				s.tBuf = append(s.tBuf, c)
			}
		case tOscEsc:
			if c == '\\' {
				finish()
			}
			s.tState = tGround
			if c == 0x1b {
				s.tState = tEsc
			}
		}
	}
}

// ── writing ─────────────────────────────────────────────────────────────────

func (s *Screen) print(r rune) {
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
	s.grid[s.y][s.x] = cell{r: r, sgr: s.pen}
	s.x++
	if s.x >= s.w {
		// xterm's deferred wrap: the cursor stays on the last column until the
		// NEXT rune, so a line that exactly fills the width does not eat a blank
		// row below it.
		s.x = s.w - 1
		s.wrapNext = true
	}
}

func (s *Screen) execute(b byte) {
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

// index moves the cursor down one row, scrolling the region when it is already
// on the last row of it (the LF that makes a terminal scroll).
func (s *Screen) index() {
	if s.y == s.bot {
		s.scrollUp(1)
		return
	}
	if s.y < s.h-1 {
		s.y++
	}
}

// reverseIndex is index's opposite (ESC M): up one row, scrolling down at the top.
func (s *Screen) reverseIndex() {
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
// while a partial region (an app's status area) just discards them, as a
// terminal does.
func (s *Screen) scrollUp(n int) {
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

func (s *Screen) scrollDown(n int) {
	for i := 0; i < n; i++ {
		copy(s.grid[s.top+1:s.bot+1], s.grid[s.top:s.bot])
		s.grid[s.top] = blankRow(s.w)
	}
}

// pushScrollback keeps the newest sbCap rows of history, counting what it
// drops, so a runaway program cannot balloon memory. Rows are trimmed to their
// content first: history is mostly short lines, and keeping thousands of
// full-width rows of blanks is pure waste.
func (s *Screen) pushScrollback(row []cell) {
	end := len(row)
	for end > 0 && row[end-1] == blankCell {
		end--
	}
	s.scrollback = append(s.scrollback, row[:end:end])
	if over := len(s.scrollback) - s.sbCap; s.sbCap > 0 && over > 0 {
		s.scrollback = s.scrollback[over:]
		s.dropped += over
	}
}

// ── sequences ───────────────────────────────────────────────────────────────

func (s *Screen) esc(cmd ansi.Cmd) {
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

func (s *Screen) csi(cmd ansi.Cmd, params ansi.Params) {
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
		s.x = clamp(p(0, 1)-1, 0, s.w-1)
		s.wrapNext = false
	case 'd': // VPA — row
		s.y = clamp(p(0, 1)-1, 0, s.h-1)
	case 'H', 'f': // CUP
		s.y = clamp(p(0, 1)-1, 0, s.h-1)
		s.x = clamp(p(1, 1)-1, 0, s.w-1)
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
		n := clamp(p(0, 1), 0, s.w-s.x)
		row := s.grid[s.y]
		copy(row[s.x:], row[s.x+n:])
		for i := s.w - n; i < s.w; i++ {
			row[i] = blankCell
		}
	case '@': // ICH — insert blanks
		n := clamp(p(0, 1), 0, s.w-s.x)
		row := s.grid[s.y]
		copy(row[s.x+n:], row[s.x:s.w-n])
		for i := s.x; i < s.x+n; i++ {
			row[i] = blankCell
		}
	case 'X': // ECH — erase characters
		s.eraseLineFrom(s.y, s.x, s.x+p(0, 1))
	case 'S': // SU — scroll up
		s.scrollUp(p(0, 1))
	case 'T': // SD — scroll down
		s.scrollDown(p(0, 1))
	case 'r': // DECSTBM — scroll region
		t := clamp(p(0, 1)-1, 0, s.h-1)
		b := clamp(p(1, s.h)-1, 0, s.h-1)
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
				case 1: // DECCKM — application cursor keys
					s.appCursor = set
				case 25: // DECTCEM — cursor visibility
					s.cursorHidden = !set
				case 2004: // bracketed paste
					s.bracketedPaste = set
				}
			})
		}
	}
}

// setPen updates the current SGR. Params are re-emitted verbatim, so truecolor
// and every attribute survive without the screen having to model them; a bare
// reset (or no params) clears the pen entirely.
func (s *Screen) setPen(params ansi.Params) {
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

func (s *Screen) saveCursor()    { s.saveX, s.saveY, s.savePen = s.x, s.y, s.pen }
func (s *Screen) restoreCursor() { s.x, s.y, s.pen = s.saveX, s.saveY, s.savePen }

// setAlt enters or leaves the alternate screen: a full-screen app (top, vim)
// gets a blank grid to paint and the scrolling history stays untouched
// underneath.
func (s *Screen) setAlt(on bool) {
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
func (s *Screen) reset() {
	s.grid = blankGrid(s.w, s.h)
	s.x, s.y, s.pen = 0, 0, ""
	s.top, s.bot = 0, s.h-1
	s.altOn, s.altGrid = false, nil
	s.wrapNext = false
	s.cursorHidden, s.bracketedPaste, s.appCursor = false, false, false
}

func (s *Screen) eraseRows(from, to int) {
	for y := max(0, from); y <= min(to, s.h-1); y++ {
		s.grid[y] = blankRow(s.w)
	}
}

func (s *Screen) eraseLineFrom(y, from, to int) {
	if y < 0 || y >= s.h {
		return
	}
	for x := max(0, from); x < min(to, s.w); x++ {
		s.grid[y][x] = blankCell
	}
}

func clamp(v, lo, hi int) int { return max(lo, min(v, hi)) }

// Resize regrows the grid in place. Content is kept top-aligned and clipped;
// a full-screen program repaints itself on the SIGWINCH the PTY resize sends,
// so preservation only matters for the moment between the two.
func (s *Screen) Resize(w, h int) {
	if w < 1 || h < 1 || (w == s.w && h == s.h) {
		return
	}
	regrow := func(g [][]cell) [][]cell {
		out := blankGrid(w, h)
		for y := 0; y < min(h, len(g)); y++ {
			copy(out[y], g[y][:min(w, len(g[y]))])
		}
		return out
	}
	s.grid = regrow(s.grid)
	if s.altGrid != nil {
		s.altGrid = regrow(s.altGrid)
	}
	s.w, s.h = w, h
	s.top, s.bot = 0, h-1
	s.x, s.y = clamp(s.x, 0, w-1), clamp(s.y, 0, h-1)
	s.altX, s.altY = clamp(s.altX, 0, w-1), clamp(s.altY, 0, h-1)
	s.saveX, s.saveY = clamp(s.saveX, 0, w-1), clamp(s.saveY, 0, h-1)
	s.wrapNext = false
}

// ── reading ─────────────────────────────────────────────────────────────────

// Size is the grid's dimensions.
func (s *Screen) Size() (w, h int) { return s.w, s.h }

// Dropped is how many scrollback rows fell off the head of the cap.
func (s *Screen) Dropped() int { return s.dropped }

// Cursor is the current cursor cell, 0-based within the grid.
func (s *Screen) Cursor() (x, y int) { return s.x, s.y }

// CursorVisible follows DECTCEM — false while a program hides its cursor.
func (s *Screen) CursorVisible() bool { return !s.cursorHidden }

// ScrollbackLen is how many history rows precede the grid in Lines.
func (s *Screen) ScrollbackLen() int { return len(s.scrollback) }

// Title is the program's OSC 0/2 window title, "" when it never set one.
func (s *Screen) Title() string { return s.title }

// BracketedPaste reports whether the program asked for bracketed paste — what
// decides if SendText wraps its payload in paste guards.
func (s *Screen) BracketedPaste() bool { return s.bracketedPaste }

// AppCursor reports DECCKM: application cursor-key encoding (SS3 arrows).
func (s *Screen) AppCursor() bool { return s.appCursor }

// AltScreen reports whether the program is painting the alternate screen.
func (s *Screen) AltScreen() bool { return s.altOn }

// Lines renders the screen as band lines: the scrollback first, then the live
// grid. Trailing blank rows are dropped so an `ls` band is three rows and not a
// 24-row screen — except on the alternate screen, where a full-screen app owns
// every row and the blanks are part of its layout.
func (s *Screen) Lines() []string {
	out := make([]string, 0, len(s.scrollback)+s.h)
	for _, row := range s.scrollback {
		out = append(out, renderRow(row))
	}
	last := s.h - 1
	if !s.altOn {
		for last >= 0 && rowBlank(s.grid[last]) && last > s.y {
			last--
		}
	}
	for y := 0; y <= last; y++ {
		out = append(out, renderRow(s.grid[y]))
	}
	return out
}

// GridPlain is the live grid as unstyled text rows — the detection surface.
// Deliberately the whole grid and never a user-scrolled view of it: status
// must not change because somebody scrolled the transcript.
func (s *Screen) GridPlain() []string {
	out := make([]string, s.h)
	for y, row := range s.grid {
		end := len(row)
		for end > 0 && (row[end-1].r == ' ' || row[end-1].r == 0) {
			end--
		}
		var b strings.Builder
		for _, c := range row[:end] {
			r := c.r
			if r == 0 {
				r = ' '
			}
			b.WriteRune(r)
		}
		out[y] = b.String()
	}
	return out
}

func rowBlank(row []cell) bool {
	for _, c := range row {
		if c.r != ' ' && c.r != 0 || c.sgr != "" {
			return false
		}
	}
	return true
}

// renderRow turns one row of cells into a styled string, re-emitting an SGR
// only where the pen changes and trimming the trailing default-styled blanks.
// Unstyled cells are painted in PlainFG rather than left bare: a terminal
// shows a program's plain output in the normal text color, so the host must
// too.
func renderRow(row []cell) string {
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
				b.WriteString(Reset + PlainFG)
			} else {
				b.WriteString(Reset + c.sgr)
			}
			pen = c.sgr
		}
		r := c.r
		if r == 0 {
			r = ' '
		}
		b.WriteRune(r)
	}
	b.WriteString(Reset)
	return b.String()
}
