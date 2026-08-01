package editor

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/creack/pty"
)

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
// one async producer is startBash's PTY-launch-failure path; the type stays the
// seam for any future line producer that streams instead of writing r.out.
type bashLinesMsg struct {
	uuid  string
	lines []outLine
}

// bashBytesMsg carries a BATCH of raw output bytes from a shell run, straight
// from the PTY into the run's terminal screen. Batching happens in the producer
// on a ~50ms window (see startBash), so a torrential command costs the UI at most
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

// startShellRun is the one launch path for a shell run, keyed by node uuid or cmd
// chip id: a fresh terminal screen, a PTY sized to the editor's body, and the
// stream pumping into the update loop.
func (m *Model) startShellRun(id, cmd string) tea.Cmd {
	ctx, cancel := context.WithCancel(context.Background())
	r := m.ensureRun(id)
	r.cancel = cancel
	r.started = time.Now() // the elapsed clock the band shows while it runs
	r.dropped = 0          // a fresh run starts its drop count over
	// a fresh run owns the band now: memory is authoritative, so don't reload the
	// old on-disk output over the incoming stream.
	r.loaded = true
	r.out = nil
	r.scr = newTermScreen(m.runCols(), termRows)
	dir := m.captureRunPWD(id)
	ch := make(chan tea.Msg, 1024)
	r.ch = ch
	go startBash(id, cmd, dir, ctx, ch, r.scr.w, r.scr.h)
	return waitBashCmd(ch)
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

// finishRun closes out a run band — the stream ended (done, canceled, or
// stopped) — persisting what memory holds and dropping the live bookkeeping.
func (m *Model) finishRun(uuid string) {
	if r := m.run(uuid); r != nil {
		r.cancel = nil // no longer running; the band (out) is kept
		r.ch = nil
	}
	m.persistRunOut(uuid)
	m.setCmdPreview(uuid)
}

// Producer-side batching knobs: a batch ships when the flush window elapses
// (so a trickle still appears promptly) or when it hits the size cap (so a
// torrent cannot grow an unbounded transient batch — the capped send then
// backpressures the reader through the channel).
const (
	runFlushEvery = 50 * time.Millisecond
	runBatchCap   = 64 << 10
)

// startBash spawns `bash -c <cmd>` ON A PTY and streams its raw output onto ch in
// time-batched bashBytesMsg chunks, for the run's terminal screen to interpret.
//
// The PTY is the point: with a pipe, a program sees "not a terminal" and changes
// what it prints — no color unless coaxed, no progress bars, no cursor work — so
// the band could never look like the terminal. On a PTY the command behaves
// exactly as it would in one, and everything it emits (CR overwrites, clears,
// cursor moves) means what it means.
func startBash(uuid, cmd, dir string, ctx context.Context, ch chan tea.Msg, cols, rows int) {
	// send delivers onto ch unless the run was canceled — the editor stops
	// draining after a cancel, so an unconditional send on a full buffer would
	// strand this goroutine forever.
	send := func(msg tea.Msg) bool {
		select {
		case ch <- msg:
			return true
		case <-ctx.Done():
			return false
		}
	}
	// closing wakes any reader still blocked in waitBashCmd; the resulting nil
	// msg is ignored by bubbletea
	defer close(ch)
	c := exec.CommandContext(ctx, "bash", "-c", cmd)
	// pin the shell to the launch cwd — the very snapshot captureRunPWD stored
	// for the alt+e header ("" inherits).
	c.Dir = dir
	// TERM is what the program keys its capabilities off; the size is passed on
	// the PTY itself, so COLUMNS/LINES need no faking.
	c.Env = append(os.Environ(), "TERM=xterm-256color", "COLORTERM=truecolor")
	ptmx, err := pty.StartWithSize(c, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)})
	if err != nil {
		send(bashLinesMsg{uuid, []outLine{{text: err.Error(), err: true}}})
		send(bashDoneMsg{uuid})
		return
	}
	// closing the master is what makes the reader below return once the child is
	// gone (and what unblocks it on a cancel).
	defer ptmx.Close()
	go func() {
		<-ctx.Done()
		ptmx.Close()
	}()
	// a canceled run leaves this function before the Wait at the bottom, so reap
	// the killed child here or it lingers as a zombie for the editor's lifetime.
	waited := false
	defer func() {
		if !waited {
			_ = c.Wait()
		}
	}()

	chunks := make(chan []byte, 64)
	go func() {
		defer close(chunks)
		buf := make([]byte, 32<<10)
		for {
			n, err := ptmx.Read(buf)
			if n > 0 {
				b := make([]byte, n)
				copy(b, buf[:n])
				select {
				case chunks <- b:
				case <-ctx.Done():
					return
				}
			}
			if err != nil {
				return // EIO on child exit, or the close above
			}
		}
	}()

	// the batcher: coalesce reads, flush a bounded batch per window
	var batch []byte
	flush := func() bool {
		if len(batch) == 0 {
			return true
		}
		ok := send(bashBytesMsg{uuid, batch})
		batch = nil
		return ok
	}
	tick := time.NewTicker(runFlushEvery)
	defer tick.Stop()
collect:
	for {
		select {
		case b, open := <-chunks:
			if !open {
				break collect
			}
			batch = append(batch, b...)
			if len(batch) >= runBatchCap && !flush() {
				return
			}
		case <-tick.C:
			if !flush() {
				return
			}
		case <-ctx.Done():
			return
		}
	}
	flush()

	waited = true
	_ = c.Wait()
	send(bashDoneMsg{uuid})
}

// runOutView is the generic inline run-output viewer (alt+e) for runnable
// node types (mods with a `run` hook): the full captured band, scrollable and
// read-only, with the program's colors preserved (see styleOutLine). It is
// stateless — the only state is the shared focusScroll offset, which the
// central render loop clamps each frame.
type runOutView struct{}

func (runOutView) Enter(m *Model, it *item) bool {
	m.ensureRunOutLoaded(it.uuid)
	m.focusScroll, m.focusFollow = 0, true
	return true // focus even when empty so the placeholder explains how to run
}

func (runOutView) Leave(m *Model, it *item) { m.focusFollow = false }

func (runOutView) Lines(m *Model, it *item, width int) int {
	return runViewLines(m, it.uuid)
}

// Key scrolls the viewer; esc/ctrl+c fall through to central defocus.
func (runOutView) Key(m *Model, it *item, k tea.KeyMsg) (tea.Cmd, bool) {
	return runViewKey(m, k)
}

// Bands renders the header and output lines, self-windowed to [scroll, scroll+winH).
func (runOutView) Bands(m *Model, it *item, rail string, width, scroll, winH int, focused bool) []string {
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

// runViewLines is the height both expanded run views report: a header, an
// optional pwd row, and one row per output line (or the "no output" placeholder).
func runViewLines(m *Model, id string) int {
	m.ensureRunOutLoaded(id)
	r := m.run(id) // non-nil after ensureRunOutLoaded
	n := max(len(r.lines()), 1)
	if r.pwd != "" {
		n++
	}
	return 1 + n
}

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

// runViewBands builds the expanded band: the header (hdr, already prefixed with
// two spaces) plus its live/idle tail, the pwd row, and the run's screen. stopKey
// is the key that stops or clears this run ("⌥k" for a node, "⌥r" for a chip).
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
	hdr += " · ↑↓ scroll · esc close"
	content := []string{clip(rail+cReset+cDim+hdr+cReset, width)}
	if pwd := r.pwd; pwd != "" {
		content = append(content, clip(rail+cReset+cDim+"  pwd: "+pwd+cReset, width))
	}
	if len(out) == 0 {
		content = append(content, clip(rail+cReset+cDim+"  no output yet · ⌥r runs"+cReset, width))
	}
	for _, l := range out {
		content = append(content, clip(rail+cReset+"  "+styleOutLine(l), width))
	}

	// follow the tail while the view is chasing output, exactly as a terminal
	// scrolls itself; the manual scroll keys take over the moment you scroll up.
	if m.focusFollow && len(content) > winH {
		scroll = len(content) - winH
	}
	if scroll < 0 {
		scroll = 0
	}
	if scroll > len(content) {
		scroll = len(content)
	}
	end := scroll + winH
	if end > len(content) {
		end = len(content)
	}
	return content[scroll:end]
}
