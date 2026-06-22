package editor

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lflow/lflow/pkg/agent"
	"github.com/lflow/lflow/pkg/tui/database"
	"github.com/lflow/lflow/pkg/tui/outline"
)

//go:embed pi/worker_finish.ts
var workerFinishTS string

// workerExtensionPath writes lflow's finish_worker pi extension to ~/.lflow/pi/
// (creating it if needed) and returns its path, for `pi --extension`. The pi
// backend passes it through RunOptions.Extensions.
func workerExtensionPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	dir := filepath.Join(home, ".lflow", "pi")
	if os.MkdirAll(dir, 0o755) != nil {
		return ""
	}
	path := filepath.Join(dir, "worker_finish.ts")
	if cur, _ := os.ReadFile(path); string(cur) != workerFinishTS {
		if os.WriteFile(path, []byte(workerFinishTS), 0o644) != nil {
			return ""
		}
	}
	return path
}

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

// workerTextInstruction frames the task for backends without the finish_worker
// extension (opencode / grok): they answer in plain assistant text, which the
// editor harvests verbatim as the deliverable. Kept terse and prepended to the task.
const workerTextInstruction = "You are a worker doing a task for an outline. Do the work with " +
	"your tools, then reply with ONLY the deliverable — the answer itself, not a recap of your " +
	"steps. Plain text, no markdown, bullets, or headings. Keep it concise unless the task asks " +
	"for a list. Task:"

// workerUsage is the running token/cost total shown next to a worker node.
type workerUsage struct {
	model     string
	in, out   int
	cost      float64
	estimated bool // cost is an estimate (grok) — rendered with a ~ prefix
}

// costStr formats a worker's cost, marking estimates (grok, which reports no cost)
// with a leading ~ to distinguish them from pi/opencode's CLI-reported cost.
func costStr(u workerUsage) string {
	if u.estimated {
		return fmt.Sprintf("~$%.4f", u.cost)
	}
	return fmt.Sprintf("$%.4f", u.cost)
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
// RUNS it immediately: the node's text is the query, its children become context
// (deep-copied so the agent owns its inputs). The original note is removed from the
// notes — use /bring to pull the agent (and its output) back into the notes later.
// Focus stays in notes.
func (m *Model) launchAgentFromNote(note *item) tea.Cmd {
	m.ensureTempTree()
	w, err := m.tempTree.newItem() // typ defaults to worker (temp tree)
	if err != nil {
		m.err = err
		return nil
	}
	w.name = m.tree.displayName(note) // the query is the agent's name
	w.parent = m.tempTree.root
	m.tempTree.root.children = append(m.tempTree.root.children, w)

	for _, c := range note.children {
		m.copyContextInto(c, w)
	}

	m.tree.remove(note)
	m.ensureViewNonEmpty()
	m.refreshRows()
	m.clampCursor()
	m.lastAgent = w.uuid
	m.unsaved = true
	m.flash = "launched agent"
	return runWorker(m, w)
}

// copyContextInto deep-copies a notes subtree into the temp tree as real context
// nodes (no mirrors), so the agent is self-contained when the source is removed.
func (m *Model) copyContextInto(src *item, parent *item) {
	nn, err := m.tempTree.newItem()
	if err != nil {
		return
	}
	nn.name = m.tree.displayName(src)
	nn.note = src.note
	nn.typ = src.typ
	if nn.typ == "" || nn.typ == database.TypeWorker {
		nn.typ = database.TypeBullets
	}
	nn.parent = parent
	parent.children = append(parent.children, nn)
	for _, c := range src.children {
		m.copyContextInto(c, nn)
	}
}

// runAgentAction is alt+r on an agent: RE-RUN it (never the old toggle-cancel). A
// running agent is left alone (stop with x); an idle/alive one is re-prompted in
// the same conversation; an exited one starts a fresh turn.
func (m *Model) runAgentAction(it *item) tea.Cmd {
	if m.workerStatus[it.uuid] == "running" {
		return nil // already working
	}
	if s := m.liveSteer(it.uuid); s != nil {
		_ = s.Steer(ultraloopStrip(it.name)) // re-prompt the same query, same conversation
		m.appendXcript(it.uuid, "you", ultraloopStrip(it.name))
		if m.workerStatus != nil {
			m.workerStatus[it.uuid] = "running"
		}
		if m.workerAction != nil {
			m.workerAction[it.uuid] = workerActivity{text: "thinking…"}
		}
		if m.workerLastActive != nil {
			m.workerLastActive[it.uuid] = time.Now()
		}
		m.flash = "re-running"
		return nil
	}
	return runWorker(m, it) // exited → fresh turn
}

// stopAgent cancels a live agent's process (the intentional stop; not an error).
func (m *Model) stopAgent(it *item) {
	if cancel, running := m.runCancel[it.uuid]; running {
		cancel()
		delete(m.runCancel, it.uuid)
		if m.workerStatus != nil {
			m.workerStatus[it.uuid] = "done"
		}
	}
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
	if m.workerSess == nil {
		m.workerSess = map[string]agent.Session{}
	}
	// already live → don't double-spawn (stop is 'x'; re-run is runAgentAction).
	// We never cancel here: cancelling an idle (alive) agent killed it and surfaced
	// "pi exited unexpectedly" when the user just wanted to re-run.
	if _, running := m.runCancel[it.uuid]; running {
		return nil
	}
	m.lastAgent = it.uuid
	// a worker that already has a session — live this run, or persisted from a prior
	// run (even across an editor restart) — RESUMES its conversation; only a
	// brand-new worker sends the full assembled context. workerHasSession hydrates
	// the snapshot, so this is correct right after reopening too.
	resuming := m.workerHasSession(it.uuid)
	if m.workerSessLoaded == nil {
		m.workerSessLoaded = map[string]bool{}
	}
	m.workerSessLoaded[it.uuid] = true                      // memory authoritative; render won't reload over it
	m.appendXcript(it.uuid, "you", ultraloopStrip(it.name)) // record this user turn
	// persist first so the context (mirror sources + this worker's subtree) is in
	// the DB for buildWorkerTask to read
	if _, err := m.saveAll(); err == nil {
		m.unsaved = false
	}

	m.runOut[it.uuid] = nil
	ch := make(chan tea.Msg, 1024)
	m.runCh[it.uuid] = ch
	if m.workerStatus == nil {
		m.workerStatus = map[string]string{}
	}
	m.workerStatus[it.uuid] = "running"
	if m.workerStart == nil {
		m.workerStart = map[string]time.Time{}
		m.workerLastActive = map[string]time.Time{}
		m.workerModel = map[string]string{}
	}
	now := time.Now()
	if m.workerStart[it.uuid].IsZero() {
		m.workerStart[it.uuid] = now // first launch; survives re-runs
	}
	m.workerLastActive[it.uuid] = now
	// capture the model at launch so switching the global model later only affects
	// NEW agents; a re-run of this agent keeps its original model
	model, thinking := m.curModel()
	if mm := m.workerModel[it.uuid]; mm != "" {
		model = mm
	} else {
		m.workerModel[it.uuid] = model
	}
	mdl := agent.ParseModel(model) // mdl.CLI selects the backend (pi/opencode/grok)

	opts := agent.RunOptions{
		Model:    mdl,
		Thinking: thinking, // "off" handled by the backend (→ no --thinking)
		// sessions are the default: a stable id keyed on the node makes the
		// conversation resumable across restarts (pi --session-id + --session-dir).
		SessionID:  it.uuid,
		SessionDir: m.piSessionDir(),
	}
	task := m.buildWorkerTask(it)
	if mdl.CLI == agent.ProviderPi {
		// pi has lflow's finish_worker extension: the deliverable is structured
		// outline nodes the agent emits via that tool.
		opts.Tools = []string{"read", "bash", "grep", "find", "ls", "edit", "write", "finish_worker"}
		opts.SystemPrompt = workerSystemPrompt
		if ext := workerExtensionPath(); ext != "" {
			opts.Extensions = []string{ext}
		}
		// resuming pi: the session already holds the context, so send only the new
		// turn rather than re-assembling and re-sending the whole context.
		if resuming {
			task = ultraloopStrip(it.name)
		}
	} else {
		// opencode / grok have no finish_worker extension and may not accept a
		// system prompt over their CLI, so the directive rides inline on the task
		// and adaptSession harvests the final assistant message as the deliverable.
		// Native cross-restart resume is not wired for these yet, so always send the
		// full context (their session id is ignored by the backend).
		task = workerTextInstruction + "\n\n" + task
	}
	sess, err := agent.Run(context.Background(), mdl.CLI, task, opts)
	if err != nil {
		m.workerStatus[it.uuid] = "error"
		delete(m.runCh, it.uuid)
		m.err = err
		return nil
	}
	m.workerSess[it.uuid] = sess
	m.runCancel[it.uuid] = sess.Stop
	go adaptSession(it.uuid, sess, ch)

	// ultraloop: if the query asks to loop, register the schedule and start the
	// 1s loop tick (once) so it re-prompts forever.
	cmd := waitBashCmd(ch)
	if m.registerLoop(it.uuid, it.name) && !m.loopTicking {
		m.loopTicking = true
		cmd = tea.Batch(cmd, loopTick())
	}
	return cmd
}

// fmtDur renders a worker's elapsed work time compactly: 4s, 1m02s, 1h05m.
func fmtDur(d time.Duration) string {
	s := int(d.Seconds())
	if s < 0 {
		s = 0
	}
	if s < 60 {
		return fmt.Sprintf("%ds", s)
	}
	mn := s / 60
	s %= 60
	if mn < 60 {
		return fmt.Sprintf("%dm%02ds", mn, s)
	}
	return fmt.Sprintf("%dh%02dm", mn/60, mn%60)
}

// workerElapsed is the time worked: launch → last activity (frozen when idle).
func (m *Model) workerElapsed(uuid string) string {
	s, ok := m.workerStart[uuid]
	if !ok {
		return ""
	}
	e := m.workerLastActive[uuid]
	if e.Before(s) {
		e = s
	}
	return fmtDur(e.Sub(s))
}

// buildWorkerTask assembles the agent's first prompt from the worker node's own
// text (the message), its note, and its children — mirror children resolve to
// their source node's content. Context = message + note + children.
func (m *Model) buildWorkerTask(it *item) string {
	var b strings.Builder
	b.WriteString(ultraloopStrip(it.name)) // prompt with the task, not the loop word
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

// adaptSession bridges a pkg/agent Session to the editor's tea.Msg stream: it
// translates each normalized agent.Event into the worker UI messages (activity,
// usage, deliverable, transcript) the editor already renders, then emits a
// terminal bashDoneMsg when the session's event stream closes. This is the only
// seam between the provider-agnostic agent layer and the bubbletea worker UI.
func adaptSession(uuid string, sess agent.Session, ch chan tea.Msg) {
	code := 0
	var turnText strings.Builder // assistant text accumulated for the current turn
	gotFinish := false           // did finish_worker fire this turn? (pi only)

	// flushDeliverable turns a turn's final assistant text into the deliverable for
	// backends that lack finish_worker (opencode/grok): the answer IS the message,
	// wrapped as one plain node. A turn that already called finish_worker, or that
	// produced no text, contributes nothing here.
	flushDeliverable := func() {
		t := strings.TrimSpace(turnText.String())
		turnText.Reset()
		if gotFinish || t == "" {
			gotFinish = false
			return
		}
		if b, err := json.Marshal([]deliverNode{{Text: t}}); err == nil {
			ch <- workerDeliverableMsg{uuid, string(b)}
		}
	}

	for ev := range sess.Events() {
		switch ev.Kind {
		case agent.EventToolStart:
			if ev.Tool == "finish_worker" {
				gotFinish = true
				ch <- workerActivityMsg{uuid, workerActivity{tool: "finish_worker", text: "writing result"}, "running", true}
				// the deliverable is an outline (nodes), never markdown — carry the
				// nodes JSON verbatim for the model side to materialize directly
				var fw struct {
					Nodes json.RawMessage `json:"nodes"`
				}
				if json.Unmarshal(ev.Args, &fw) == nil && len(fw.Nodes) > 0 {
					ch <- workerDeliverableMsg{uuid, string(fw.Nodes)}
				}
				continue
			}
			ch <- workerActivityMsg{uuid, workerActivity{tool: ev.Tool, text: ev.Detail}, "running", true}
		case agent.EventToolUpdate:
			ch <- workerActivityMsg{uuid, workerActivity{tool: ev.Tool, text: ev.Detail}, "running", false}
		case agent.EventUsage:
			if ev.Usage != nil {
				ch <- workerUsageMsg{uuid, workerUsage{model: ev.Usage.Model, in: ev.Usage.In, out: ev.Usage.Out, cost: ev.Usage.Cost, estimated: ev.Usage.Estimated}}
			}
		case agent.EventAgentText:
			// pi narrates nothing here (it uses finish_worker); opencode/grok emit
			// their answer as text, which becomes the deliverable on turn end.
			turnText.WriteString(ev.Text)
		case agent.EventTurnEnd:
			flushDeliverable()
			ch <- workerActivityMsg{uuid, workerActivity{text: ev.Status}, ev.Status, false}
		case agent.EventError:
			code = 1
			ch <- workerActivityMsg{uuid, workerActivity{text: "error: " + clipStr(ev.Text, 60)}, "error", false}
			ch <- bashLineMsg{uuid, "error: " + ev.Text, true}
		case agent.EventLog:
			ch <- bashLineMsg{uuid, ev.Text, ev.IsErr}
		}
	}
	flushDeliverable() // stream closed without a turn-end (e.g. one-shot exit)
	// stream closed → the process exited. Surface a terminal error we hadn't
	// already reported as an activity, then mark the worker done.
	if err := sess.Err(); err != nil {
		if code == 0 {
			ch <- workerActivityMsg{uuid, workerActivity{text: "error: " + clipStr(err.Error(), 60)}, "error", false}
		}
		code = 1
	}
	ch <- bashDoneMsg{uuid, code}
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
	m.ensureWorkerSessLoaded(it.uuid) // a reopened worker shows its prior run
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
		b.WriteString(cDim + fmt.Sprintf(" ↑%s ↓%s %s", ktok(u.in), ktok(u.out), costStr(u)) + cReset)
	}
	if el := m.workerElapsed(it.uuid); el != "" {
		b.WriteString(cDim + " " + el + cReset)
	}
	if cd := m.loopCountdown(it.uuid); cd != "" {
		b.WriteString(cDim + " · " + cReset + cd)
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
