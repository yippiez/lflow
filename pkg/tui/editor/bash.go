package editor

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"

	tea "github.com/charmbracelet/bubbletea"
)

// outLine is one captured line of a bash run; err marks stderr (rendered red).
type outLine struct {
	text string
	err  bool
}

type bashLineMsg struct {
	uuid string
	text string
	err  bool
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
// — the bash node and every runnable artifact type go through here.
func runShell(m *Model, it *item, cmd string) tea.Cmd {
	if m.runCancel == nil {
		m.runCancel = map[string]func(){}
		m.runOut = map[string][]outLine{}
		m.runCh = map[string]chan tea.Msg{}
	}
	if cancel, running := m.runCancel[it.uuid]; running {
		cancel()
		delete(m.runCancel, it.uuid)
		m.persistRunOut(it.uuid) // keep whatever was captured before the cancel
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.runCancel[it.uuid] = cancel
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

// startBash spawns `bash -c <cmd>` and streams each output line onto ch.
func startBash(uuid, cmd string, ctx context.Context, ch chan tea.Msg) {
	c := exec.CommandContext(ctx, "bash", "-c", cmd)
	// coax color out of tools that suppress it when stdout isn't a TTY; captured
	// ANSI is then rendered faithfully (see styleOutLine).
	c.Env = append(os.Environ(), "FORCE_COLOR=1", "CLICOLOR_FORCE=1", "CLICOLOR=1")
	stdout, _ := c.StdoutPipe()
	stderr, _ := c.StderrPipe()
	if err := c.Start(); err != nil {
		ch <- bashLineMsg{uuid, err.Error(), true}
		ch <- bashDoneMsg{uuid, 1}
		return
	}
	var wg sync.WaitGroup
	scan := func(r io.Reader, isErr bool) {
		defer wg.Done()
		sc := bufio.NewScanner(r)
		sc.Buffer(make([]byte, 64*1024), 1<<20)
		for sc.Scan() {
			ch <- bashLineMsg{uuid, sc.Text(), isErr}
		}
	}
	wg.Add(2)
	go scan(stdout, false)
	go scan(stderr, true)
	wg.Wait()
	exit := 0
	if err := c.Wait(); err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			exit = ee.ExitCode()
		} else {
			exit = 1
		}
	}
	ch <- bashDoneMsg{uuid, exit}
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
	if running {
		hdr += " · running…"
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
