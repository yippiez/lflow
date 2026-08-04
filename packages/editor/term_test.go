package editor

import (
	"strings"
	"testing"
)

// screenText renders the screen the way the band does, then strips styling — the
// plain text a terminal would be showing.
func screenText(s *termScreen) []string {
	var out []string
	for _, l := range s.lines() {
		out = append(out, strings.TrimRight(stripSGR(l.text), " "))
	}
	return out
}

func feed(s *termScreen, chunks ...string) {
	for _, c := range chunks {
		s.Write([]byte(c))
	}
}

// TestTermPlainOutput: the ordinary case — lines land in order and the band shows
// exactly the rows that have content.
func TestTermPlainOutput(t *testing.T) {
	s := newTermScreen(20, 6)
	feed(s, "one\r\ntwo\r\n", "three\r\n")
	if got, want := screenText(s), []string{"one", "two", "three", ""}; !equalRows(got, want) {
		t.Errorf("screen = %q, want %q", got, want)
	}
}

// TestTermCarriageReturnOverwrites is the progress-bar case: a \r rewinds to the
// column zero of the SAME row, so successive updates replace each other instead of
// piling up.
func TestTermCarriageReturnOverwrites(t *testing.T) {
	s := newTermScreen(20, 4)
	feed(s, "  0% [   ]\r", " 50% [## ]\r", "100% [###]\r\n", "done\r\n")
	got := screenText(s)
	if len(got) < 2 || got[0] != "100% [###]" {
		t.Fatalf("row 0 = %q, want the last progress update to have overwritten", got)
	}
	if got[1] != "done" {
		t.Errorf("row 1 = %q, want %q", got[1], "done")
	}
	for _, row := range got {
		if strings.Contains(row, "0% [   ] 50%") {
			t.Errorf("progress updates piled up on one row: %q", row)
		}
	}
}

// TestTermClearWipesTheScreen: `clear` (ED 2/3 + home) leaves an empty screen and
// drops the history, as it does in a terminal.
func TestTermClearWipesTheScreen(t *testing.T) {
	s := newTermScreen(20, 4)
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

// TestTermEraseLineAndCursorMoves: a status line that rewrites itself in place —
// cursor up, erase to end of line, print — shows only its latest state.
func TestTermEraseLineAndCursorMoves(t *testing.T) {
	s := newTermScreen(24, 5)
	feed(s, "step 1\r\nworking\r\n")
	// go back up one row, wipe it, write the finished state
	feed(s, "\x1b[2A\x1b[Kstep 1 done\r\n")
	got := screenText(s)
	if got[0] != "step 1 done" {
		t.Errorf("row 0 = %q, want the rewritten line", got[0])
	}
	if len(got) > 1 && got[1] != "working" {
		t.Errorf("row 1 = %q, want the untouched line %q", got[1], "working")
	}
}

// TestTermScrollbackKeepsHistory: output past the screen height scrolls, and what
// scrolled off is kept as scrollback so the expanded view can read back through it.
func TestTermScrollbackKeepsHistory(t *testing.T) {
	s := newTermScreen(20, 3)
	for i := 1; i <= 8; i++ {
		feed(s, strings.Repeat("", 0)+"line "+itoa(i)+"\r\n")
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

// TestTermColorsSurvive: a program's SGR is carried per cell and re-emitted, so
// colored output looks like it did in the terminal.
func TestTermColorsSurvive(t *testing.T) {
	s := newTermScreen(20, 3)
	feed(s, "\x1b[32mok\x1b[0m plain\r\n")
	row := s.lines()[0].text
	if !strings.Contains(row, "\x1b[32m") {
		t.Errorf("row lost the program's color: %q", row)
	}
	if stripSGR(row) != "ok plain" {
		t.Errorf("row text = %q, want %q", stripSGR(row), "ok plain")
	}
	// unstyled cells wear the editor's normal foreground, not muted chrome
	if !strings.Contains(row, cFG) {
		t.Errorf("plain cells should render in the default foreground: %q", row)
	}
}

// TestTermWrapsAtTheEdge: a line longer than the screen wraps to the next row, the
// way a terminal wraps it — and a line that exactly fills the width does not eat a
// blank row (deferred wrap).
func TestTermWrapsAtTheEdge(t *testing.T) {
	s := newTermScreen(6, 4)
	feed(s, "abcdefgh\r\n")
	got := screenText(s)
	if got[0] != "abcdef" || got[1] != "gh" {
		t.Errorf("wrapped rows = %q, want abcdef / gh", got)
	}

	s = newTermScreen(6, 4)
	feed(s, "abcdef\r\nnext\r\n")
	got = screenText(s)
	if got[0] != "abcdef" || got[1] != "next" {
		t.Errorf("exact-fit rows = %q, want abcdef / next (no blank row between)", got)
	}
}

// TestTermAltScreen: a full-screen app paints the alternate screen and leaves the
// scrolling history untouched, then restores it on exit.
func TestTermAltScreen(t *testing.T) {
	s := newTermScreen(20, 4)
	feed(s, "before\r\n")
	feed(s, "\x1b[?1049h")           // enter the alt screen
	feed(s, "\x1b[H\x1b[2Jfull app") // paint it
	got := screenText(s)
	if got[0] != "full app" {
		t.Errorf("alt screen row 0 = %q, want the app's paint", got[0])
	}
	for _, row := range got {
		if row == "before" {
			t.Error("the alt screen should not show the primary screen's output")
		}
	}
	feed(s, "\x1b[?1049l") // leave
	if got := screenText(s); got[0] != "before" {
		t.Errorf("after leaving the alt screen row 0 = %q, want %q", got[0], "before")
	}
}

// TestTermSplitSequence: a sequence split across two reads (the PTY does not
// respect sequence boundaries) still lands as one sequence.
func TestTermSplitSequence(t *testing.T) {
	s := newTermScreen(20, 3)
	feed(s, "one\r\ntwo", "\x1b[1;1H\x1b", "[2Kzap")
	got := screenText(s)
	if got[0] != "zap" {
		t.Errorf("row 0 = %q, want the split-sequence rewrite %q", got[0], "zap")
	}
}

// TestTermInsertDeleteLines covers IL/DL, which anything drawing a list in place
// (a package manager, a test runner) uses to shuffle rows.
func TestTermInsertDeleteLines(t *testing.T) {
	s := newTermScreen(12, 5)
	feed(s, "a\r\nb\r\nc\r\n")
	feed(s, "\x1b[2;1H\x1b[L", "X") // insert a blank row at row 2, write into it
	if got := screenText(s); got[0] != "a" || got[1] != "X" || got[2] != "b" {
		t.Errorf("after IL = %q, want a / X / b", got)
	}
	feed(s, "\x1b[2;1H\x1b[M") // delete row 2 again
	if got := screenText(s); got[0] != "a" || got[1] != "b" || got[2] != "c" {
		t.Errorf("after DL = %q, want a / b / c", got)
	}
}

// TestRunStateLinesPrefersTheScreen: every read path goes through runState.lines(),
// so a shell run reads its terminal screen while a line producer keeps its list.
func TestRunStateLinesPrefersTheScreen(t *testing.T) {
	r := &runState{out: []outLine{{text: "line producer"}}}
	if got := r.lines(); len(got) != 1 || got[0].text != "line producer" {
		t.Errorf("line band = %v, want the appended lines", got)
	}
	r.scr = newTermScreen(20, 3)
	feed(r.scr, "from the screen\r\n")
	got := r.lines()
	if len(got) == 0 || stripSGR(got[0].text) != "from the screen" {
		t.Errorf("shell band = %v, want the screen's rows", got)
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
