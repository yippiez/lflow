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

// A worker node: its name is a task for the Pi coding agent. alt+r runs a turn.
// The worker WORKS (uses tools) — it does not just chat — and its token/cost usage
// is shown next to the node. pi is launched with NO extensions, then lflow's own
// finish-tool extension is loaded.
//
// Grounded in work2/pchain/pi/src/agents/manager.ts.

const workerSystemPrompt = "You are a worker doing a task for an outline. Do the work " +
	"with your tools, then call finish_worker exactly once with the deliverable in " +
	"markdown (the answer itself, not a recap of steps). After finish_worker, your " +
	"assistant text must be exactly: WORKER_DONE."

// piEvent is one RPC event line from pi's stdout.
type piEvent struct {
	Type     string          `json:"type"`
	Message  *piMessage      `json:"message"`
	ToolName string          `json:"toolName"`
	Args     json.RawMessage `json:"args"`
}

type piMessage struct {
	Role     string `json:"role"`
	Provider string `json:"provider"`
	Model    string `json:"model"`
	Content  []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	Usage *piUsage `json:"usage"`
}

type piUsage struct {
	Input  int `json:"input"`
	Output int `json:"output"`
	Cost   *struct {
		Total float64 `json:"total"`
	} `json:"cost"`
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

// workerUsage is the running token/cost total shown next to a worker node.
type workerUsage struct {
	model   string
	in, out int
	cost    float64
}

type workerUsageMsg struct {
	uuid  string
	usage workerUsage
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
	model, thinking := piModelInfo()
	go startWorker(it.uuid, it.name, model, thinking, ctx, ch)
	return waitBashCmd(ch)
}

// startWorker spawns pi in RPC mode (no extensions + lflow's finish tool), sends
// the task, and translates pi's event stream into output-band lines + usage.
func startWorker(uuid, task, model, thinking string, ctx context.Context, ch chan tea.Msg) {
	args := []string{"--mode", "rpc", "--no-session", "--approve", "--no-extensions",
		"--append-system-prompt", workerSystemPrompt,
		"--tools", "read,bash,grep,find,ls,edit,write,finish_worker"}
	if ext := workerExtensionPath(); ext != "" {
		args = append(args, "--extension", ext)
	}
	if model != "" {
		args = append(args, "--model", model)
	}
	if thinking != "" {
		args = append(args, "--thinking", thinking)
	}
	c := exec.CommandContext(ctx, "pi", args...)
	stdin, _ := c.StdinPipe()
	stdout, _ := c.StdoutPipe()
	if err := c.Start(); err != nil {
		ch <- bashLineMsg{uuid, "pi: " + err.Error(), true}
		ch <- bashDoneMsg{uuid, 1}
		return
	}
	msgJSON, _ := json.Marshal(task)
	io.WriteString(stdin, fmt.Sprintf(`{"id":"1","type":"prompt","message":%s}`+"\n", msgJSON))

	var use workerUsage
	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 64*1024), 1<<20)
	for sc.Scan() {
		var ev piEvent
		if json.Unmarshal(sc.Bytes(), &ev) != nil {
			continue
		}
		switch ev.Type {
		case "tool_execution_start":
			if ev.ToolName == "finish_worker" {
				var fw struct {
					Markdown string `json:"markdown"`
				}
				if json.Unmarshal(ev.Args, &fw) == nil && fw.Markdown != "" {
					ch <- bashLineMsg{uuid, strings.TrimSpace(fw.Markdown), false} // the deliverable
				}
				break
			}
			line := "→ " + ev.ToolName
			if s := clipStr(string(ev.Args), 50); s != "" && s != "{}" {
				line += " " + s
			}
			ch <- bashLineMsg{uuid, line, false}
		case "message_end":
			if ev.Message == nil {
				break
			}
			if ev.Message.Role == "assistant" {
				if txt := ev.Message.text(); txt != "" {
					ch <- bashLineMsg{uuid, txt, false}
				}
			}
			if u := ev.Message.Usage; u != nil {
				use.in += u.Input
				use.out += u.Output
				if u.Cost != nil {
					use.cost += u.Cost.Total
				}
				if ev.Message.Model != "" {
					use.model = ev.Message.Provider + "/" + ev.Message.Model
				}
				ch <- workerUsageMsg{uuid, use}
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

func ktok(n int) string {
	if n < 1000 {
		return fmt.Sprintf("%d", n)
	}
	return fmt.Sprintf("%.1fk", float64(n)/1000)
}

// workerSuffix is the cost/token chip shown next to a worker node.
func (m *Model) workerSuffix(it *item) string {
	u, ok := m.workerUsage[it.uuid]
	if !ok {
		return ""
	}
	s := cDim + " ┊ "
	if u.model != "" {
		s += u.model + " · "
	}
	s += fmt.Sprintf("↑%s ↓%s $%.4f", ktok(u.in), ktok(u.out), u.cost) + cReset
	return s
}
