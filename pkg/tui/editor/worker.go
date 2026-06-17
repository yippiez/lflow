package editor

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// A worker node: its name is a message to the Pi coding agent. alt+r runs a turn
// (pi in RPC mode); the agent's activity (tool calls + messages) streams into the
// output band. Reuses the bash run infra (runCancel/runCh/runOut + bashLineMsg).
//
// Grounded in work2/pchain/pi/src/agents/manager.ts: spawn pi --mode rpc, send a
// {type:"prompt"} command on stdin, read newline-JSON events on stdout.

// piEvent is one RPC event line from pi's stdout.
type piEvent struct {
	Type     string          `json:"type"`
	Message  *piMessage      `json:"message"`
	ToolName string          `json:"toolName"`
	Args     json.RawMessage `json:"args"`
}

type piMessage struct {
	Role    string `json:"role"`
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
}

func (msg *piMessage) text() string {
	if msg == nil {
		return ""
	}
	var b strings.Builder
	for _, c := range msg.Content {
		if c.Type == "text" {
			b.WriteString(c.Text)
		}
	}
	return strings.TrimSpace(b.String())
}

func runWorker(m *Model, it *item) tea.Cmd {
	if m.runCancel == nil {
		m.runCancel = map[string]func(){}
		m.runOut = map[string][]outLine{}
		m.runCh = map[string]chan tea.Msg{}
	}
	if cancel, running := m.runCancel[it.uuid]; running {
		cancel()
		delete(m.runCancel, it.uuid)
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.runCancel[it.uuid] = cancel
	m.runOut[it.uuid] = nil
	ch := make(chan tea.Msg, 1024)
	m.runCh[it.uuid] = ch
	go startWorker(it.uuid, it.name, ctx, ch)
	return waitBashCmd(ch)
}

// startWorker spawns pi in RPC mode, sends the prompt, and translates pi's event
// stream into output-band lines.
func startWorker(uuid, message string, ctx context.Context, ch chan tea.Msg) {
	c := exec.CommandContext(ctx, "pi", "--mode", "rpc", "--no-session", "--approve",
		"--tools", "read,bash,grep,find,ls")
	stdin, _ := c.StdinPipe()
	stdout, _ := c.StdoutPipe()
	if err := c.Start(); err != nil {
		ch <- bashLineMsg{uuid, "pi: " + err.Error(), true}
		ch <- bashDoneMsg{uuid, 1}
		return
	}
	// send the prompt command (one JSON line)
	msgJSON, _ := json.Marshal(message)
	io.WriteString(stdin, fmt.Sprintf(`{"id":"1","type":"prompt","message":%s}`+"\n", msgJSON))

	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 64*1024), 1<<20)
	for sc.Scan() {
		var ev piEvent
		if json.Unmarshal(sc.Bytes(), &ev) != nil {
			continue
		}
		switch ev.Type {
		case "tool_execution_start":
			line := "→ " + ev.ToolName
			if s := clipStr(string(ev.Args), 50); s != "" && s != "{}" {
				line += " " + s
			}
			ch <- bashLineMsg{uuid, line, false}
		case "message_end":
			if ev.Message != nil && ev.Message.Role == "assistant" {
				if txt := ev.Message.text(); txt != "" {
					ch <- bashLineMsg{uuid, txt, false}
				}
			}
		case "agent_end":
			stdin.Close()
			_ = c.Wait()
			ch <- bashDoneMsg{uuid, 0}
			return
		}
	}
	stdin.Close()
	_ = c.Wait()
	ch <- bashDoneMsg{uuid, 0}
}

func clipStr(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
