package editor

import (
	"fmt"
	"os"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lflow/lflow/tui/multiplexer"
)

// The screen a session paints renders through the multiplexer package now;
// point its unstyled-cell foreground at the editor's own.
func init() {
	multiplexer.Reset = cReset
	multiplexer.PlainFG = cFG
}

// clampInt bounds v to [lo, hi] — shared by the sizing and scroll math.
func clampInt(v, lo, hi int) int { return max(lo, min(v, hi)) }

// outLine is one line of a run band; err marks stderr (rendered red). Line
// producers (math's LaTeX export writes r.out directly) use these; a SHELL run
// has no separate stderr — it runs on a PTY, where both streams interleave on one
// screen exactly as they do in a terminal — and its lines are rendered from that
// screen.
type outLine struct {
	text string
	err  bool
}

// bashLinesMsg carries a BATCH of captured lines onto the run band. Today its
// one async producer is forwardMux's launch-failure path; the type stays the
// seam for any future line producer that streams instead of writing r.out.
type bashLinesMsg struct {
	uuid  string
	lines []outLine
}

// bashBytesMsg carries a BATCH of raw output bytes from a shell run, straight
// from the PTY into the run's terminal screen. Batching happens in the producer
// on a ~50ms window (see multiplexer.Session), so a torrential command costs the UI at most
// ~20 bounded messages a second — one message per read would pin the update loop
// for the whole run and freeze the editor.
type bashBytesMsg struct {
	uuid string
	data []byte
}

// bashDoneMsg says the stream for uuid ended; finishRun closes out the band.
type bashDoneMsg struct {
	uuid string
}

// Shell runs get a PTY of this size: a classic terminal height, and a width the
// caller passes from the editor's body (so wrapping matches what the band shows).
// Programs size their output to it — columns for tables, rows for full-screen
// apps — the same way they would in a terminal window.
const termRows = 24

// runShell is the run-or-cancel toggle for an arbitrary shell command, keyed by
// node uuid (the Bash node) or chip id (the cmd chip, runCmdChip). Output lands
// on a terminal screen, not a line list.
func runShell(m *Model, id, cmd string) tea.Cmd {
	if r := m.run(id); r != nil && r.cancel != nil {
		r.cancel()
		m.finishRun(id) // keep whatever was captured before the cancel
		return nil
	}
	return m.startShellRun(id, cmd)
}

// startShellRun is the one launch path for a shell run, keyed by node uuid or
// cmd chip id: a fresh multiplexer session (PTY + terminal screen) sized to
// the editor's body, its stream forwarded into the update loop.
func (m *Model) startShellRun(id, cmd string) tea.Cmd {
	r := m.ensureRun(id)
	r.started = time.Now() // the elapsed clock the band shows while it runs
	r.dropped = 0          // a fresh run starts its drop count over
	r.cmd = cmd            // what was run — a later edit to the node invalidates its tail
	// a fresh run owns the band now: memory is authoritative, so don't reload the
	// old on-disk output over the incoming stream.
	r.loaded = true
	r.out = nil
	dir := m.captureRunPWD(id)
	// a previous run under this id may still be winding down; a fresh run always
	// gets a fresh session
	m.mux().Drop(id)
	sess := m.mux().Start(id, "", cmd, dir, []string{"bash", "-c", cmd}, m.runCols(), termRows)
	r.scr = sess.Scr
	r.cancel = sess.Kill
	ch := make(chan tea.Msg, 1024)
	r.ch = ch
	go forwardMux(sess, ch)
	// start the shimmer IMMEDIATELY, not on the first output byte: a silent
	// command (`sleep 30`) would otherwise never kick the animation tick and a
	// running row would sit static. startAnim batches the tick on and keeps it
	// alive via anyRunning while the command is in flight.
	return m.startAnim(waitBashCmd(ch))
}

// forwardMux adapts a session's event stream into the update loop's message
// vocabulary: output batches become bashBytesMsg, a launch failure becomes a
// red band line, the end of the stream a bashDoneMsg. Sends select against the
// session's Done so a canceled run can never strand this goroutine — the
// editor stops draining after a cancel.
func forwardMux(s *multiplexer.Session, ch chan tea.Msg) {
	// closing wakes any reader still blocked in waitBashCmd; the resulting nil
	// msg is ignored by bubbletea
	defer close(ch)
	send := func(msg tea.Msg) bool {
		select {
		case ch <- msg:
			return true
		case <-s.Done():
			return false
		}
	}
	for ev := range s.Events() {
		switch {
		case ev.Err != nil:
			if !send(bashLinesMsg{ev.ID, []outLine{{text: ev.Err.Error(), err: true}}}) {
				return
			}
		case ev.Done:
		case len(ev.Data) > 0:
			if !send(bashBytesMsg{ev.ID, ev.Data}) {
				return
			}
		}
		if ev.Done {
			break
		}
	}
	send(bashDoneMsg{s.ID})
}

// runCols is the column count a run's terminal gets: the editor's body width less
// the band's rail, clamped to something a program can lay out in.
func (m *Model) runCols() int {
	cols := m.width - 6
	return clampInt(cols, 40, 200)
}

func waitBashCmd(ch chan tea.Msg) tea.Cmd {
	return func() tea.Msg { return <-ch }
}

// captureRunPWD remembers the working directory a run is launched from and
// returns it — ONE Getwd snapshot serving both the alt+e header's "pwd:" row and
// the shell's Cmd.Dir ("" on the rare Getwd failure: no header row, cwd
// inherited).
func (m *Model) captureRunPWD(id string) string {
	pwd := processCWD()
	m.ensureRun(id).pwd = pwd
	return pwd
}

// processCWD returns the editor process working directory, or "" when unknown —
// where a run (and an agent session) is launched from.
func processCWD() string {
	pwd, err := os.Getwd()
	if err != nil {
		return ""
	}
	return pwd
}

// elapsedShort renders a run's age compactly for the live band: "4s", "1m04s",
// "1h02m" — long enough to make a long-running command's age obvious at a glance,
// short enough to sit in a one-line footer.
func elapsedShort(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm%02ds", int(d.Minutes()), int(d.Seconds())%60)
	default:
		return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
	}
}

// runningTag is the shared live-run indicator every band and header shows while a
// command is in flight: a shimmering "running…" (the same sliding highlight the
// chip's background wears) followed by the elapsed clock. It ends on cDim, so a
// caller in a dim run of text can keep appending.
func runningTag(r *runState) string {
	s := shimmerLabel("running…")
	if d := r.elapsed(); d > 0 {
		s += cDim + " " + elapsedShort(d)
	}
	return s
}

// runTailWidth bounds the headline both shell surfaces hang after their "→".
// It is a fixed clip on purpose: the row must not rewrap as output streams
// through it, or the view below would shift on every line.
const runTailWidth = 32

// runTail is one node row's live "→" section for this frame.
type runTail struct {
	text    string
	running bool
	cmd     string // the command the run corresponds to — a changed cmd invalidates it
}

// runTails is the render-time map of node uuid → the headline its row hangs,
// the node-side twin of a cmd chip's Label. Same render-time global discipline
// as liveCmdRuns and animFrame: the bodyTail hook is a pure
// func(item, chips) reached from every render surface, and this is the one bit
// of run state it needs. syncRunTails refreshes it once per frame, and both it
// and the readers run on bubbletea's single goroutine.
var runTails map[string]runTail

// syncRunTails snapshots each node run's headline for this frame's render. Chip
// runs are skipped — a chip carries its headline in its own Label. A node that is
// RUNNING but has produced nothing yet still gets a tail (with running=true), so
// the row reads as alive instead of falling back to the dim command preview —
// the tail hook turns that into the shimmering "running…". The cmd is carried so
// a later edit to the node (whose composed command then differs) drops the tail.
func (m *Model) syncRunTails() {
	tails := map[string]runTail{}
	for id, r := range m.runs {
		if _, isChip := m.chips[id]; isChip {
			continue
		}
		if t := runHeadline(r); t != "" || r.cancel != nil {
			tails[id] = runTail{text: t, running: r.cancel != nil, cmd: r.cmd}
		}
	}
	runTails = tails
}

// finishRun closes out a run band — the stream ended (done, canceled, or
// stopped) — persisting what memory holds and dropping the live bookkeeping.
func (m *Model) finishRun(uuid string) {
	if r := m.run(uuid); r != nil {
		r.cancel = nil // no longer running; the band (out) is kept
		r.ch = nil
	}
	// a shell session's record has nothing left to say once the band is
	// persisted — the screen lives on in runState; agent sessions stay filed
	if m.muxm != nil {
		if s := m.muxm.Get(uuid); s != nil && s.Agent == "" {
			m.muxm.Drop(uuid)
		}
	}
	m.persistRunOut(uuid)
	m.setCmdPreview(uuid)
}

// The PTY producer itself — spawn, read, coalesce on a ~50ms window — lives in
// the multiplexer package now (Session), shared with agent sessions. The PTY
// remains the point: with a pipe, a program sees "not a terminal" and changes
// what it prints; on a PTY it behaves exactly as it would in one.

// runOutView is the generic inline run-output viewer (alt+e) for runnable
// node types (mods with a `run` hook): the full captured band, scrollable and
// read-only, with the program's colors preserved (see styleOutLine). It is
// stateless — the only state is the shared focusScroll offset, which the
// central render loop clamps each frame.
type runOutView struct{}

func (runOutView) enter(m *Model, it *item) bool {
	m.ensureRunOutLoaded(it.uuid)
	m.focusScroll, m.focusFollow = 0, true
	return true // focus even when empty so the placeholder explains how to run
}

func (runOutView) leave(m *Model, it *item) { m.focusFollow = false }

func (runOutView) lines(m *Model, it *item, width int) int {
	return runViewLines(m, it.uuid)
}

// Key scrolls the viewer; esc/ctrl+c fall through to central defocus.
func (runOutView) key(m *Model, it *item, k tea.KeyMsg) (tea.Cmd, bool) {
	return runViewKey(m, k)
}

// Bands renders the header and output lines, self-windowed to [scroll, scroll+winH).
func (runOutView) bands(m *Model, it *item, rail string, width, scroll, winH int, focused bool) []string {
	m.ensureRunOutLoaded(it.uuid)
	r := m.run(it.uuid) // non-nil after ensureRunOutLoaded
	hdr := fmt.Sprintf("  %s · %d lines", it.typ, len(r.lines()))
	return runViewBands(m, r, hdr, "⌥k", rail, width, scroll, winH)
}

// ── the shared expanded output surface ──────────────────────────────────────

// A run's expanded band (alt+e) is the TERMINAL for that run: one chrome header,
// then the command's screen exactly as a terminal would show it — the program's
// own colors, its overwrites and clears already applied by the screen (see
// term.go), plain output in the normal text color rather than muted chrome.
//
// It also FOLLOWS the output like a terminal does: a fresh focus sits at the
// bottom and stays there as lines arrive, until you scroll up (which stops the
// follow); end/G resumes it. runOutView and cmdChipView share both halves so the
// Bash node and the cmd chip behave identically.

// runViewLines is the CONTENT height both expanded run views report — one
// header plus every output line. It is what the central loop clamps scrolling
// against; the pane actually drawn is a fixed window onto it (see runViewBands),
// so this growing with the output moves nothing on screen.
func runViewLines(m *Model, id string) int {
	m.ensureRunOutLoaded(id)
	r := m.run(id) // non-nil after ensureRunOutLoaded
	n := 1 + max(len(r.lines()), 1)
	// A run IN FLIGHT keeps a floor under its pane: output arrives a line at a
	// time, and a pane that grew with it would shuffle the outline below on every
	// line. A finished run has nothing left to arrive, so it is exactly its own
	// size — which is what keeps ⌥e on a short result from opening a blank page.
	if r.cancel != nil && n < runPaneFloor {
		n = runPaneFloor
	}
	return n
}

// runPaneFloor is the height a streaming run's pane holds even while empty —
// enough to read a few lines of output without the pane resizing under them.
const runPaneFloor = 10

// runViewKey is the scroll handling both expanded run views use.
func runViewKey(m *Model, k tea.KeyMsg) (tea.Cmd, bool) {
	switch k.String() {
	case "down", "j", "pgdown":
		step := 1
		if k.String() == "pgdown" {
			step = 10
		}
		m.focusScroll += step
		return nil, true
	case "up", "k", "pgup":
		step := 1
		if k.String() == "pgup" {
			step = 10
		}
		m.focusScroll -= step
		if m.focusScroll < 0 {
			m.focusScroll = 0
		}
		m.focusFollow = false // reading back through the run: stop chasing the tail
		return nil, true
	case "home", "g":
		m.focusScroll = 0
		m.focusFollow = false
		return nil, true
	case "end", "G":
		m.focusScroll = 1 << 30 // central clamp pins it to the last page
		m.focusFollow = true
		return nil, true
	}
	return nil, false
}

// runViewBands draws the expanded terminal: one chrome header, then a PANE of
// exactly winH-1 filled rows showing a window onto the run's screen. stopKey is
// the key that stops or clears this run ("⌥k" for a node, "⌥r" for a chip).
//
// The pane's height never depends on how much has been printed — an empty run
// opens the same size it will have at a thousand lines, so nothing on screen
// shifts as output streams in. That is also how a terminal behaves: the window
// is the window, and the text scrolls inside it.
func runViewBands(m *Model, r *runState, hdr, stopKey, rail string, width, scroll, winH int) []string {
	out := r.lines()
	running := r.cancel != nil
	if d := r.dropCount(); d > 0 {
		hdr += fmt.Sprintf(" · %d dropped", d)
	}
	// the header is a live surface too: the line count climbs as output streams in,
	// and the shimmering "running…" with its elapsed clock says it is still going.
	switch {
	case running:
		hdr += " · " + runningTag(r) + cDim + " · " + stopKey + " stop"
	case len(out) > 0:
		hdr += " · " + stopKey + " clear"
	}
	hdr += " · esc close"
	inner := width - visibleWidth(rail) // the pane's own width, right of the rail
	head := cDim + hdr
	// where it ran, right-aligned on the header row — one line of chrome, not two
	if pad := inner - visibleWidth(head) - visibleWidth(r.pwd) - 2; r.pwd != "" && pad > 0 {
		head += strings.Repeat(" ", pad) + cDim + r.pwd
	}
	lines := []string{clip(rail+cReset+head+cReset, width)}

	paneH := max(winH-1, 1)
	// the window onto the output: follow the tail as a terminal scrolls itself,
	// until a scroll key takes over (runViewKey clears focusFollow)
	top := clampInt(scroll, 0, max(len(out)-paneH, 0))
	if m.focusFollow {
		top = max(len(out)-paneH, 0)
	}
	for i := top; i < top+paneH; i++ {
		body := ""
		switch {
		case i < len(out):
			body = "  " + styleOutLine(out[i])
		case i == 0:
			body = cDim + "  no output yet · ⌥r runs"
		}
		// the pane is the TERMINAL itself — plain terminal output on the editor's
		// own background, no tinted block behind it (bgTerm was the old chrome)
		lines = append(lines, clip(rail+cReset+body+cReset, width))
	}
	return lines
}
