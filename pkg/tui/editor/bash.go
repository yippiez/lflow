package editor

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// outLine is one captured line of a bash run; err marks stderr (rendered red).
type outLine struct {
	text string
	err  bool
}

// bashLinesMsg carries a BATCH of captured lines. Batching happens in the
// producer on a ~50ms window (see startBash), so a torrential command costs
// the UI at most ~20 bounded messages a second — one message per line would
// pin the update loop for the whole run and freeze the editor.
type bashLinesMsg struct {
	uuid  string
	lines []outLine
}
type bashDoneMsg struct {
	uuid string
	exit int
}

// runShell streams an arbitrary shell command through the shared run machinery
// — every runnable mod type goes through here (cmd chips mirror it in
// runCmdChip, keyed by chip id instead of node uuid).
func runShell(m *Model, it *item, cmd string) tea.Cmd {
	if r := m.run(it.uuid); r != nil && r.cancel != nil {
		r.cancel()
		m.finishRun(it.uuid) // keep whatever was captured before the cancel
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	r := m.ensureRun(it.uuid)
	r.cancel = cancel
	r.dropped = 0 // a fresh run starts its drop count over
	// a fresh run owns the band now: memory is authoritative, so don't reload the
	// old on-disk output over the incoming stream.
	r.loaded = true
	r.out = nil
	m.captureRunPWD(it.uuid)
	ch := make(chan tea.Msg, 1024)
	r.ch = ch
	go startBash(it.uuid, cmd, ctx, ch)
	return waitBashCmd(ch)
}

func waitBashCmd(ch chan tea.Msg) tea.Cmd {
	return func() tea.Msg { return <-ch }
}

// captureRunPWD remembers the working directory a run was launched from, so the
// alt+e output menu can say exactly where the command ran. startBash pins
// Cmd.Dir to this same Getwd snapshot at launch (empty → inherit for the rare
// Getwd failure), so os.Getwd is the source of truth for both the band header
// and the shell process.
func (m *Model) captureRunPWD(id string) {
	pwd, err := os.Getwd()
	if err != nil || pwd == "" {
		return
	}
	m.ensureRun(id).pwd = pwd
}

// processCWD returns the editor process working directory, or "" when unknown.
// Used at alt+r to pin command-chip and runnable-node work.
func processCWD() string {
	pwd, err := os.Getwd()
	if err != nil {
		return ""
	}
	return pwd
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
// backpressures the scanner through the channel).
const (
	runFlushEvery = 50 * time.Millisecond
	runBatchCap   = 4096
)

// startBash spawns `bash -c <cmd>` and streams its output onto ch in
// time-batched bashLinesMsg chunks.
func startBash(uuid, cmd string, ctx context.Context, ch chan tea.Msg) {
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
	// pin the shell to the process cwd at launch (same snapshot captureRunPWD
	// stored for the alt+e header). Empty Dir would inherit too; setting it
	// makes the "pwd where run" contract explicit.
	if dir := processCWD(); dir != "" {
		c.Dir = dir
	}
	// coax color out of tools that suppress it when stdout isn't a TTY; captured
	// ANSI is then rendered faithfully (see styleOutLine).
	c.Env = append(os.Environ(), "FORCE_COLOR=1", "CLICOLOR_FORCE=1", "CLICOLOR=1")
	stdout, _ := c.StdoutPipe()
	stderr, _ := c.StderrPipe()
	if err := c.Start(); err != nil {
		send(bashLinesMsg{uuid, []outLine{{text: err.Error(), err: true}}})
		send(bashDoneMsg{uuid, 1})
		return
	}

	lines := make(chan outLine, 1024)
	var wg sync.WaitGroup
	scan := func(r io.Reader, isErr bool) {
		defer wg.Done()
		sc := bufio.NewScanner(r)
		sc.Buffer(make([]byte, 64*1024), 1<<20)
		for sc.Scan() {
			select {
			case lines <- outLine{text: sc.Text(), err: isErr}:
			case <-ctx.Done():
				return // canceled — CommandContext is killing the process
			}
		}
	}
	wg.Add(2)
	go scan(stdout, false)
	go scan(stderr, true)
	go func() {
		wg.Wait()
		close(lines)
	}()

	// the batcher: collect lines, flush a bounded batch per window
	var batch []outLine
	flush := func() bool {
		if len(batch) == 0 {
			return true
		}
		ok := send(bashLinesMsg{uuid, batch})
		batch = nil
		return ok
	}
	tick := time.NewTicker(runFlushEvery)
	defer tick.Stop()
collect:
	for {
		select {
		case l, open := <-lines:
			if !open {
				break collect
			}
			batch = append(batch, l)
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

	exit := 0
	if err := c.Wait(); err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			exit = ee.ExitCode()
		} else {
			exit = 1
		}
	}
	send(bashDoneMsg{uuid, exit})
}

// runOutView is the generic inline run-output viewer (alt+e) for runnable
// node types (mods with a `run` hook): the full captured band, scrollable and
// read-only, with the program's colors preserved (see styleOutLine). It is
// stateless — the only state is the shared focusScroll offset, which the
// central render loop clamps each frame.
type runOutView struct{}

func (runOutView) Enter(m *Model, it *item) bool {
	m.ensureRunOutLoaded(it.uuid)
	m.focusScroll = 0
	return true // focus even when empty so the placeholder explains how to run
}

func (runOutView) Leave(m *Model, it *item) {}

// Lines is a header plus optional pwd row plus one row per output line (or a
// placeholder when empty).
func (runOutView) Lines(m *Model, it *item, width int) int {
	m.ensureRunOutLoaded(it.uuid)
	r := m.run(it.uuid) // non-nil after ensureRunOutLoaded
	n := len(r.out)
	if n == 0 {
		n = 1 // the "no output" placeholder
	}
	if r.pwd != "" {
		n++
	}
	return 1 + n
}

// Key scrolls the viewer; esc/ctrl+c fall through to central defocus.
func (runOutView) Key(m *Model, it *item, k tea.KeyMsg) (tea.Cmd, bool) {
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
		return nil, true
	case "home", "g":
		m.focusScroll = 0
		return nil, true
	case "end", "G":
		m.focusScroll = 1 << 30 // central clamp pins it to the last page
		return nil, true
	}
	return nil, false
}

// Bands renders the header and output lines, self-windowed to [scroll, scroll+winH).
func (runOutView) Bands(m *Model, it *item, rail string, width, scroll, winH int, focused bool) []string {
	m.ensureRunOutLoaded(it.uuid)
	r := m.run(it.uuid) // non-nil after ensureRunOutLoaded
	out := r.out
	running := r.cancel != nil

	hdr := fmt.Sprintf("  %s · %d lines", it.typ, len(out))
	if d := r.dropped; d > 0 {
		hdr += fmt.Sprintf(" · %d dropped", d)
	}
	if running {
		hdr += " · running… · ⌥x stop"
	} else if len(out) > 0 {
		hdr += " · ⌥x clear"
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
