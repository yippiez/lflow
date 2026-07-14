package nodes

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// simFor parses src and returns a settled sim, failing the test on a bad grid.
func simFor(t *testing.T, src string) *rsSim {
	t.Helper()
	c, err := rsParse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return newRsSim(c)
}

// lampAt reads the lamp at x,y.
func lampAt(s *rsSim, x, y int) bool { return s.lampLit[s.c.at(x, y)] }

// settle steps n ticks.
func settle(s *rsSim, n int) {
	for i := 0; i < n; i++ {
		s.step()
	}
}

func TestRSParse(t *testing.T) {
	c, err := rsParse("; a comment\nL-#T-O\n")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if c.w != 6 || c.h != 1 {
		t.Fatalf("size = %dx%d, want 6x1", c.w, c.h)
	}
	if len(c.inter) != 1 || c.kind[c.inter[0]] != rsLever {
		t.Fatalf("inter = %v, want one lever", c.inter)
	}
	if _, err := rsParse("L-?"); err == nil || !strings.Contains(err.Error(), "col 3") {
		t.Fatalf("unknown cell error = %v", err)
	}
	if _, err := rsParse("  \n; only comments\n"); err == nil {
		t.Fatal("empty circuit should error")
	}
}

func TestRSDustDecay(t *testing.T) {
	// a redstone block feeding a 16-cell dust run: power dies before the end
	s := simFor(t, "*"+strings.Repeat("-", 16))
	if p := s.power[s.c.at(1, 0)]; p != 15 {
		t.Fatalf("first dust = %d, want 15", p)
	}
	if p := s.power[s.c.at(15, 0)]; p != 1 {
		t.Fatalf("dust 15 = %d, want 1", p)
	}
	if p := s.power[s.c.at(16, 0)]; p != 0 {
		t.Fatalf("dust 16 = %d, want 0 (decayed out)", p)
	}
}

func TestRSNotGate(t *testing.T) {
	_, grid, ok := rsTemplate("@not")
	if !ok {
		t.Fatal("@not is a template")
	}
	s := simFor(t, grid)
	if !lampAt(s, 5, 0) {
		t.Fatal("lever off: lamp should be lit")
	}
	s.toggle(0)
	settle(s, 2)
	if lampAt(s, 5, 0) {
		t.Fatal("lever on: lamp should be dark")
	}
	s.toggle(0)
	settle(s, 2)
	if !lampAt(s, 5, 0) {
		t.Fatal("lever off again: lamp should relight")
	}
}

func TestRSOrGate(t *testing.T) {
	_, grid, _ := rsTemplate("@or")
	for _, tc := range []struct {
		a, b, want bool
	}{{false, false, false}, {true, false, true}, {false, true, true}, {true, true, true}} {
		s := simFor(t, grid)
		if tc.a {
			s.toggle(0)
		}
		if tc.b {
			s.toggle(1)
		}
		settle(s, 2)
		if got := lampAt(s, 4, 2); got != tc.want {
			t.Fatalf("or(%v,%v) lamp = %v, want %v", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestRSAndGate(t *testing.T) {
	_, grid, _ := rsTemplate("@and")
	for _, tc := range []struct {
		a, b, want bool
	}{{false, false, false}, {true, false, false}, {false, true, false}, {true, true, true}} {
		s := simFor(t, grid)
		if tc.a {
			s.toggle(0)
		}
		if tc.b {
			s.toggle(1)
		}
		settle(s, 4) // two torch stages
		if got := lampAt(s, 8, 2); got != tc.want {
			t.Fatalf("and(%v,%v) lamp = %v, want %v", tc.a, tc.b, got, tc.want)
		}
		if settled, _, _ := s.probe(); !settled {
			t.Fatalf("and(%v,%v) should be stable", tc.a, tc.b)
		}
	}
}

func TestRSLatch(t *testing.T) {
	_, grid, _ := rsTemplate("@latch")
	s := simFor(t, grid)
	if settled, _, _ := s.probe(); !settled {
		t.Fatal("latch should wake stable, not blinking")
	}
	q := func() bool { return lampAt(s, 6, 1) }
	if q() {
		t.Fatal("latch should wake reset (lamp dark)")
	}
	s.toggle(0) // set…
	settle(s, 4)
	s.toggle(0) // …and release
	settle(s, 4)
	if !q() {
		t.Fatal("set then release: lamp should stay lit (latched)")
	}
	s.toggle(1) // reset…
	settle(s, 4)
	s.toggle(1) // …and release
	settle(s, 4)
	if q() {
		t.Fatal("reset then release: lamp should stay dark")
	}
}

func TestRSClock(t *testing.T) {
	_, grid, _ := rsTemplate("@clock 0")
	s := simFor(t, grid)
	settled, _, period := s.probe()
	if settled || period != 2 {
		t.Fatalf("bare torch clock: settled=%v period=%d, want oscillation period 2", settled, period)
	}
	_, grid, _ = rsTemplate("@clock 2")
	s = simFor(t, grid)
	settled, _, period = s.probe()
	if settled || period != 6 {
		t.Fatalf("2-repeater clock: settled=%v period=%d, want oscillation period 6", settled, period)
	}
}

func TestRSRepeaterIsADiode(t *testing.T) {
	s := simFor(t, "*->O")
	settle(s, 2)
	if !lampAt(s, 3, 0) {
		t.Fatal("repeater should carry power forward")
	}
	s = simFor(t, "O<-*")
	settle(s, 2)
	if !lampAt(s, 0, 0) {
		t.Fatal("< repeater should carry power leftward")
	}
	s = simFor(t, "*-<O") // facing away from the source: nothing crosses
	settle(s, 2)
	if lampAt(s, 3, 0) {
		t.Fatal("repeater must not carry power backward")
	}
}

func TestRSButtonExpires(t *testing.T) {
	s := simFor(t, "B-O")
	s.toggle(0)
	if !lampAt(s, 2, 0) {
		t.Fatal("pressed button should light the lamp")
	}
	settle(s, rsBtnTicks)
	if lampAt(s, 2, 0) {
		t.Fatalf("button should expire after %d ticks", rsBtnTicks)
	}
}

func TestRSRunExpandsTemplates(t *testing.T) {
	h := newFakeHost(t)
	n := &fakeNode{uuid: "r1", typ: "redstone", text: "@and"}
	if cmd := runRedstone(h, n); cmd != nil {
		t.Fatal("expansion is synchronous")
	}
	if !strings.Contains(n.text, "#T") || strings.Contains(n.text, "@") {
		t.Fatalf("text should now be the generated grid, got %q", n.text)
	}
	if !strings.Contains(h.flash, "generated @and") {
		t.Fatalf("flash = %q", h.flash)
	}
}

func TestRSRunProbesHeadless(t *testing.T) {
	h := newFakeHost(t)
	n := &fakeNode{uuid: "r2", typ: "redstone", text: "*-O"}
	runRedstone(h, n)
	if !strings.Contains(h.flash, "stable") || !strings.Contains(h.flash, "1/1 lamps") {
		t.Fatalf("flash = %q", h.flash)
	}
	n.text = "L-?"
	runRedstone(h, n)
	if !strings.Contains(h.flash, "unknown cell") {
		t.Fatalf("flash = %q", h.flash)
	}
}

func TestRSViewSimFlow(t *testing.T) {
	h := newFakeHost(t)
	n := &fakeNode{uuid: "r3", typ: "redstone", text: "@not"}
	v := rsView{}
	if !v.Enter(h, n) {
		t.Fatal("Enter should focus")
	}
	st := rsStateOf(h, n.UUID())
	if st.buf != "@not" {
		t.Fatalf("buf = %q", st.buf)
	}
	altR := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r"), Alt: true}
	if _, handled := v.Key(h, n, altR); !handled {
		t.Fatal("alt+r should expand the directive")
	}
	if st.simOn || !strings.Contains(st.buf, "#T") || n.text != st.buf {
		t.Fatalf("directive should expand in place: simOn=%v buf=%q text=%q", st.simOn, st.buf, n.text)
	}
	v.Key(h, n, altR) // now a grid: enter the simulator
	if !st.simOn || st.sim == nil {
		t.Fatal("alt+r on a grid should start the sim")
	}
	if !strings.Contains(h.flash, "stable") {
		t.Fatalf("sim entry should probe, flash = %q", h.flash)
	}
	tick := st.sim.tick
	v.Key(h, n, tea.KeyMsg{Type: tea.KeySpace, Runes: []rune(" ")})
	if st.sim.tick != tick+1 {
		t.Fatal("space should step one tick")
	}
	v.Key(h, n, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("1")})
	if !st.sim.leverOn[st.sim.c.inter[0]] {
		t.Fatal("digit 1 should flip lever 1")
	}
	bands := v.Bands(h, n, "", 80, 0, 0, true)
	if len(bands) != st.sim.c.h+2 {
		t.Fatalf("sim bands = %d lines, want grid+header+footer = %d", len(bands), st.sim.c.h+2)
	}
	v.Key(h, n, tea.KeyMsg{Type: tea.KeyEsc})
	if st.simOn {
		t.Fatal("esc should return to the source face")
	}
	if _, handled := v.Key(h, n, tea.KeyMsg{Type: tea.KeyEsc}); handled {
		t.Fatal("esc on the source face falls through to the outline")
	}
}

func TestRSPlayHeartbeat(t *testing.T) {
	h := newFakeHost(t)
	n := &fakeNode{uuid: "r4", typ: "redstone", text: "; clock\n#T+\n|.|\n+-+"}
	v := rsView{}
	v.Enter(h, n)
	st := rsStateOf(h, n.UUID())
	altR := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r"), Alt: true}
	v.Key(h, n, altR)
	if !st.simOn {
		t.Fatal("sim should be on")
	}
	cmd, _ := v.Key(h, n, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("p")})
	if cmd == nil || !st.play {
		t.Fatal("p should arm the play heartbeat")
	}
	tick := st.sim.tick
	if next := (rsPlayMsg{uuid: n.UUID()}).HandleNodePlugin(h); next == nil {
		t.Fatal("a play beat should re-arm while playing")
	}
	if st.sim.tick != tick+1 {
		t.Fatal("a play beat should step the sim")
	}
	st.play = false
	if next := (rsPlayMsg{uuid: n.UUID()}).HandleNodePlugin(h); next != nil {
		t.Fatal("a stopped sim must not re-arm")
	}
}
