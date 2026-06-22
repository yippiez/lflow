package editor

import (
	"bufio"
	"context"
	"io"
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
	go startBash(it.uuid, it.name, ctx, ch)
	return waitBashCmd(ch)
}

func waitBashCmd(ch chan tea.Msg) tea.Cmd {
	return func() tea.Msg { return <-ch }
}

// startBash spawns `bash -c <cmd>` and streams each output line onto ch.
func startBash(uuid, cmd string, ctx context.Context, ch chan tea.Msg) {
	c := exec.CommandContext(ctx, "bash", "-c", cmd)
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
