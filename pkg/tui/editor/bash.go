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

// runBash runs a bash node's command. Output is ephemeral (in-memory, keyed by
// uuid) and streams in; a second alt+r while running cancels it.
func runBash(m *Model, it *item) tea.Cmd {
	return runShell(m, it, expandAnchors(it.name, m.chips)) // chips → full paths
}

// runShell streams an arbitrary shell command through the shared run machinery
// — the bash node and every runnable genui type go through here.
func runShell(m *Model, it *item, cmd string) tea.Cmd {
	if m.runCancel == nil {
		m.runCancel = map[string]func(){}
		m.runOut = map[string][]outLine{}
		m.runCh = map[string]chan tea.Msg{}
	}
	if cancel, running := m.runCancel[it.uuid]; running {
		cancel()
		m.finishRun(it.uuid) // keep whatever was captured before the cancel
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.runCancel[it.uuid] = cancel
	if m.runDropped != nil {
		delete(m.runDropped, it.uuid) // a fresh run starts its drop count over
	}
	// a fresh run owns the band now: memory is authoritative, so don't reload the
	// old on-disk output over the incoming stream.
	if m.runOutLoaded == nil {
		m.runOutLoaded = map[string]bool{}
	}
	m.runOutLoaded[it.uuid] = true
	m.runOut[it.uuid] = nil
	ch := make(chan tea.Msg, 1024)
	m.runCh[it.uuid] = ch
	go startBash(it.uuid, cmd, ctx, ch)
	return waitBashCmd(ch)
}

func waitBashCmd(ch chan tea.Msg) tea.Cmd {
	return func() tea.Msg { return <-ch }
}

// finishRun closes out a run band — the stream ended (done, canceled, or
// stopped) — persisting what memory holds and dropping the live bookkeeping.
func (m *Model) finishRun(uuid string) {
	delete(m.runCancel, uuid)
	delete(m.runCh, uuid)
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

// bashView is a bash node's inline expanded output viewer (alt+e): the full
// captured run band, scrollable and read-only, with the program's colors
// preserved (see styleOutLine). It is stateless — the only state is the shared
// focusScroll offset, which the central render loop clamps each frame.
type bashView struct{}

func (bashView) Enter(m *Model, it *item) bool {
	m.ensureRunOutLoaded(it.uuid)
	m.focusScroll = 0
	return true // focus even when empty so the placeholder explains how to run
}

func (bashView) Leave(m *Model, it *item) {}

// Lines is a header plus one row per output line (or a placeholder when empty).
func (bashView) Lines(m *Model, it *item, width int) int {
	m.ensureRunOutLoaded(it.uuid)
	n := len(m.runOut[it.uuid])
	if n == 0 {
		n = 1 // the "no output" placeholder
	}
	return 1 + n
}

// Key scrolls the viewer; esc/ctrl+c fall through to central defocus.
func (bashView) Key(m *Model, it *item, k tea.KeyMsg) (tea.Cmd, bool) {
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
func (bashView) Bands(m *Model, it *item, rail string, width, scroll, winH int, focused bool) []string {
	m.ensureRunOutLoaded(it.uuid)
	out := m.runOut[it.uuid]
	_, running := m.runCancel[it.uuid]

	hdr := fmt.Sprintf("  %s · %d lines", it.typ, len(out))
	if d := m.runDropped[it.uuid]; d > 0 {
		hdr += fmt.Sprintf(" · %d dropped", d)
	}
	if running {
		hdr += " · running… · ⌥x stop"
	} else if len(out) > 0 {
		hdr += " · ⌥x clear"
	}
	hdr += " · ↑↓ scroll · esc close"
	content := []string{clip(rail+cReset+cDim+hdr+cReset, width)}

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
