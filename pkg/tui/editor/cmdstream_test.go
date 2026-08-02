package editor

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/lflow/lflow/pkg/tui/database"
)

// liveCmdChip builds a model whose one node carries a cmd chip, with a run in
// flight holding the given output lines. It returns the chip and the row the chip
// sits on, ready for the band/render assertions below.
func liveCmdChip(t *testing.T, cmd string, out ...string) (*Model, database.Chip, row) {
	t.Helper()
	m, _ := dbModel(t, database.Node{UUID: "edit", Name: ""})
	cursorOn(m, "edit")
	m.caret = 0
	c := typeCmdChip(t, m)
	setCmdChipValue(m, c.ID, cmd)
	r := m.ensureRun(c.ID)
	r.cancel = func() {}
	r.started = time.Now().Add(-4 * time.Second)
	r.loaded = true
	for _, l := range out {
		m.appendRunOut(c.ID, outLine{text: l})
	}
	m.setCmdPreview(c.ID) // what the stream wiring does after every batch
	m.refreshRows()
	return m, c, m.rows[m.cursor]
}

// TestCmdChipPreviewStreamsTailWhileRunning: a running chip's → tail tracks the
// NEWEST line, so a long-running command shows progress instead of staying blank
// until it exits; once the run finishes the tail settles on the run's headline.
func TestCmdChipPreviewStreamsTailWhileRunning(t *testing.T) {
	m, c, _ := liveCmdChip(t, "build", "step 1", "step 2")

	m.setCmdPreview(c.ID)
	if got := m.chips[c.ID].Label; got != "step 2" {
		t.Errorf("streaming label = %q, want the newest line %q", got, "step 2")
	}
	// more output arrives: the tail follows it (a trailing blank line is skipped,
	// never blanking the preview)
	m.appendRunOut(c.ID, outLine{text: "step 3"})
	m.appendRunOut(c.ID, outLine{text: "   "})
	m.setCmdPreview(c.ID)
	if got := m.chips[c.ID].Label; got != "step 3" {
		t.Errorf("streaming label = %q, want %q", got, "step 3")
	}

	// finished: the headline (first line) is the resting preview
	m.finishRun(c.ID)
	if got := m.chips[c.ID].Label; got != "step 1" {
		t.Errorf("settled label = %q, want the first line %q", got, "step 1")
	}
}

// TestCmdChipPreviewCollapsesSpaces: the → tail is a space saver — runs of
// spaces in a wide, columned or ASCII-art output line collapse to one in the
// PREVIEW only (the stored band keeps its own spacing). This is what keeps a
// "grill-me         neural_netwo…" line reading as "grill-me neural_netwo…".
func TestCmdChipPreviewCollapsesSpaces(t *testing.T) {
	m, c, _ := liveCmdChip(t, "ls", "grill-me         neural_netwo…")
	m.setCmdPreview(c.ID)
	if got := m.chips[c.ID].Label; got != "grill-me neural_netwo…" {
		t.Errorf("preview = %q, want spaces collapsed", got)
	}
	// the band itself keeps the wide line as-is
	r := m.run(c.ID)
	if r == nil || len(r.lines()) != 1 || r.lines()[0].text != "grill-me         neural_netwo…" {
		t.Errorf("stored band lost its spacing: %q", r.lines()[0].text)
	}
}

// TestCmdChipRunClearsStalePreview: launching a run drops the previous run's →
// tail immediately, so what is on the row is never last run's result presented as
// this one's.
func TestCmdChipRunClearsStalePreview(t *testing.T) {
	m, c, _ := liveCmdChip(t, "true", "old result")
	m.finishRun(c.ID)
	if m.chips[c.ID].Label == "" {
		t.Fatal("expected a settled preview to seed the test")
	}

	cmd := m.runCmdChip(m.chips[c.ID]) // the real launch path
	defer func() {
		if r := m.run(c.ID); r != nil && r.cancel != nil {
			r.cancel()
		}
	}()
	if cmd == nil {
		t.Fatal("runCmdChip returned no stream command")
	}
	if got := m.chips[c.ID].Label; got != "" {
		t.Errorf("label = %q at launch, want cleared", got)
	}
	if m.run(c.ID).started.IsZero() {
		t.Error("run did not stamp its start time (no elapsed clock)")
	}
}

// TestExpandedViewHeightNeverMoves: the terminal pane is a WINDOW, not a
// growing list — an empty run opens at exactly the height it will have at a
// thousand lines, so nothing below it shifts while output streams in. It also
// follows the tail, the way a terminal scrolls itself.
func TestExpandedViewHeightNeverMoves(t *testing.T) {
	m, c, _ := liveCmdChip(t, "build")
	m.focusCmdChip(m.chips[c.ID])
	it := m.cursorItem()

	const winH = 8
	empty := (cmdChipView{}).Bands(m, it, "", 80, 0, winH, true)
	if len(empty) != winH {
		t.Fatalf("an empty run drew %d rows, want the full %d-row pane", len(empty), winH)
	}
	for i, n := range []int{1, 3, 12, 400} {
		for len(m.run(c.ID).out) < n {
			m.appendRunOut(c.ID, outLine{text: fmt.Sprintf("line %d", len(m.run(c.ID).out)+1)})
		}
		got := (cmdChipView{}).Bands(m, it, "", 80, 0, winH, true)
		if len(got) != winH {
			t.Errorf("step %d (%d lines): pane is %d rows, want a fixed %d", i, n, len(got), winH)
		}
		// following the tail: the newest line is on screen, the oldest is not
		flat := stripSGR(strings.Join(got, "\n"))
		if newest := fmt.Sprintf("line %d", n); !strings.Contains(flat, newest) {
			t.Errorf("%d lines: pane lost the tail (%q):\n%s", n, newest, flat)
		}
	}
	// scrolling up stops the follow and shows the head — still the same height
	m.focusFollow = false
	top := (cmdChipView{}).Bands(m, it, "", 80, 0, winH, true)
	if len(top) != winH {
		t.Errorf("scrolled pane is %d rows, want %d", len(top), winH)
	}
	if flat := stripSGR(strings.Join(top, "\n")); !strings.Contains(flat, "line 1") {
		t.Errorf("scrolled to the top, want the first line:\n%s", flat)
	}
}

// TestRunningChipClaimsNoBand: a collapsed row streams entirely on itself — the
// running chip must not grow an output band underneath, so a run never pushes the
// outline around. The output is the expanded view's (alt+e) job.
func TestRunningChipClaimsNoBand(t *testing.T) {
	m, c, _ := liveCmdChip(t, "tail -f log", "one", "two", "three", "four")

	_, bands := m.viewRenderRows(80)
	under := strings.Join(bands[m.cursor], "\n")
	for _, line := range []string{"one", "two", "three", "four", "running…", "⌥r stop"} {
		if strings.Contains(stripSGR(under), line) {
			t.Errorf("a running chip drew %q under the row:\n%s", line, stripSGR(under))
		}
	}
	// the row itself carries the live state: the newest line as the chip's tail
	if got := m.chips[c.ID].Label; got != "four" {
		t.Errorf("row tail = %q, want the newest line %q", got, "four")
	}

	// alt+e is where the output lives, streaming the same feed
	m.focusCmdChip(m.chips[c.ID])
	view := strings.Join((cmdChipView{}).Bands(m, m.cursorItem(), "", 80, 0, 20, true), "\n")
	for _, want := range []string{"one", "two", "three", "four", "running…", "4s", "⌥r stop"} {
		if !strings.Contains(stripSGR(view), want) {
			t.Errorf("expanded band missing %q:\n%s", want, stripSGR(view))
		}
	}
}

// TestRunningChipShimmers: a running chip's code cell is painted with the sliding
// background highlight and keeps moving with the animation frame — while its
// DISPLAY WIDTH stays exactly chipDisplay's, which the caret and wrap math depend
// on. The block cursor wins over the shimmer when it sits on the chip.
func TestRunningChipShimmers(t *testing.T) {
	m, c, _ := liveCmdChip(t, "make test")
	m.syncLiveCmdRuns()
	live := m.chips[c.ID]

	// the highlight sweeps in waves, so some frames rest at the flat cell color —
	// over one sweep it must both LIFT the background above bgCode and change from
	// frame to frame.
	defer func() { animFrame = 0 }()
	lifted, moved, prev := false, false, ""
	for f := 0; f < 60; f++ {
		animFrame = f
		got := renderCmdChip(live, false)
		// a background sequence other than the resting cell means the band is passing
		for _, seq := range strings.Split(got, "\x1b[48;2;")[1:] {
			if end := strings.IndexByte(seq, 'm'); end >= 0 && "\x1b[48;2;"+seq[:end+1] != bgCode {
				lifted = true
			}
		}
		if prev != "" && got != prev {
			moved = true
		}
		prev = got
	}
	if !lifted {
		t.Error("the shimmer never lifted the chip's background above the flat code cell")
	}
	if !moved {
		t.Error("the shimmer did not move with the animation frame")
	}
	// alt+r runs the chip the caret sits on, so a running chip is usually under the
	// caret: it keeps shimmering, with the caret drawn as one inverted cell ("$")
	// rather than reverse video over the whole pulse.
	animFrame = 12
	onCaret := renderCmdChip(live, true)
	if !strings.Contains(onCaret, cInvert+"$"+cReset) {
		t.Errorf("running chip under the caret lost its one-cell cursor: %q", onCaret)
	}
	if strings.Contains(onCaret, cInvert+bgCode) {
		t.Errorf("running chip under the caret inverted the whole cell: %q", onCaret)
	}
	if visibleWidth(onCaret) != visibleWidth(chipDisplay(live)) {
		t.Errorf("caret-on running chip width = %d, want %d", visibleWidth(onCaret), visibleWidth(chipDisplay(live)))
	}
	// a settled chip under the caret still inverts as a whole, as before
	liveCmdRuns = nil
	if settled := renderCmdChip(live, true); !strings.Contains(settled, cInvert+bgCode) {
		t.Errorf("settled chip under the caret lost its inverted code cell: %q", settled)
	}
	m.syncLiveCmdRuns()

	// width invariant: painting must not change how wide the chip is
	want := visibleWidth(chipDisplay(live))
	for _, name := range []string{"running", "settled"} {
		if name == "settled" {
			liveCmdRuns = nil
		}
		if got := visibleWidth(renderCmdChip(live, false)); got != want {
			t.Errorf("%s chip width = %d, want %d (caret/wrap math reads chipDisplay)", name, got, want)
		}
	}
}

// TestRunKeepsAnimationTicking: the animation clock must stay armed for as long as
// a command runs — that is what animates the shimmer and the elapsed clock for a
// command that prints nothing at all (a bare sleep).
func TestRunKeepsAnimationTicking(t *testing.T) {
	m, c, _ := liveCmdChip(t, "sleep 30")
	if !m.anyRunning() || !m.animActive() {
		t.Fatal("a live run must keep the animation tick active")
	}
	m.finishRun(c.ID)
	if m.anyRunning() || m.animActive() {
		t.Error("a finished run must let the animation tick stop")
	}
}

// TestBashLinesMsgStreamsThroughUpdate: the streamed batch lands in the band AND
// refreshes the chip's inline tail in the same update, and the stream keeps
// pumping (a follow-up command is returned) — the wiring that makes output appear
// while the command is still running.
func TestBashLinesMsgStreamsThroughUpdate(t *testing.T) {
	m, c, _ := liveCmdChip(t, "deploy")
	m.run(c.ID).ch = make(chan tea.Msg, 1)

	_, cmd := m.Update(bashLinesMsg{uuid: c.ID, lines: []outLine{{text: "uploading"}}})
	if cmd == nil {
		t.Fatal("streaming stopped: no follow-up wait command")
	}
	if got := len(m.run(c.ID).out); got != 1 {
		t.Fatalf("band holds %d lines, want 1", got)
	}
	if got := m.chips[c.ID].Label; got != "uploading" {
		t.Errorf("inline tail = %q, want the streamed line %q", got, "uploading")
	}

	// a canceled run stops draining — no follow-up command, nothing appended
	m.finishRun(c.ID)
	if _, cmd := m.Update(bashLinesMsg{uuid: c.ID, lines: []outLine{{text: "late"}}}); cmd != nil {
		t.Error("a canceled run kept streaming")
	}
}

// TestElapsedShort pins the live clock's format at the boundaries the footer shows.
func TestElapsedShort(t *testing.T) {
	cases := []struct {
		d    time.Duration
		want string
	}{
		{900 * time.Millisecond, "0s"},
		{4 * time.Second, "4s"},
		{59 * time.Second, "59s"},
		{64 * time.Second, "1m04s"},
		{2*time.Hour + 3*time.Minute, "2h03m"},
	}
	for _, c := range cases {
		if got := elapsedShort(c.d); got != c.want {
			t.Errorf("elapsedShort(%s) = %q, want %q", c.d, got, c.want)
		}
	}
}
