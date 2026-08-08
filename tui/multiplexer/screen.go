// Package multiplexer hosts lflow's background terminal sessions: a PTY, the
// VT screen its output paints, and — for agent sessions — a status read off
// that screen. The editor owns a Manager; sessions die with the editor and
// agent conversations are picked back up through their CLIs' native resume.
package multiplexer

import (
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/x/ansi"
	"github.com/mattn/go-runewidth"
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
// written ("" = default). A space with a pen keeps a colored background. cont
// marks the shadow cell under the right half of a wide rune — rendering skips
// it, the terminal's own wide glyph covers it. comb carries the zero-width
// tail of a grapheme cluster (combining accents, ZWJ emoji joins); link is
// the OSC 8 hyperlink the cell was written under.
type cell struct {
	r    rune
	sgr  string
	comb string
	link string
	cont bool
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

// ReplyFG and ReplyBG are what OSC 10/11 color queries are answered with, in
// X11 rgb spec form. A TUI asks the terminal for its colors on startup and
// themes itself off the answer — the host captures its real terminal colors
// before bubbletea takes the tty and files them here, so a program inside a
// session paints the SAME background the outer terminal has instead of
// guessing at one.
var (
	ReplyFG = "rgb:d4d4/d4d4/d4d4"
	ReplyBG = "rgb:0000/0000/0000"
)

// Screen is the VT state machine plus its grid.
type Screen struct {
	w, h       int
	grid       [][]cell
	x, y       int    // cursor, 0-based
	pen        string // current SGR, "" = default
	penBG      string // the pen's BACKGROUND alone — what erases fill with (BCE)
	link       string // the open OSC 8 hyperlink target, "" = none
	lastX      int    // where the last graphic rune landed — combining marks join it
	lastY      int
	top, bot   int // scroll region rows, inclusive
	wrapNext   bool   // deferred wrap: a rune landed in the last column
	scrollback [][]cell
	sbCap      int // scrollback cap; rows dropped off the head are counted
	dropped    int

	saveX, saveY int
	penSt        penState // the pen as categories — pen/penBG are its render
	savePenSt    penState

	altGrid    [][]cell // the primary grid, parked while the alt screen is up
	altX, altY int
	altOn      bool

	title          string // OSC 0/2 — what the program calls itself right now
	cursorHidden   bool   // DECTCEM ?25l
	bracketedPaste bool   // DECSET ?2004h
	appCursor      bool   // DECCKM ?1h

	// the title filter's state — see filterTitles
	tState int
	tBuf   []byte

	// Reply writes an answer back to the program (the session points it at the
	// PTY master). A real terminal ANSWERS: TUIs query device attributes, the
	// cursor position, mode states and the terminal colors at startup and start
	// degraded — or hang out a timeout — when nothing comes back.
	Reply func([]byte)

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
	s := &Screen{w: w, h: h, top: 0, bot: h - 1, sbCap: sbCap, lastX: -1, lastY: -1}
	s.grid = blankGrid(w, h)
	s.p = ansi.NewParser()
	s.p.SetHandler(ansi.Handler{
		Print:     s.print,
		Execute:   s.execute,
		HandleCsi: s.csi,
		HandleEsc: s.esc,
		HandleOsc: s.osc,
	})
	return s
}

// osc handles the one OSC that must land IN PARSE ORDER with the text around
// it: 8, hyperlinks — the open target stamps every cell printed under it, so
// linked text keeps its URL through the screen. Titles ride their own byte
// filter instead (see filterTitles: the parser truncates multibyte titles at a
// raw 0x9c); URIs are ASCII, so the parser hook is safe for links.
func (s *Screen) osc(cmd int, data []byte) {
	if cmd != 8 {
		return
	}
	t := string(data) // "8;params;URI"
	if rest, ok := strings.CutPrefix(t, "8;"); ok {
		if _, uri, ok := strings.Cut(rest, ";"); ok {
			s.link = uri
			return
		}
	}
	s.link = ""
}

// reply sends an answer to the program when the session wired one up. Only
// the CURSOR POSITION report goes through here — it needs the grid's live
// state; every static query is answered instantly by the reader goroutine's
// queryAnswerer, and answering both places would double-reply.
func (s *Screen) reply(format string, args ...any) {
	if s.Reply != nil {
		s.Reply(fmt.Appendf(nil, format, args...))
	}
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

// blank is what ERASES fill with: back-color erase, as real terminals do it —
// a program that paints `\e[44m\e[2J` gets a blue screen, not default-colored
// holes. Only the pen's background travels into erased cells, never its
// foreground attributes.
func (s *Screen) blank() cell {
	if s.penBG == "" {
		return blankCell
	}
	return cell{r: ' ', sgr: s.penBG}
}

// fillRow is blankRow under the current background — scroll and erase fills.
func (s *Screen) fillRow(w int) []cell {
	r := make([]cell, w)
	b := s.blank()
	for i := range r {
		r[i] = b
	}
	return r
}

// Write feeds output bytes to the screen. Safe to call with arbitrary chunk
// boundaries — both the title filter and the parser keep their state between
// calls, so a sequence split across two reads still lands as one sequence.
func (s *Screen) Write(b []byte) (int, error) {
	s.p.Parse(s.filterTitles(b))
	return len(b), nil
}

// filterTitles harvests OSC 0/2 titles with its own byte scanner AND strips
// them from the stream, so the parser never sees them. Both halves matter: the
// parser reads the raw byte 0x9c as an 8-bit C1 String Terminator even
// mid-UTF-8-rune, and Claude Code's idle glyph ✳ (U+2733, e2 9c b3) contains
// exactly that byte — fed such a title, the parser not only truncates it, it
// ends the OSC early and PRINTS the title's tail into the grid as text. This
// scanner terminates only on BEL or ESC \, is incremental across chunk
// boundaries, and passes every other sequence (OSC 8 links included) through
// to the parser byte for byte.

// filterTitles states, held in Screen.tState across Write chunks.
const (
	tGround      = iota
	tEsc         // held ESC — ']' opens an OSC, anything else replays
	tOscHead     // deciding whether the OSC is a title ("0;" / "2;")
	tOscTitle    // swallowing a title's text
	tOscTitleEsc // ESC inside a title: '\' terminates, anything else aborts
	tOscPass     // some other OSC, passing through until its terminator
	tOscPassEsc  // ESC inside a passed-through OSC
)

func (s *Screen) filterTitles(b []byte) []byte {
	// the "0;"/"2;" prefix was consumed on the way into tOscTitle, so tBuf is
	// the bare title text. Color queries are NOT answered here: the reader
	// goroutine already did, and a second reply would land in the program's
	// input as keys.
	finish := func() {
		s.title = strings.Map(func(r rune) rune {
			if r < ' ' {
				return -1
			}
			return r
		}, string(s.tBuf))
	}
	out := make([]byte, 0, len(b))
	for i := 0; i < len(b); i++ {
		c := b[i]
		switch s.tState {
		case tGround:
			if c == 0x1b {
				s.tState = tEsc
				continue // held until we know it is not a title OSC
			}
			out = append(out, c)
		case tEsc:
			switch c {
			case ']':
				s.tState, s.tBuf = tOscHead, s.tBuf[:0]
			case 0x1b:
				out = append(out, 0x1b) // release the held ESC, hold this one
			default:
				out = append(out, 0x1b, c)
				s.tState = tGround
			}
		case tOscHead:
			// deciding whether this OSC is a title: "0;" or "2;" says yes,
			// anything else replays the held bytes and passes the rest through
			switch {
			case c == 0x07 || c == 0x1b:
				out = append(out, 0x1b, ']')
				out = append(out, s.tBuf...)
				out = append(out, c)
				s.tState = tGround
				if c == 0x1b {
					s.tState = tEsc
					out = out[:len(out)-1]
				}
			case len(s.tBuf) == 0 && (c == '0' || c == '2'):
				s.tBuf = append(s.tBuf, c)
			case len(s.tBuf) == 1 && c == ';':
				s.tBuf = s.tBuf[:0]
				s.tState = tOscTitle
			default:
				out = append(out, 0x1b, ']')
				out = append(out, s.tBuf...)
				out = append(out, c)
				s.tState = tOscPass
			}
		case tOscTitle: // swallowing a title: nothing reaches the parser
			switch {
			case c == 0x07:
				finish()
				s.tState = tGround
			case c == 0x1b:
				s.tState = tOscTitleEsc
			case len(s.tBuf) < 4096:
				s.tBuf = append(s.tBuf, c)
			}
		case tOscTitleEsc:
			if c == '\\' {
				finish()
			}
			s.tState = tGround
			if c == 0x1b {
				s.tState = tEsc
			}
		case tOscPass: // some other OSC: the parser's business, passed through
			out = append(out, c)
			switch c {
			case 0x07:
				s.tState = tGround
			case 0x1b:
				s.tState = tOscPassEsc
			}
		case tOscPassEsc:
			out = append(out, c)
			s.tState = tOscPass
			if c == '\\' {
				s.tState = tGround
			}
		}
	}
	return out
}

// ── writing ─────────────────────────────────────────────────────────────────

func (s *Screen) print(r rune) {
	// wide runes occupy two columns — an agent TUI full of emoji and CJK
	// shears its whole layout if they are laid down as one
	w := runewidth.RuneWidth(r)
	last := func() *cell {
		if s.lastY >= 0 && s.lastY < s.h && s.lastX >= 0 && s.lastX < s.w {
			return &s.grid[s.lastY][s.lastX]
		}
		return nil
	}
	if w <= 0 {
		// zero-width input JOINS the last graphic cell instead of vanishing:
		// combining accents (café in NFD), ZWJ, variation selectors — dropping
		// them strips accents and splits emoji families apart
		if c := last(); c != nil {
			c.comb += string(r)
		}
		return
	}
	// a rune arriving after a ZWJ continues that cell's cluster — the family
	// emoji is ONE glyph in a terminal, not three glyphs and four columns
	if c := last(); c != nil && strings.HasSuffix(c.comb, "\u200d") {
		c.comb += string(r)
		return
	}
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
	if w == 2 && s.x == s.w-1 {
		// a wide rune does not fit the last column: wrap early, as xterm does
		s.x = 0
		s.index()
	}
	s.grid[s.y][s.x] = cell{r: r, sgr: s.pen, link: s.link}
	s.lastX, s.lastY = s.x, s.y
	if w == 2 && s.x+1 < s.w {
		s.grid[s.y][s.x+1] = cell{r: 0, sgr: s.pen, link: s.link, cont: true}
		s.x++
	}
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
		s.grid[s.bot] = s.fillRow(s.w)
		if full && !s.altOn {
			s.pushScrollback(row)
		}
	}
}

func (s *Screen) scrollDown(n int) {
	for i := 0; i < n; i++ {
		copy(s.grid[s.top+1:s.bot+1], s.grid[s.top:s.bot])
		s.grid[s.top] = s.fillRow(s.w)
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
	// PRIVATE-PREFIXED sequences are protocol, not screen edits. Dispatching on
	// the final byte alone executed kitty-keyboard pushes (CSI > 1 u) as
	// restore-cursor, XTMODKEYS (CSI > 4;2 m) as SGR underline+faint, and
	// XTSAVE/XTRESTORE (CSI ? Pm s/r) as save-cursor and scroll-region — a
	// startup scramble in every modern TUI. Only the DEC modes ('?' h/l) act;
	// every other prefixed sequence is consumed whole.
	if pfx := cmd.Prefix(); pfx != 0 {
		if pfx == '?' {
			switch cmd.Final() {
			case 'h', 'l':
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
			case 'n':
				if p(0, 0) == 6 { // DECXCPR
					s.reply("\x1b[?%d;%dR", s.y+1, s.x+1)
				}
			}
		}
		return
	}
	// intermediate-byte forms (DECSCUSR's SP q, DECSTR's ! p, DECRQM's $ p)
	// are likewise consumed rather than misread through their final byte
	if cmd.Intermediate() != 0 {
		return
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
			s.grid[s.y] = s.fillRow(s.w)
		}
	case 'M': // DL — delete lines at the cursor
		n := min(p(0, 1), s.bot-s.y+1)
		for i := 0; i < n; i++ {
			copy(s.grid[s.y:s.bot], s.grid[s.y+1:s.bot+1])
			s.grid[s.bot] = s.fillRow(s.w)
		}
	case 'P': // DCH — delete characters
		n := clamp(p(0, 1), 0, s.w-s.x)
		row := s.grid[s.y]
		copy(row[s.x:], row[s.x+n:])
		for i := s.w - n; i < s.w; i++ {
			row[i] = s.blank()
		}
	case '@': // ICH — insert blanks
		n := clamp(p(0, 1), 0, s.w-s.x)
		row := s.grid[s.y]
		copy(row[s.x+n:], row[s.x:s.w-n])
		for i := s.x; i < s.x+n; i++ {
			row[i] = s.blank()
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
	case 'n': // DSR 6 — cursor position report (the one grid-stateful query)
		if p(0, 0) == 6 {
			s.reply("\x1b[%d;%dR", s.y+1, s.x+1)
		}
	}
}

// penState is the pen as CATEGORIES, not a transcript: each attribute slot
// holds the raw escape that last set it, and a new SGR of the same category
// REPLACES its predecessor. The transcript model (append every sequence, only
// a reset truncates) grew without bound under programs that never reset — an
// agent TUI accumulated one more fg/bg pair per row, and a color-cycling
// program built kilobyte pens for one word.
type penState struct {
	bold, faint, italic, blink, inverse, conceal, strike string
	under, fg, bg, ul                                    string
	other                                                map[int]string // unmodeled SGRs (fonts, overline…), keyed by their lead param
}

func (p *penState) clear() { *p = penState{} }

// compose renders the pen for cell storage, plus the background alone (BCE).
func (p *penState) compose() (pen, bg string) {
	var b strings.Builder
	for _, s := range []string{p.bold, p.faint, p.italic, p.under, p.blink,
		p.inverse, p.conceal, p.strike, p.fg, p.bg, p.ul} {
		b.WriteString(s)
	}
	if len(p.other) > 0 {
		keys := make([]int, 0, len(p.other))
		for k := range p.other {
			keys = append(keys, k)
		}
		sort.Ints(keys)
		for _, k := range keys {
			b.WriteString(p.other[k])
		}
	}
	return b.String(), p.bg
}

// setPen updates the current SGR pen. The walk is structure-aware: only a
// BARE TOP-LEVEL zero is a reset (38;2;0;0;0 carries three zeros that are
// channel values — the old "any zero resets" reading shredded dark themes),
// extended colors consume their arguments as a unit, and colon chains keep
// their colons so 4:3 undercurl and 38:2::r:g:b survive as sent.
func (s *Screen) setPen(params ansi.Params) {
	if len(params) == 0 {
		// ESC[m — the empty form of the reset, what git and less actually emit.
		// It clears the BCE background too; leaving that armed floods every
		// later erase with a stale color.
		s.penSt.clear()
		s.pen, s.penBG = "", ""
		return
	}
	type tok struct {
		v    int
		more bool // the colon chain continues after this token
	}
	toks := make([]tok, 0, len(params))
	params.ForEach(0, func(_, v int, more bool) { toks = append(toks, tok{v, more}) })

	i := 0
	next := func() []tok { // one group: a lone param, or a whole colon chain
		start := i
		for i < len(toks) && toks[i].more {
			i++
		}
		if i < len(toks) {
			i++
		}
		return toks[start:i]
	}
	nums := func(g []tok) []string {
		out := make([]string, len(g))
		for j, t := range g {
			out[j] = fmt.Sprint(t.v)
		}
		return out
	}
	p := &s.penSt
	apply := func(head int, seq string) {
		switch {
		case head == 0:
			p.clear()
		case head == 1:
			p.bold = seq
		case head == 2:
			p.faint = seq
		case head == 22:
			p.bold, p.faint = "", ""
		case head == 3:
			p.italic = seq
		case head == 23:
			p.italic = ""
		case head == 4 || head == 21:
			p.under = seq
		case head == 24:
			p.under = ""
		case head == 5 || head == 6:
			p.blink = seq
		case head == 25:
			p.blink = ""
		case head == 7:
			p.inverse = seq
		case head == 27:
			p.inverse = ""
		case head == 8:
			p.conceal = seq
		case head == 28:
			p.conceal = ""
		case head == 9:
			p.strike = seq
		case head == 29:
			p.strike = ""
		case (head >= 30 && head <= 37) || (head >= 90 && head <= 97) || head == 38:
			p.fg = seq
		case head == 39:
			p.fg = ""
		case (head >= 40 && head <= 47) || (head >= 100 && head <= 107) || head == 48:
			p.bg = seq
		case head == 49:
			p.bg = ""
		case head == 58:
			p.ul = seq
		case head == 59:
			p.ul = ""
		default:
			if p.other == nil {
				p.other = map[int]string{}
			}
			p.other[head] = seq
		}
	}
	for i < len(toks) {
		g := next()
		switch {
		case len(g) == 1 && (g[0].v == 38 || g[0].v == 48 || g[0].v == 58):
			// semicolon-form extended color: the mode and its channels are
			// separate params that belong to this one attribute
			parts := nums(g)
			if i < len(toks) {
				mode := next()
				parts = append(parts, nums(mode)...)
				n := 0
				if len(mode) == 1 && mode[0].v == 2 {
					n = 3 // r, g, b
				} else if len(mode) == 1 && mode[0].v == 5 {
					n = 1 // palette index
				}
				for k := 0; k < n && i < len(toks); k++ {
					parts = append(parts, nums(next())...)
				}
			}
			apply(g[0].v, "\x1b["+strings.Join(parts, ";")+"m")
		case len(g) > 1:
			apply(g[0].v, "\x1b["+strings.Join(nums(g), ":")+"m")
		default:
			apply(g[0].v, "\x1b["+strings.Join(nums(g), ";")+"m")
		}
	}
	s.pen, s.penBG = p.compose()
}

func (s *Screen) saveCursor() {
	s.saveX, s.saveY = s.x, s.y
	s.savePenSt = s.penSt
	if s.penSt.other != nil {
		s.savePenSt.other = make(map[int]string, len(s.penSt.other))
		for k, v := range s.penSt.other {
			s.savePenSt.other[k] = v
		}
	}
}

func (s *Screen) restoreCursor() {
	s.x, s.y = s.saveX, s.saveY
	s.penSt = s.savePenSt
	s.pen, s.penBG = s.penSt.compose()
}

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
	s.x, s.y, s.pen, s.penBG, s.link = 0, 0, "", "", ""
	s.penSt.clear()
	s.lastX, s.lastY = -1, -1
	s.top, s.bot = 0, s.h-1
	s.altOn, s.altGrid = false, nil
	s.wrapNext = false
	s.cursorHidden, s.bracketedPaste, s.appCursor = false, false, false
}

func (s *Screen) eraseRows(from, to int) {
	for y := max(0, from); y <= min(to, s.h-1); y++ {
		s.grid[y] = s.fillRow(s.w)
	}
}

func (s *Screen) eraseLineFrom(y, from, to int) {
	if y < 0 || y >= s.h {
		return
	}
	b := s.blank()
	for x := max(0, from); x < min(to, s.w); x++ {
		s.grid[y][x] = b
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

// GridLines is the live grid as styled rows, all h of them — the attach
// view's pane. With cursor set, the cell under a visible cursor is rendered
// in reverse video, the block cursor a terminal would show.
func (s *Screen) GridLines(cursor bool) []string {
	out := make([]string, s.h)
	for y, row := range s.grid {
		cx := -1
		if cursor && !s.cursorHidden && y == s.y {
			cx = s.x
			if cx < len(row) && row[cx].cont {
				cx-- // the cursor sits on a wide rune's right half: mark the rune
			}
		}
		out[y] = renderRowCursor(row, cx)
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
			if c.cont {
				continue
			}
			r := c.r
			if r == 0 {
				r = ' '
			}
			b.WriteRune(r)
			b.WriteString(c.comb)
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
func renderRow(row []cell) string { return renderRowCursor(row, -1) }

// renderRowCursor is renderRow with a block cursor: the cell at cx (-1 for
// none) renders in reverse video and the row extends to reach it even when the
// content ends earlier. Hyperlinked runs are re-wrapped in their OSC 8, and a
// cell's combining tail (accents, ZWJ clusters) rides out with its rune.
func renderRowCursor(row []cell, cx int) string {
	end := len(row)
	for end > 0 && (row[end-1].r == ' ' || row[end-1].r == 0) && row[end-1].sgr == "" {
		end--
	}
	if cx >= 0 && cx+1 > end && cx < len(row) {
		end = cx + 1
	}
	if end == 0 {
		return ""
	}
	var b strings.Builder
	pen := "\x00" // not any real pen, so the first cell always emits
	link := ""
	setLink := func(target string) {
		if target == link {
			return
		}
		if link != "" {
			b.WriteString("\x1b]8;;\x1b\\")
		}
		if target != "" {
			b.WriteString("\x1b]8;;" + target + "\x1b\\")
		}
		link = target
	}
	for j, c := range row[:end] {
		if c.cont {
			continue // covered by the wide rune to its left
		}
		setLink(c.link)
		if j == cx {
			// the cursor cell resets around itself so the reverse video covers
			// exactly one cell; the next cell re-arms its own pen
			b.WriteString(Reset + "\x1b[7m")
			if c.sgr != "" {
				b.WriteString(c.sgr)
			} else {
				b.WriteString(PlainFG)
			}
			r := c.r
			if r == 0 {
				r = ' '
			}
			b.WriteRune(r)
			b.WriteString(c.comb)
			b.WriteString(Reset)
			pen = "\x00"
			continue
		}
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
		b.WriteString(c.comb)
	}
	setLink("")
	b.WriteString(Reset)
	return b.String()
}
