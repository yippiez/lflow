package multiplexer

import (
	"strings"
	"testing"
)

// screenText renders the screen the way a band does, then strips styling — the
// plain text a terminal would be showing.
func screenText(s *Screen) []string {
	var out []string
	for _, l := range s.Lines() {
		out = append(out, strings.TrimRight(stripSGR(l), " "))
	}
	return out
}

// stripSGR removes ANSI escapes, test-locally — the editor has the shared one.
func stripSGR(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] != 0x1b {
			b.WriteByte(s[i])
			continue
		}
		i++
		if i < len(s) && s[i] == '[' {
			for i < len(s) && !(s[i] >= '@' && s[i] <= '~' && s[i] != '[') {
				i++
			}
		}
	}
	return b.String()
}

func feed(s *Screen, chunks ...string) {
	for _, c := range chunks {
		s.Write([]byte(c))
	}
}

func newTestScreen(w, h int) *Screen { return NewScreen(w, h, 5000) }

// TestTermPlainOutput: the ordinary case — lines land in order and the band
// shows exactly the rows that have content.
func TestTermPlainOutput(t *testing.T) {
	s := newTestScreen(20, 6)
	feed(s, "one\r\ntwo\r\n", "three\r\n")
	if got, want := screenText(s), []string{"one", "two", "three", ""}; !equalRows(got, want) {
		t.Errorf("screen = %q, want %q", got, want)
	}
}

// TestTermCarriageReturnOverwrites is the progress-bar case: a \r rewinds to
// column zero of the SAME row, so successive updates replace each other
// instead of piling up.
func TestTermCarriageReturnOverwrites(t *testing.T) {
	s := newTestScreen(20, 4)
	feed(s, "  0% [   ]\r", " 50% [## ]\r", "100% [###]\r\n", "done\r\n")
	got := screenText(s)
	if len(got) < 2 || got[0] != "100% [###]" {
		t.Fatalf("row 0 = %q, want the last progress update to have overwritten", got)
	}
	if got[1] != "done" {
		t.Errorf("row 1 = %q, want %q", got[1], "done")
	}
}

// TestTermClearWipesTheScreen: `clear` (ED 2/3 + home) leaves an empty screen
// and drops the history, as it does in a terminal.
func TestTermClearWipesTheScreen(t *testing.T) {
	s := newTestScreen(20, 4)
	feed(s, "old one\r\nold two\r\n")
	feed(s, "\x1b[H\x1b[2J\x1b[3J") // what `clear` emits
	if got := screenText(s); len(got) > 1 || (len(got) == 1 && got[0] != "") {
		t.Errorf("after clear screen = %q, want empty", got)
	}
	feed(s, "fresh\r\n")
	if got := screenText(s); got[0] != "fresh" {
		t.Errorf("after clear + write = %q, want the new output at the top", got)
	}
}

// TestTermEraseLineAndCursorMoves: a status line that rewrites itself in place
// shows only its latest state.
func TestTermEraseLineAndCursorMoves(t *testing.T) {
	s := newTestScreen(24, 5)
	feed(s, "step 1\r\nworking\r\n")
	feed(s, "\x1b[2A\x1b[Kstep 1 done\r\n")
	got := screenText(s)
	if got[0] != "step 1 done" {
		t.Errorf("row 0 = %q, want the rewritten line", got[0])
	}
	if len(got) > 1 && got[1] != "working" {
		t.Errorf("row 1 = %q, want the untouched line %q", got[1], "working")
	}
}

// TestTermScrollbackKeepsHistory: output past the screen height scrolls, and
// what scrolled off is kept so the expanded view can read back through it.
func TestTermScrollbackKeepsHistory(t *testing.T) {
	s := newTestScreen(20, 3)
	for i := 1; i <= 8; i++ {
		feed(s, "line "+itoa(i)+"\r\n")
	}
	got := screenText(s)
	if len(got) < 8 {
		t.Fatalf("screen+scrollback = %d rows, want at least the 8 written: %q", len(got), got)
	}
	if got[0] != "line 1" {
		t.Errorf("first row = %q, want the oldest line from scrollback", got[0])
	}
	if last := got[len(got)-2]; last != "line 8" {
		t.Errorf("last content row = %q, want %q", last, "line 8")
	}
}

// TestTermScrollbackCap: the cap drops head rows and counts them.
func TestTermScrollbackCap(t *testing.T) {
	s := NewScreen(20, 3, 4)
	for i := 1; i <= 12; i++ {
		feed(s, "line "+itoa(i)+"\r\n")
	}
	if s.ScrollbackLen() != 4 {
		t.Errorf("scrollback = %d rows, want the cap 4", s.ScrollbackLen())
	}
	if s.Dropped() == 0 {
		t.Error("dropped rows were not counted")
	}
}

// TestTermColorsSurvive: a program's SGR is carried per cell and re-emitted;
// unstyled cells wear PlainFG rather than being left bare.
func TestTermColorsSurvive(t *testing.T) {
	s := newTestScreen(20, 3)
	feed(s, "\x1b[32mok\x1b[0m plain\r\n")
	row := s.Lines()[0]
	if !strings.Contains(row, "\x1b[32m") {
		t.Errorf("row lost the program's color: %q", row)
	}
	if stripSGR(row) != "ok plain" {
		t.Errorf("row text = %q, want %q", stripSGR(row), "ok plain")
	}
	if !strings.Contains(row, PlainFG) {
		t.Errorf("plain cells should render in PlainFG: %q", row)
	}
}

// TestTermWrapsAtTheEdge: long lines wrap; an exact-fit line does not eat a
// blank row (deferred wrap).
func TestTermWrapsAtTheEdge(t *testing.T) {
	s := newTestScreen(6, 4)
	feed(s, "abcdefgh\r\n")
	got := screenText(s)
	if got[0] != "abcdef" || got[1] != "gh" {
		t.Errorf("wrapped rows = %q, want abcdef / gh", got)
	}

	s = newTestScreen(6, 4)
	feed(s, "abcdef\r\nnext\r\n")
	got = screenText(s)
	if got[0] != "abcdef" || got[1] != "next" {
		t.Errorf("exact-fit rows = %q, want abcdef / next (no blank row between)", got)
	}
}

// TestTermAltScreen: a full-screen app paints the alternate screen and leaves
// the scrolling history untouched, then restores it on exit.
func TestTermAltScreen(t *testing.T) {
	s := newTestScreen(20, 4)
	feed(s, "before\r\n")
	feed(s, "\x1b[?1049h")
	feed(s, "\x1b[H\x1b[2Jfull app")
	got := screenText(s)
	if got[0] != "full app" {
		t.Errorf("alt screen row 0 = %q, want the app's paint", got[0])
	}
	for _, row := range got {
		if row == "before" {
			t.Error("the alt screen should not show the primary screen's output")
		}
	}
	if !s.AltScreen() {
		t.Error("AltScreen() should report the alt screen")
	}
	feed(s, "\x1b[?1049l")
	if got := screenText(s); got[0] != "before" {
		t.Errorf("after leaving the alt screen row 0 = %q, want %q", got[0], "before")
	}
}

// TestTermSplitSequence: a sequence split across two reads (the PTY does not
// respect sequence boundaries) still lands as one sequence.
func TestTermSplitSequence(t *testing.T) {
	s := newTestScreen(20, 3)
	feed(s, "one\r\ntwo", "\x1b[1;1H\x1b", "[2Kzap")
	got := screenText(s)
	if got[0] != "zap" {
		t.Errorf("row 0 = %q, want the split-sequence rewrite %q", got[0], "zap")
	}
}

// TestTermInsertDeleteLines covers IL/DL, which anything drawing a list in
// place uses to shuffle rows.
func TestTermInsertDeleteLines(t *testing.T) {
	s := newTestScreen(12, 5)
	feed(s, "a\r\nb\r\nc\r\n")
	feed(s, "\x1b[2;1H\x1b[L", "X")
	if got := screenText(s); got[0] != "a" || got[1] != "X" || got[2] != "b" {
		t.Errorf("after IL = %q, want a / X / b", got)
	}
	feed(s, "\x1b[2;1H\x1b[M")
	if got := screenText(s); got[0] != "a" || got[1] != "b" || got[2] != "c" {
		t.Errorf("after DL = %q, want a / b / c", got)
	}
}

// TestTermResize: content survives top-aligned, the cursor stays in range, and
// writing keeps working at the new size.
func TestTermResize(t *testing.T) {
	s := newTestScreen(20, 4)
	feed(s, "keep me\r\n")
	s.Resize(30, 8)
	if w, h := s.Size(); w != 30 || h != 8 {
		t.Fatalf("size = %dx%d, want 30x8", w, h)
	}
	if got := screenText(s); got[0] != "keep me" {
		t.Errorf("row 0 after resize = %q, want the kept content", got[0])
	}
	feed(s, "after resize\r\n")
	if got := screenText(s); got[1] != "after resize" {
		t.Errorf("row 1 = %q, want the post-resize write", got[1])
	}
}

func equalRows(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}
