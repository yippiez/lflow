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

	"github.com/lflow/lflow/pkg/tui/database"
	"github.com/lflow/lflow/pkg/tui/outline"
)

// A worker node: an agent doing a task for the outline. It is shown on a single
// minimal line (status · usage · live activity); the full transcript and a
// steering box live in the agent UI (alt+e). A worker's pi process stays alive
// across turns so follow-up messages (alt+r on a notebook node, or the agent
// UI's input box) steer the same conversation instead of starting over.
//
// Grounded in work2/pchain/pi/src/agents/manager.ts.

const workerSystemPrompt = "You are a worker doing a task for an outline. Do the work " +
	"with your tools, then call finish_worker exactly once with the deliverable as " +
	"outline nodes (the answer itself, not a recap of steps). The parent already sees " +
	"your tool calls — never narrate your process. Return a SINGLE node unless the user " +
	"explicitly asks for a list, steps, or an outline. Plain text only — never markdown, " +
	"bullets, or headings; express nesting with child nodes. After finish_worker, your " +
	"assistant text must be exactly: WORKER_DONE."

// piEvent is one RPC event line from pi's stdout.
type piEvent struct {
	Type          string          `json:"type"`
	Message       *piMessage      `json:"message"`
	ToolName      string          `json:"toolName"`
	Args          json.RawMessage `json:"args"`
	PartialResult json.RawMessage `json:"partialResult"`
	Error         string          `json:"error"`
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

// workerActivity is the single live "what the agent is doing" line — a tool call
// (colored name + short detail) or a plain status. Replaced on each pi event.
type workerActivity struct {
	tool string // the tool name (colored), "" for a plain status
	text string // detail (file/command/tail) or status text
}

type workerActivityMsg struct {
	uuid   string
	act    workerActivity
	status string // "" leaves the status unchanged
	start  bool   // a tool_execution_start — append to the tool-call history
}

// workerDeliverableMsg carries the finish_worker outline (nodes JSON) — the
// harvestable result Enter materializes into the notebook.
type workerDeliverableMsg struct {
	uuid  string
	nodes string
}

// --- launch (notes → Agent Domain) -------------------------------------------

// launchAgentFromNote creates an agent in the Agent Domain from a notes node and
// RUNS it immediately (alt+s): the node's text is the query, and its children
// become the agent's context (as mirrors). Focus stays in notes.
func (m *Model) launchAgentFromNote(note *item) tea.Cmd {
	m.ensureTempTree()
	w, err := m.tempTree.newItem() // typ defaults to worker (temp tree)
	if err != nil {
		m.err = err
		return nil
	}
	w.name = m.tree.displayName(note) // the query is the agent's name
	w.parent = m.tempTree.root
	// add after the compose line so the empty compose stays first
	m.tempTree.root.children = append(m.tempTree.root.children, w)

	for _, c := range note.children {
		src := m.tree.sourceUUID(c)
		child, err := m.tempTree.newItem()
		if err != nil {
			continue
		}
		child.typ = database.TypeBullets
		child.mirrorOf = src
		child.collapsed = true
		child.parent = w
		w.children = append(w.children, child)
		m.tempTree.externalNames[src] = m.tree.displayName(c)
	}
	m.lastAgent = w.uuid
	m.unsaved = true
	m.flash = "launched agent"
	return runWorker(m, w)
}

// workerRan reports whether a worker has been launched (is running, or has a
// recorded status). Such a worker's title is locked — 's' steers it instead.
func (m *Model) workerRan(it *item) bool {
	if it == nil || it.typ != database.TypeWorker {
		return false
	}
	if _, running := m.runCancel[it.uuid]; running {
		return true
	}
	return m.workerStatus[it.uuid] != ""
}

// --- running ------------------------------------------------------------------

func runWorker(m *Model, it *item) tea.Cmd {
	if m.runCancel == nil {
		m.runCancel = map[string]func(){}
		m.runOut = map[string][]outLine{}
		m.runCh = map[string]chan tea.Msg{}
	}
	if m.workerSteer == nil {
		m.workerSteer = map[string]chan string{}
	}
	// already live → stop it (cancel kills the pi process)
	if cancel, running := m.runCancel[it.uuid]; running {
		cancel()
		delete(m.runCancel, it.uuid)
		return nil
	}
	m.lastAgent = it.uuid
	// persist first so the context (mirror sources + this worker's subtree) is in
	// the DB for buildWorkerTask to read
	if _, err := m.saveAll(); err == nil {
		m.unsaved = false
	}
	task := m.buildWorkerTask(it)

	ctx, cancel := context.WithCancel(context.Background())
	m.runCancel[it.uuid] = cancel
	m.runOut[it.uuid] = nil
	ch := make(chan tea.Msg, 1024)
	m.runCh[it.uuid] = ch
	steer := make(chan string, 16)
	m.workerSteer[it.uuid] = steer
	if m.workerStatus == nil {
		m.workerStatus = map[string]string{}
	}
	m.workerStatus[it.uuid] = "running"
	model, thinking := piModelInfo()
	go startWorker(it.uuid, task, model, thinking, ctx, ch, steer)
	return waitBashCmd(ch)
}

// buildWorkerTask assembles the agent's first prompt from the worker node's own
// text (the message), its note, and its children — mirror children resolve to
// their source node's content. Context = message + note + children.
func (m *Model) buildWorkerTask(it *item) string {
	var b strings.Builder
	b.WriteString(it.name)
	if note := strings.TrimSpace(it.note); note != "" {
		b.WriteString("\n\n" + note)
	}

	var parts []string
	for _, c := range it.children {
		uuid := c.uuid
		if c.mirrorOf != "" {
			uuid = m.tempTree.sourceUUID(c)
		}
		n, err := database.GetNode(m.db, uuid)
		if err != nil || n.Name == "" {
			continue
		}
		part := n.Name
		if md, err := outline.RenderMarkdown(m.db, n, 0, true); err == nil && strings.TrimSpace(md) != "" {
			part += "\n" + md
		}
		parts = append(parts, part)
	}
	if len(parts) > 0 {
		b.WriteString("\n\n## Context\n\n" + strings.Join(parts, "\n\n"))
	}
	return b.String()
}

// sendPrompt writes one RPC prompt line to pi's stdin.
func sendPrompt(stdin io.Writer, msg string) {
	if strings.TrimSpace(msg) == "" {
		return
	}
	j, _ := json.Marshal(msg)
	io.WriteString(stdin, fmt.Sprintf(`{"id":"1","type":"prompt","message":%s}`+"\n", j))
}

// startWorker spawns pi in RPC mode (no extensions + lflow's finish tool), sends
// the task, then keeps the process alive: a steering goroutine forwards follow-up
// messages, and the scanner translates pi's event stream into transcript lines,
// usage, and a live activity line until the process exits.
func startWorker(uuid, task, model, thinking string, ctx context.Context, ch chan tea.Msg, steer chan string) {
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
	stderr, _ := c.StderrPipe()
	if err := c.Start(); err != nil {
		ch <- bashLineMsg{uuid, "pi: " + err.Error(), true}
		ch <- workerActivityMsg{uuid, workerActivity{text: "failed: " + err.Error()}, "error", false}
		ch <- bashDoneMsg{uuid, 1}
		return
	}
	sendPrompt(stdin, task)

	// forward steering messages until the conversation is stopped (ctx cancelled);
	// select on ctx.Done so this goroutine never leaks waiting on an idle worker.
	go func() {
		for {
			select {
			case msg, ok := <-steer:
				if !ok {
					stdin.Close()
					return
				}
				sendPrompt(stdin, msg)
			case <-ctx.Done():
				stdin.Close()
				return
			}
		}
	}()

	// collect stderr — pi errors (rate limits, crashes, bad config) land here and
	// were previously dropped, leaving the worker stuck "idle" when it had failed.
	stderrCh := make(chan string, 1)
	go func() {
		var b strings.Builder
		s := bufio.NewScanner(stderr)
		s.Buffer(make([]byte, 64*1024), 1<<20)
		for s.Scan() {
			if line := strings.TrimSpace(s.Text()); line != "" {
				b.WriteString(line + "\n")
				ch <- bashLineMsg{uuid, "stderr: " + line, true}
			}
		}
		stderrCh <- b.String()
	}()

	var use workerUsage
	sawError := false
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
				ch <- workerActivityMsg{uuid, workerActivity{tool: "finish_worker", text: "writing result"}, "running", true}
				// the deliverable is an outline (nodes), never markdown — carry the
				// nodes JSON verbatim for the model side to materialize directly
				var fw struct {
					Nodes json.RawMessage `json:"nodes"`
				}
				if json.Unmarshal(ev.Args, &fw) == nil && len(fw.Nodes) > 0 {
					ch <- workerDeliverableMsg{uuid, string(fw.Nodes)}
				}
				break
			}
			ch <- workerActivityMsg{uuid, workerActivity{tool: ev.ToolName, text: toolDetail(ev.Args)}, "running", true}
		case "tool_execution_update":
			// stream the tail of the tool's live output onto the activity line
			detail := toolDetail(ev.Args)
			if tail := resultTail(ev.PartialResult); tail != "" {
				if detail != "" {
					detail += " · " + tail
				} else {
					detail = tail
				}
			}
			ch <- workerActivityMsg{uuid, workerActivity{tool: ev.ToolName, text: detail}, "running", false}
		case "message_end":
			if ev.Message == nil {
				break
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
			// a turn finished; keep the process alive for follow-up steering
			st := "idle"
			if sawError {
				st = "error"
			}
			ch <- workerActivityMsg{uuid, workerActivity{text: st}, st, false}
		default:
			// any error-shaped event (error / agent_error / model_error …) — surface
			// it instead of silently leaving the worker idle
			if strings.Contains(ev.Type, "error") {
				sawError = true
				msg := ev.Error
				if msg == "" && ev.Message != nil {
					msg = ev.Message.text()
				}
				if msg == "" {
					msg = ev.Type
				}
				ch <- workerActivityMsg{uuid, workerActivity{text: "error: " + clipStr(msg, 60)}, "error", false}
				ch <- bashLineMsg{uuid, "error: " + msg, true}
			}
		}
	}
	err := c.Wait()
	stderrText := <-stderrCh
	code := 0
	if err != nil || sawError {
		code = 1
		last := lastLine(stderrText)
		if last == "" {
			last = "pi exited unexpectedly"
		}
		if !sawError { // an exit error we hadn't already reported
			ch <- workerActivityMsg{uuid, workerActivity{text: "error: " + clipStr(last, 60)}, "error", false}
		}
	}
	ch <- bashDoneMsg{uuid, code}
}

// lastLine returns the last non-empty line of s.
func lastLine(s string) string {
	var last string
	for _, ln := range strings.Split(s, "\n") {
		if t := strings.TrimSpace(ln); t != "" {
			last = t
		}
	}
	return last
}

// resultTail extracts the last non-empty line of a tool's partial result (the
// live output pchain appends after " · ").
func resultTail(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) != nil {
		var obj map[string]any
		if json.Unmarshal(raw, &obj) != nil {
			return ""
		}
		for _, k := range []string{"value", "text", "output", "content", "stdout"} {
			if v, ok := obj[k].(string); ok {
				s = v
				break
			}
		}
	}
	var last string
	for _, ln := range strings.Split(s, "\n") {
		if t := strings.TrimSpace(ln); t != "" {
			last = t
		}
	}
	return clipStr(last, 48)
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

// --- single-line render -------------------------------------------------------

// workerSuffix is the worker's whole status, rendered on its own node line:
// status · ↑in ↓out $cost · live activity. Minimal — the transcript and steering
// live in the agent UI (alt+e). Grounded in work2/pchain's job line.
func (m *Model) workerSuffix(it *item) string {
	status := m.workerStatus[it.uuid]
	_, running := m.runCancel[it.uuid]
	u, hasUsage := m.workerUsage[it.uuid]
	act, hasAct := m.workerAction[it.uuid]

	if status == "" && !hasUsage && !hasAct && !running {
		return "" // never run — a plain draft worker
	}

	var b strings.Builder
	b.WriteString(cDim + " · " + cReset)
	b.WriteString(statusColor(status) + statusWord(status, running) + cReset)
	if hasUsage {
		b.WriteString(cDim + fmt.Sprintf(" ↑%s ↓%s $%.4f", ktok(u.in), ktok(u.out), u.cost) + cReset)
	}
	if hasAct {
		b.WriteString(cDim + " · " + cReset)
		if act.tool != "" {
			b.WriteString(toolColor(act.tool) + toolLabel(act.tool) + cReset)
			if act.text != "" {
				b.WriteString(cDim + " " + act.text + cReset)
			}
		} else if act.text != "" {
			b.WriteString(cDim + act.text + cReset)
		}
	}
	return b.String()
}

func statusWord(status string, running bool) string {
	if status != "" {
		return status
	}
	if running {
		return "running"
	}
	return "idle"
}

func statusColor(status string) string {
	switch status {
	case "running":
		return cYellow
	case "done":
		return cGreen
	case "error":
		return cRed
	default:
		return cDim
	}
}

// toolLabel title-cases a pi tool name for the status line (Read, Bash, Edit…).
func toolLabel(tool string) string {
	switch strings.ToLower(tool) {
	case "finish_worker":
		return "Finish"
	case "":
		return ""
	default:
		t := strings.ToLower(tool)
		return strings.ToUpper(t[:1]) + t[1:]
	}
}

// toolColor maps a pi tool to a palette color, like pchain's colored tool names.
func toolColor(tool string) string {
	switch strings.ToLower(tool) {
	case "read":
		return cAccent
	case "write", "edit":
		return cGreen
	case "bash":
		return cMagenta
	case "grep", "find", "ls", "glob", "search":
		return cCyan
	case "finish_worker":
		return cYellow
	default:
		return cFG
	}
}

// toolDetail pulls a short, human detail (file or command) out of a tool's args.
func toolDetail(args json.RawMessage) string {
	var m map[string]any
	if json.Unmarshal(args, &m) != nil {
		return ""
	}
	for _, k := range []string{"path", "file", "file_path", "filename", "command", "cmd", "pattern", "query", "url"} {
		if v, ok := m[k].(string); ok && v != "" {
			return clipStr(v, 48)
		}
	}
	return ""
}
