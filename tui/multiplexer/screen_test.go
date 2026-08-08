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

// TestTermDarkTruecolorSurvives: zero channels inside an extended color are
// channel values, not SGR resets — the bug that shredded an agent TUI's dark
// theme. Both semicolon and colon forms must come back intact.
func TestTermDarkTruecolorSurvives(t *testing.T) {
	s := newTestScreen(30, 3)
	feed(s, "\x1b[38;2;0;0;0mink\x1b[0m\r\n")
	row := s.Lines()[0]
	if !strings.Contains(row, "\x1b[38;2;0;0;0m") {
		t.Errorf("black truecolor mangled: %q", row)
	}

	s = newTestScreen(30, 3)
	feed(s, "\x1b[1;38;2;0;10;0mbold dark\x1b[0m\r\n")
	row = s.Lines()[0]
	if !strings.Contains(row, "\x1b[1m") || !strings.Contains(row, "\x1b[38;2;0;10;0m") {
		t.Errorf("bold + dark truecolor mangled: %q", row)
	}

	s = newTestScreen(30, 3)
	feed(s, "\x1b[38:2:0:20:40mcolon\x1b[0m\r\n")
	row = s.Lines()[0]
	if !strings.Contains(row, "38:2:0:20:40") {
		t.Errorf("colon-form truecolor mangled: %q", row)
	}

	// a bare zero still resets
	s = newTestScreen(30, 3)
	feed(s, "\x1b[31mred\x1b[0mplain\r\n")
	row = s.Lines()[0]
	if strings.Contains(row[strings.Index(row, "red")+3:], "\x1b[31m") {
		t.Errorf("bare zero failed to reset the pen: %q", row)
	}
}

// TestTermWideRunes: emoji and CJK occupy two columns, so a box drawn around
// them stays aligned — and the plain-text read renders each wide rune once.
func TestTermWideRunes(t *testing.T) {
	s := newTestScreen(10, 3)
	feed(s, "a猫b\r\n")
	got := screenText(s)
	if got[0] != "a猫b" {
		t.Errorf("wide-rune row = %q, want a猫b", got[0])
	}
	// the wide rune consumed two columns: the next rune lands at column 3
	s = newTestScreen(4, 3)
	feed(s, "猫猫x\r\n") // 2+2 fills the row; x wraps
	got = screenText(s)
	if got[0] != "猫猫" || got[1] != "x" {
		t.Errorf("wide wrap rows = %q, want 猫猫 / x", got)
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

// TestTermPrivatePrefixIgnored: protocol sequences with private prefixes must
// not execute as screen edits — kitty-keyboard push/pop read as SCORC warped
// the cursor home, XTMODKEYS read as SGR underline+faint, XTSAVE as SCOSC,
// XTRESTORE as DECSTBM, XTSMGRAPHICS as scroll-up. (Found by the agent loop.)
func TestTermPrivatePrefixIgnored(t *testing.T) {
	s := newTestScreen(40, 12)
	feed(s, "A normal line\r\n")
	feed(s, "\x1b[>4;2m", "B after XTMODKEYS\r\n")
	feed(s, "\x1b[>1u", "C after kitty push\r\n")
	feed(s, "\x1b[<u", "D after kitty pop\r\n")
	feed(s, "\x1b[?1004s", "E after XTSAVE\r\n")
	feed(s, "\x1b[?1;1;0S", "X after sixel query\r\n")
	feed(s, "\x1b[?1004;1006r", "Y after XTRESTORE\r\n")
	got := screenText(s)
	want := []string{"A normal line", "B after XTMODKEYS", "C after kitty push",
		"D after kitty pop", "E after XTSAVE", "X after sixel query", "Y after XTRESTORE"}
	for i, w := range want {
		if i >= len(got) || got[i] != w {
			t.Fatalf("row %d = %q, want %q — private prefix executed as an edit:\n%v", i, got[min(i, len(got)-1)], w, got)
		}
	}
	if raw := s.Lines()[1]; strings.Contains(raw, "\x1b[4m") || strings.Contains(raw, "\x1b[2m") {
		t.Errorf("XTMODKEYS leaked into the pen: %q", raw)
	}
}

// TestTermBackColorErase: ED/EL fill with the pen's BACKGROUND, so a program
// that paints \e[44m\e[2J gets a blue screen, not default holes.
func TestTermBackColorErase(t *testing.T) {
	s := newTestScreen(24, 6)
	feed(s, "\x1b[44m\x1b[2J")
	for y, row := range s.GridLines(false) {
		if !strings.Contains(row, "\x1b[44m") {
			t.Fatalf("row %d lost the erase background: %q", y, row)
		}
	}
	feed(s, "\x1b[3;1HAAAA\x1b[41m\x1b[K")
	if row := s.GridLines(false)[2]; !strings.Contains(row, "\x1b[41m") {
		t.Errorf("EL 0 did not fill with the red background: %q", row)
	}
	// scroll fills carry it too
	s = newTestScreen(10, 2)
	feed(s, "\x1b[42m", "a\r\nb\r\nc\r\n")
	if row := s.GridLines(false)[1]; !strings.Contains(row, "\x1b[42m") {
		t.Errorf("scroll fill lost the background: %q", row)
	}
	// and a bare reset clears it
	s = newTestScreen(10, 2)
	feed(s, "\x1b[44m\x1b[0m\x1b[2J")
	if row := s.GridLines(false)[0]; strings.Contains(row, "44") {
		t.Errorf("reset did not clear the erase background: %q", row)
	}
}

// TestTermZeroWidthClusters: combining accents attach to their base cell and
// ZWJ emoji sequences stay one cluster — café keeps its accent, the family
// stays a family. (Found by the agent loop.)
func TestTermZeroWidthClusters(t *testing.T) {
	s := newTestScreen(40, 3)
	feed(s, "café ë\r\n")
	if got := s.GridPlain()[0]; got != "café ë" {
		t.Fatalf("combining marks lost: %q", got)
	}
	s = newTestScreen(40, 3)
	family := "\U0001F468‍\U0001F469‍\U0001F467"
	feed(s, family+" x\r\n")
	if got := s.GridPlain()[0]; !strings.Contains(got, family) {
		t.Fatalf("ZWJ cluster split apart: %q", got)
	}
	// the cluster occupies ONE wide cell: x lands at column 3, not column 7
	if x := strings.Index(strings.ReplaceAll(s.GridPlain()[0], family, "FF"), "x"); x != 3 {
		t.Errorf("cluster width wrong: x at %d, want 3", x)
	}
}

// TestTermHyperlinksSurvive: OSC 8 targets stamp their cells and render back
// out, so linked text keeps its URL through the screen.
func TestTermHyperlinksSurvive(t *testing.T) {
	s := newTestScreen(40, 3)
	feed(s, "\x1b]8;;https://x.dev\x1b\\docs\x1b]8;;\x1b\\ plain\r\n")
	row := s.Lines()[0]
	if !strings.Contains(row, "\x1b]8;;https://x.dev\x1b\\") {
		t.Fatalf("link target lost: %q", row)
	}
	if !strings.Contains(row, "\x1b]8;;\x1b\\") {
		t.Errorf("link never closed: %q", row)
	}
	if got := s.GridPlain()[0]; got != "docs plain" {
		t.Errorf("text mangled by the link: %q", got)
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
