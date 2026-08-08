package editor

import (
	"strings"
	"testing"
	"time"

	"github.com/lflow/lflow/tui/database"
	"github.com/lflow/lflow/tui/multiplexer"
)

// fakeAgentSession starts a session under the claude variant whose "CLI" is a
// bash script — the mux does not care what binary an agent session runs.
func fakeAgentSession(t *testing.T, m *Model, id, script string) *multiplexer.Session {
	t.Helper()
	s := m.mux().Start(id, "claude", "test session", "", []string{"bash", "-c", script}, 40, 10)
	t.Cleanup(func() { m.mux().Drop(id) })
	return s
}

// drainUntil pumps session events through handleAgentMux until cond holds.
func drainUntil(t *testing.T, m *Model, s *multiplexer.Session, cond func() bool) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for !cond() {
		select {
		case ev, ok := <-s.Events():
			if !ok {
				t.Fatal("stream ended before the condition held")
			}
			m.handleAgentMux(agentMuxMsg{id: s.ID, ev: ev})
		case <-deadline:
			t.Fatal("condition did not hold in time")
		}
	}
}

// TestAgentMuxWorkingShimmer: a session whose title carries the braille
// spinner reads as working, the render global picks it up, and the chip pill
// renders the sliding background instead of the flat fill.
func TestAgentMuxWorkingShimmer(t *testing.T) {
	m := &Model{width: 80, height: 24}
	s := fakeAgentSession(t, m, "chip-a", `printf '\033]0;\342\240\271 busy\007'; sleep 3`)
	drainUntil(t, m, s, func() bool { return s.Status() == multiplexer.StatusWorking && s.Scr.Title() != "" })

	m.syncAgentMux()
	if !agentMuxWorking("chip-a") {
		t.Fatal("render global missed the working session")
	}
	if m.runningAgentCount() != 1 {
		t.Fatalf("runningAgentCount = %d, want 1", m.runningAgentCount())
	}

	c := database.Chip{ID: "chip-a", Kind: chipKindAgent, Value: "claude", Label: "test session"}
	pill := renderAgentChip(c, false, false)
	if !strings.Contains(pill, "\x1b[48;2;") {
		t.Fatalf("working pill lost its background: %q", pill)
	}
	// two different backgrounds in one pill = the highlight band is present
	first := pill[strings.Index(pill, "\x1b[48;2;"):]
	rest := first[strings.Index(first, "m")+1:]
	if !strings.Contains(rest, "\x1b[48;2;") {
		t.Fatalf("working pill is flat — no shimmer band: %q", pill)
	}

	// once the session ends, the shimmer global clears with the next sync
	m.mux().Drop("chip-a")
	m.syncAgentMux()
	if agentMuxWorking("chip-a") {
		t.Fatal("shimmer survived the session")
	}
}

// TestAgentQuitGuard: the first quit with live agents warns in the toolbar and
// stays; the second stops them and quits.
func TestAgentQuitGuard(t *testing.T) {
	m := &Model{width: 80, height: 24}
	s := fakeAgentSession(t, m, "chip-q", "sleep 30")

	if _, _ = m.quit(); m.quitting {
		t.Fatal("first quit with a live agent must not quit")
	}
	if !m.quitWarned || !m.flashErr || !strings.Contains(m.flash, "quit again") {
		t.Fatalf("first quit must warn in the toolbar, flash = %q", m.flash)
	}
	// any other key withdraws the warning
	m.quitWarned = false
	if _, _ = m.quit(); m.quitting {
		t.Fatal("a withdrawn warning must warn again, not quit")
	}
	if _, _ = m.quit(); !m.quitting {
		t.Fatal("the second quit must go through")
	}
	deadline := time.After(5 * time.Second)
	for s.Live() {
		select {
		case <-time.After(20 * time.Millisecond):
		case <-deadline:
			t.Fatal("quit did not stop the live agent session")
		}
	}
}

// TestAgentMuxToolbarCount: the bottom bar carries the red agents tally.
func TestAgentMuxToolbarCount(t *testing.T) {
	m := &Model{width: 100, height: 30}
	fakeAgentSession(t, m, "chip-t", "sleep 30")
	bar := stripSGR(strings.Join(m.bottomBar(120), "\n"))
	if !strings.Contains(bar, "1 agent running") {
		t.Fatalf("agents tally missing from bar: %q", bar)
	}
}
