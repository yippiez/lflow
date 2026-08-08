package multiplexer

import (
	"strings"
	"testing"
	"time"
)

// drain collects a session's whole stream into the screen, the way the editor
// update loop does, and returns when Done (or the channel closing) lands.
func drain(t *testing.T, s *Session, timeout time.Duration) {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case ev, ok := <-s.Events():
			if !ok || ev.Done {
				return
			}
			if ev.Err != nil {
				t.Fatalf("launch: %v", ev.Err)
			}
			if len(ev.Data) > 0 {
				s.Scr.Write(ev.Data)
			}
		case <-deadline:
			t.Fatalf("stream did not finish in %v", timeout)
		}
	}
}

// TestPTYRoundTrip pins the hand-rolled PTY: the child must see a real
// terminal of the requested size, and its output must land on the screen.
func TestPTYRoundTrip(t *testing.T) {
	mx := NewManager(100)
	s := mx.Start("t1", "", "stty", "", []string{"bash", "-c", "stty size; tty; echo done"}, 50, 12)
	drain(t, s, 5*time.Second)
	text := strings.Join(s.Scr.GridPlain(), "\n")
	if !strings.Contains(text, "12 50") {
		t.Fatalf("child did not see the PTY size:\n%s", text)
	}
	if !strings.Contains(text, "/dev/pts/") {
		t.Fatalf("child not on a pts:\n%s", text)
	}
	if !strings.Contains(text, "done") {
		t.Fatalf("output missing:\n%s", text)
	}
	if s.Live() {
		t.Fatal("session still live after Done")
	}
	if mx.LiveCount() != 0 {
		t.Fatal("LiveCount should be 0")
	}
}

// TestPTYKill pins the kill path: a long-running child dies, the stream ends.
func TestPTYKill(t *testing.T) {
	mx := NewManager(100)
	s := mx.Start("t2", "", "sleep", "", []string{"sleep", "60"}, 40, 10)
	time.Sleep(100 * time.Millisecond)
	s.Kill()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case _, ok := <-s.Events():
			if !ok {
				if s.Live() {
					t.Fatal("session still live after kill+close")
				}
				return
			}
		case <-deadline:
			t.Fatal("kill did not end the stream")
		}
	}
}

// TestSendText pins the quick-reply write path: the payload lands on the
// child's stdin, and the Enter arrives as its own delayed write.
func TestSendText(t *testing.T) {
	mx := NewManager(100)
	s := mx.Start("t3", "", "cat", "", []string{"bash", "-c", "read -r line; echo got:$line"}, 60, 10)
	time.Sleep(150 * time.Millisecond) // let bash reach the read
	if err := s.SendText("hello mux"); err != nil {
		t.Fatal(err)
	}
	drain(t, s, 5*time.Second)
	text := strings.Join(s.Scr.GridPlain(), "\n")
	if !strings.Contains(text, "got:hello mux") {
		t.Fatalf("payload did not reach the child:\n%s", text)
	}
}

// TestScreenOSCTitle pins the new OSC handling: title retained, prefix
// stripped, spinner glyph removed by TitleLine.
func TestScreenOSCTitle(t *testing.T) {
	s := &Session{Scr: NewScreen(40, 5, 10)}
	s.Scr.Write([]byte("\x1b]0;⠹ Fixing the parser\x07"))
	if got := s.Scr.Title(); got != "⠹ Fixing the parser" {
		t.Fatalf("title = %q", got)
	}
	if got := s.TitleLine(); got != "Fixing the parser" {
		t.Fatalf("TitleLine = %q", got)
	}
	s.Scr.Write([]byte("\x1b]2;✳ At rest\x1b\\"))
	if got := s.TitleLine(); got != "At rest" {
		t.Fatalf("TitleLine after ST form = %q", got)
	}
	// a title split across two reads must land as one title
	s.Scr.Write([]byte("\x1b]0;⠧ split "))
	s.Scr.Write([]byte("across reads\x07"))
	if got := s.TitleLine(); got != "split across reads" {
		t.Fatalf("TitleLine after split chunks = %q", got)
	}
}

// TestTerminalQueryReplies: a program that asks its terminal for device
// attributes, the cursor position and the background color gets ANSWERS back
// through the PTY — what keeps a real TUI from starting degraded.
func TestTerminalQueryReplies(t *testing.T) {
	mx := NewManager(100)
	script := `printf '\033[c'; IFS= read -rsn9 -t 3 da; printf 'da:%s\n' "$(printf '%s' "$da" | tr -d '\033')"
printf '\033[6n'; IFS= read -rsd R -t 3 pos; printf 'pos:%sR\n' "$(printf '%s' "$pos" | tr -d '\033')"
printf '\033]11;?\007'; IFS= read -rsd $'\a' -t 3 bg; printf 'bg:%s\n' "$(printf '%s' "$bg" | tr -d '\033')"`
	s := mx.Start("q1", "", "queries", "", []string{"bash", "-c", script}, 60, 10)
	drain(t, s, 10*time.Second)
	text := strings.Join(s.Scr.GridPlain(), "\n")
	if !strings.Contains(text, "da:[?62;22c") {
		t.Errorf("DA1 reply missing:\n%s", text)
	}
	if !strings.Contains(text, "pos:[") || !strings.Contains(text, "R") {
		t.Errorf("cursor position reply missing:\n%s", text)
	}
	if !strings.Contains(text, "bg:]11;rgb:") {
		t.Errorf("OSC 11 background reply missing:\n%s", text)
	}
}

// TestQueryRepliesSingle: exactly ONE answer per query — a duplicate lands in
// the program's input as a keystroke burst. (Found by the agent loop: both
// the screen scanner and the reader goroutine were answering OSC 10/11.)
func TestQueryRepliesSingle(t *testing.T) {
	mx := NewManager(100)
	script := `printf '\033]11;?\007'; sleep 0.7
stty raw -echo min 0 time 0 2>/dev/null || true
n=0; while IFS= read -rsd $'\a' -t 0.3 r; do n=$((n+1)); done
printf 'replies:%d\n' "$n"`
	s := mx.Start("qs", "", "count", "", []string{"bash", "-c", script}, 60, 8)
	drain(t, s, 10*time.Second)
	text := strings.Join(s.Scr.GridPlain(), "\n")
	if !strings.Contains(text, "replies:1") {
		t.Fatalf("want exactly one OSC 11 reply, got:\n%s", text)
	}
}

// TestQueryXTVersionAnswered: the identification queries probing apps stall on.
func TestQueryXTVersionAnswered(t *testing.T) {
	mx := NewManager(100)
	script := `printf '\033[>0q'; IFS= read -rsd '\' -t 3 v; printf 'xtver:%s\n' "$(printf '%s' "$v" | tr -d '\033')"
printf '\033[?1;1;0S'; IFS= read -rsd S -t 3 g; printf 'gfx:%sS\n' "$(printf '%s' "$g" | tr -d '\033')"`
	s := mx.Start("qv", "", "xtver", "", []string{"bash", "-c", script}, 60, 8)
	drain(t, s, 10*time.Second)
	text := strings.Join(s.Scr.GridPlain(), "\n")
	if !strings.Contains(text, "xtver:P>|lflow-mux") {
		t.Errorf("XTVERSION unanswered:\n%s", text)
	}
	if !strings.Contains(text, "gfx:[?1;1S") {
		t.Errorf("XTSMGRAPHICS unanswered:\n%s", text)
	}
}

// TestScreenModes pins DECSET tracking: bracketed paste, app cursor, cursor
// visibility.
func TestScreenModes(t *testing.T) {
	scr := NewScreen(40, 5, 10)
	scr.Write([]byte("\x1b[?2004h\x1b[?1h\x1b[?25l"))
	if !scr.BracketedPaste() || !scr.AppCursor() || scr.CursorVisible() {
		t.Fatal("DECSET set forms not tracked")
	}
	scr.Write([]byte("\x1b[?2004l\x1b[?1l\x1b[?25h"))
	if scr.BracketedPaste() || scr.AppCursor() || !scr.CursorVisible() {
		t.Fatal("DECSET reset forms not tracked")
	}
}

// TestDetectClaude pins the claude rules on representative screens.
func TestDetectClaude(t *testing.T) {
	cases := []struct {
		name    string
		title   string
		grid    []string
		want    Status
		visible bool
	}{
		{"spinner title", "⠹ Fixing tests", []string{"whatever"}, StatusWorking, true},
		{"esc to interrupt", "", []string{"· Cerebrating… (2s · esc to interrupt)"}, StatusWorking, true},
		{"prompt box", "✳ Ready", []string{"╭──────╮", "│ ❯    │", "╰──────╯"}, StatusIdle, true},
		{"permission form", "", []string{
			"Do you want to proceed?",
			"❯ 1. Yes",
			"  2. No",
			"esc to cancel · enter to confirm",
		}, StatusBlocked, true},
		{"select menu", "", []string{
			"tab/arrow keys to navigate · enter to select · esc to cancel",
		}, StatusBlocked, true},
		{"nothing", "", []string{"plain output"}, StatusIdle, false},
	}
	for _, c := range cases {
		got, vis := detectRaw("claude", c.title, c.grid)
		if got != c.want || vis != c.visible {
			t.Errorf("%s: got %v/%v want %v/%v", c.name, got, vis, c.want, c.visible)
		}
	}
}

// TestDetectOthers pins the pi/opencode/fallback rules.
func TestDetectOthers(t *testing.T) {
	if got, _ := detectRaw("pi", "", []string{"Working..."}); got != StatusWorking {
		t.Errorf("pi working: got %v", got)
	}
	if got, _ := detectRaw("prime-agent", "", []string{"Working..."}); got != StatusWorking {
		t.Errorf("prime-agent working: got %v", got)
	}
	if got, _ := detectRaw("opencode", "", []string{"△ Permission required"}); got != StatusBlocked {
		t.Errorf("opencode blocked: got %v", got)
	}
	if got, _ := detectRaw("opencode", "", []string{"esc to interrupt"}); got != StatusWorking {
		t.Errorf("opencode working: got %v", got)
	}
	if got, _ := detectRaw("someagent", "⠙ busy", nil); got != StatusWorking {
		t.Errorf("generic spinner: got %v", got)
	}
}

// TestStatusDebounce pins the working→idle hold: a chrome-less idle reading
// publishes only after it sticks for idleConfirmAfter.
func TestStatusDebounce(t *testing.T) {
	s := &Session{Agent: "claude", Scr: NewScreen(40, 5, 10)}
	s.status, s.candidate = StatusWorking, StatusWorking
	s.Scr.Write([]byte("plain output, no chrome"))
	if got := s.UpdateStatus(); got != StatusWorking {
		t.Fatalf("first chrome-less idle reading must hold working, got %v", got)
	}
	s.candidateAt = time.Now().Add(-2 * idleConfirmAfter)
	if got := s.UpdateStatus(); got != StatusIdle {
		t.Fatalf("held idle reading must publish after the hold, got %v", got)
	}
	// visible chrome publishes immediately
	s.status, s.candidate = StatusWorking, StatusWorking
	s.Scr.Write([]byte("\x1b[2J\x1b[H│ ❯ │"))
	if got := s.UpdateStatus(); got != StatusIdle {
		t.Fatalf("visible idle must publish immediately, got %v", got)
	}
}
