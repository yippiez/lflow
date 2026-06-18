package editor

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lflow/lflow/pkg/tui/database"
	"github.com/lflow/lflow/pkg/tui/outline"
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

// workerUsage is the running token/cost total shown next to a worker node. The
// full transcript is kept separately and only shown when expanded (alt+e); the
// live activity streams on a one-line status below the node.
type workerUsage struct {
	model   string
	in, out int
	cost    float64
}

type workerUsageMsg struct {
	uuid  string
	usage workerUsage
}

// workerActivity is one streamed unit of "what the agent is doing" — a tool call
// (with a short detail) or a plain status line. They are queued and shown one at
// a time on the status line below the node.
type workerActivity struct {
	tool string // the tool name (colored), "" for a plain status
	text string // detail (file/command) or status text
}

type workerActivityMsg struct {
	uuid string
	act  workerActivity
}

// workerDeliverableMsg carries the finish_worker markdown — the harvestable
// result that Enter materializes into the notebook.
type workerDeliverableMsg struct {
	uuid     string
	markdown string
}

// toggleWorkerOutput shows/hides a worker node's full transcript (alt+e). The
// transcript is otherwise hidden — only the compact status chip shows inline.
func (m *Model) toggleWorkerOutput(it *item) {
	if m.workerExpanded == nil {
		m.workerExpanded = map[string]bool{}
	}
	m.workerExpanded[it.uuid] = !m.workerExpanded[it.uuid]
}

// sendToWorker adds a notebook node to a temp worker as context (a mirror child).
// With newWorker, or when there is no current draft worker, it creates a fresh
// worker under the temp root and makes it current; otherwise it appends to the
// current draft. The worker's task is its own text + note + these context
// children. Focus stays in the notebook.
func (m *Model) sendToWorker(it *item, newWorker bool) {
	m.ensureTempTree()
	src := m.tree.sourceUUID(it) // resolve mirror chains to the real node
	srcName := m.tree.displayName(it)

	var w *item
	if !newWorker {
		if m.currentWorker != "" {
			w = m.tempTree.byUUID[m.currentWorker]
		}
		if w == nil {
			w = m.emptyDraftWorker() // adopt the empty placeholder if present
		}
	}
	if w == nil {
		nw, err := m.tempTree.newItem() // typ defaults to worker (temp tree)
		if err != nil {
			m.err = err
			return
		}
		nw.parent = m.tempTree.root
		m.tempTree.root.children = append(m.tempTree.root.children, nw)
		w = nw
	}
	m.currentWorker = w.uuid

	child, err := m.tempTree.newItem()
	if err != nil {
		m.err = err
		return
	}
	child.typ = database.TypeBullets // a context mirror, not itself a worker
	child.mirrorOf = src
	child.collapsed = true
	child.parent = w
	w.children = append(w.children, child)
	m.tempTree.externalNames[src] = srcName // resolve the mirror's display name

	m.unsaved = true
	m.flash = "sent to worker"
}

// emptyDraftWorker returns an existing empty, unrun worker under the temp root
// (e.g. the always-present placeholder) so alt+s adopts it instead of leaving it
// orphaned beside a fresh one. nil if there is none.
func (m *Model) emptyDraftWorker() *item {
	for _, c := range m.tempTree.root.children {
		if c.typ != database.TypeWorker || strings.TrimSpace(c.name) != "" || len(c.children) > 0 {
			continue
		}
		if _, ran := m.runOut[c.uuid]; !ran {
			return c
		}
	}
	return nil
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
	// running clears the draft pointer so the next alt+s starts a fresh worker
	if it.uuid == m.currentWorker {
		m.currentWorker = ""
	}
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
	model, thinking := piModelInfo()
	go startWorker(it.uuid, task, model, thinking, ctx, ch)
	return waitBashCmd(ch)
}

// buildWorkerTask assembles the agent's prompt from the worker node's own text
// (the message), its note, and its children — mirror children resolve to their
// source node's content. Context = message + note + children.
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
				ch <- workerActivityMsg{uuid, workerActivity{tool: "finish_worker", text: "writing result"}}
				var fw struct {
					Markdown string `json:"markdown"`
				}
				if json.Unmarshal(ev.Args, &fw) == nil && fw.Markdown != "" {
					md := strings.TrimSpace(fw.Markdown)
					ch <- bashLineMsg{uuid, md, false}        // transcript (expanded view)
					ch <- workerDeliverableMsg{uuid, md}      // harvestable result (Enter)
				}
				break
			}
			// queue the live activity (colored tool name + a short detail)…
			ch <- workerActivityMsg{uuid, workerActivity{tool: ev.ToolName, text: toolDetail(ev.Args)}}
			// …and keep the raw line in the transcript (expanded view only)
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
			ch <- workerActivityMsg{uuid, workerActivity{text: "done"}}
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

// workerSuffix is the compact token/cost chip next to a worker node. The live
// activity is streamed on the status line below the node (workerBandLines), not
// here. Grounded in work2/pchain's job line.
func (m *Model) workerSuffix(it *item) string {
	u, ok := m.workerUsage[it.uuid]
	if !ok {
		return ""
	}
	return cDim + fmt.Sprintf(" ┊ ↑%s ↓%s $%.4f", ktok(u.in), ktok(u.out), u.cost) + cReset
}

// --- streaming activity queue -------------------------------------------------
//
// Each worker keeps a queue of activity updates. One is shown at a time on the
// status line below the node for a few ticks; the more the queue backs up, the
// shorter each is shown, so the stream catches up instead of falling behind.

const workerTickEvery = 150 * time.Millisecond
const workerBaseTicks = 10 // ~1.5s per item with an empty backlog

type workerTickMsg time.Time

func workerTick() tea.Cmd {
	return tea.Tick(workerTickEvery, func(t time.Time) tea.Msg { return workerTickMsg(t) })
}

// workerHoldTicks is how long the current item is held before advancing, shrinking
// as the backlog grows so a busy stream drains faster.
func workerHoldTicks(backlog int) int {
	n := workerBaseTicks / (backlog + 1)
	if n < 2 {
		n = 2
	}
	return n
}

// advanceWorkerFeeds advances each worker's displayed activity: it pops the next
// queued item once the current one has been shown long enough for the backlog.
func (m *Model) advanceWorkerFeeds() {
	for uuid, q := range m.workerQueue {
		if len(q) == 0 {
			continue
		}
		_, hasCur := m.workerCur[uuid]
		if !hasCur || m.workerCurTicks[uuid] >= workerHoldTicks(len(q)) {
			m.workerCur[uuid] = q[0]
			m.workerQueue[uuid] = q[1:]
			m.workerCurTicks[uuid] = 0
		} else {
			m.workerCurTicks[uuid]++
		}
	}
}

// anyWorkerFeedActive reports whether the activity tick should keep firing: any
// queued items, or any worker still running.
func (m *Model) anyWorkerFeedActive() bool {
	for _, q := range m.workerQueue {
		if len(q) > 0 {
			return true
		}
	}
	for uuid := range m.runCancel {
		if it := m.tree.byUUID[uuid]; it != nil && it.typ == database.TypeWorker {
			return true
		}
	}
	return false
}

// workerBandLines is the worker's hanging band: the full transcript when expanded
// (alt+e), otherwise a single streaming status line — "Starting…" then colored
// tool calls — drawn under the node.
func (m *Model) workerBandLines(r row, subtreeBelow bool, maxLine int) []string {
	uuid := r.it.uuid
	rail := continuationPrefix(r, subtreeBelow)
	_, running := m.runCancel[uuid]

	if m.workerExpanded[uuid] {
		var lines []string
		for _, l := range m.runOut[uuid] {
			col := cFG
			if l.err {
				col = cRed
			}
			lines = append(lines, clip(rail+cReset+col+"  "+l.text+cReset, maxLine))
		}
		if running {
			lines = append(lines, clip(rail+cReset+cDim+"  running…"+cReset, maxLine))
		}
		return lines
	}

	cur, hasCur := m.workerCur[uuid]
	if !running && !hasCur {
		return nil
	}
	var body string
	switch {
	case !hasCur:
		body = cDim + "Starting…" + cReset
	case cur.tool != "":
		body = toolColor(cur.tool) + toolLabel(cur.tool) + cReset
		if cur.text != "" {
			body += cDim + " " + cur.text + cReset
		}
	default:
		body = cDim + cur.text + cReset
	}
	return []string{clip(rail+cReset+"  "+body, maxLine)}
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
