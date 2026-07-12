package nodes

import (
	"context"
	"encoding/json"
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lflow/lflow/pkg/tui/database"
	"github.com/lflow/lflow/pkg/tui/editor"
	"github.com/lflow/lflow/pkg/tui/tag"
)

// The nlpcompute node: natural language as code — a red → arrow and red text
// ("Create a NN, train it on these inputs, return its weights and metrics"),
// an ipynb cell whose source is prose. alt+r launches an agent pinned to the
// node's CWD (recorded the first time it runs — the cell stays tied to that
// repo); the agent reads the surrounding lflow nodes and the underlying repo
// and generates the code snippet implementing the instruction. alt+e TOGGLES the
// node's face: the NLP prose flips to the code face — the same gray code block
// the Code node wears (white rule, line numbers, editable) — and alt+e/esc flips
// back. The natural language lives in the node text; the code face's edits (and
// the cell data {cwd, code, lang}) live in node_output — local, decoupled from
// the node row.

func init() {
	editor.RegisterNodePlugin(editor.NodePlugin{
		Key: database.TypeNLPCompute, Label: "NLP Compute",
		InlineEditable: true,
		Glyph:          func() (string, string) { return "→", editor.NodeTheme().Red },
		BaseColor:      func() string { return editor.NodeTheme().Red },
		Render:         ncRender,
		Run:            runNLPCompute,
		View:           ncView{},
		BlockCode:      ncBlockCode,
		ToContext: func(h editor.NodeHost, n editor.NodeRef) (string, string, string) {
			d := ncLoad(h, n.UUID())
			attrs := ""
			if d.Lang != "" {
				attrs = `lang="` + d.Lang + `"`
			}
			return "nlpcompute", attrs, d.Code
		},
		OnRemove: func(h editor.NodeHost, uuid string) {
			delete(h.NodeStore(uuid), "animating")
			if st := ncStateOf(h, uuid); st.cancel != nil {
				st.cancel()
				st.cancel, st.busy, st.tool = nil, false, ""
			}
		},
	})
}

// ncData is the persisted cell state (node_output JSON).
type ncData struct {
	Cwd  string `json:"cwd"`
	Code string `json:"code,omitempty"`
	Lang string `json:"lang,omitempty"`
}

// ncState is the in-memory turn state (NodeStore, key "nlpcompute"). It also
// holds the code face's live edit buffer while alt+e has it open.
type ncState struct {
	busy   bool
	cancel func()
	tool   string // last tool line, shown while generating
	buf    string // code-face edit buffer (seeded on Enter, flushed on Leave)
	caret  int    // caret index into buf
}

func ncStateOf(h editor.NodeHost, uuid string) *ncState {
	d := h.NodeStore(uuid)
	st, _ := d["nlpcompute"].(*ncState)
	if st == nil {
		st = &ncState{}
		d["nlpcompute"] = st
	}
	return st
}

func ncLoad(h editor.NodeHost, uuid string) ncData {
	var d ncData
	db := h.NodeDB()
	if db == nil {
		return d
	}
	if raw, err := database.LoadNodeOutput(db, uuid); err == nil && raw != "" {
		_ = json.Unmarshal([]byte(raw), &d)
	}
	return d
}

func ncSave(h editor.NodeHost, uuid string, d ncData) {
	db := h.NodeDB()
	if db == nil {
		return
	}
	if raw, err := json.Marshal(d); err == nil {
		_ = database.SaveNodeOutput(db, uuid, string(raw))
	}
}

// ncSystemPrompt frames the agent as a code generation engine.
func ncSystemPrompt() string {
	return "You are a code generation engine inside lflow, a terminal outline " +
		"note app. The user wrote a natural-language compute instruction as an " +
		"outline node. Read the instruction and its surrounding outline context; " +
		"explore the repository in your working directory and, when the context " +
		"references other notes, the outline itself via the lflow CLI " +
		"(`lflow node grep <text>`, `lflow node list <node>`). Then write ONE " +
		"code snippet that implements the instruction — runnable as-is, in the " +
		"language the repository or the instruction implies (default python). " +
		"Reply with ONLY the code inside a single fenced block with a language " +
		"tag (```python … ```). No prose before or after."
}

// ncPrompt renders the instruction and its outline neighborhood.
func ncPrompt(n editor.NodeRef) string {
	var b strings.Builder
	b.WriteString("<instruction>\n" + n.Text() + "\n</instruction>\n\n")
	b.WriteString("<outline-context>\n")
	if p, ok := n.Parent(); ok {
		b.WriteString("parent: " + p.Text() + "\n")
		for _, s := range n.Siblings() {
			marker := "- "
			if s.Is(n) {
				marker = "- (the instruction) "
			}
			b.WriteString(marker + s.Text() + "\n")
		}
	}
	for _, c := range n.Children() {
		b.WriteString("  input: " + c.Text() + "\n")
	}
	b.WriteString("</outline-context>")
	return b.String()
}

// ncEvMsg carries one generation-stream event back into the plugin.
type ncEvMsg struct {
	uuid string
	ev   tag.Event
	ch   <-chan tag.Event
}

func waitNCCmd(uuid string, ch <-chan tag.Event) tea.Cmd {
	return func() tea.Msg {
		ev, ok := <-ch
		if !ok {
			return ncEvMsg{uuid: uuid, ev: tag.Event{Op: "done"}}
		}
		return ncEvMsg{uuid: uuid, ev: ev, ch: ch}
	}
}

// HandleNodePlugin lands one generation event (editor.NodePluginMsg).
func (msg ncEvMsg) HandleNodePlugin(h editor.NodeHost) tea.Cmd {
	st := ncStateOf(h, msg.uuid)
	switch msg.ev.Op {
	case "tool":
		st.tool = msg.ev.Tool
		return waitNCCmd(msg.uuid, msg.ch)
	case "thinking":
		st.tool = ""
		return waitNCCmd(msg.uuid, msg.ch)
	case "message":
		code, lang := peelCodeFence(msg.ev.Text)
		data := ncLoad(h, msg.uuid)
		data.Code, data.Lang = code, lang
		ncSave(h, msg.uuid, data)
		h.NodeFlash("code ready · alt+e toggles the code face")
		return waitNCCmd(msg.uuid, msg.ch)
	case "error":
		h.NodeFlash("compute: " + msg.ev.Text)
	}
	// done / error: park the cell
	st.busy, st.tool = false, ""
	delete(h.NodeStore(msg.uuid), "animating")
	if st.cancel != nil {
		st.cancel()
		st.cancel = nil
	}
	return nil
}

// runNLPCompute (alt+r) launches the generator agent in the cell's cwd.
func runNLPCompute(h editor.NodeHost, n editor.NodeRef) tea.Cmd {
	st := ncStateOf(h, n.UUID())
	if st.busy {
		h.NodeFlash("already computing")
		return nil
	}
	if strings.TrimSpace(n.Text()) == "" {
		h.NodeFlash("write the instruction first")
		return nil
	}
	data := ncLoad(h, n.UUID())
	// the cell is TIED to the cwd it first ran in; later runs reuse it even
	// if the editor has moved elsewhere
	if data.Cwd == "" {
		if pwd, err := os.Getwd(); err == nil {
			data.Cwd = pwd
		}
		ncSave(h, n.UUID(), data)
	}

	agentName := h.NodeDefaultAgent()
	if bin, missing := h.NodeAgentGate(agentName); missing {
		h.NodeFlash("Missing dependency: " + bin)
		return nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	ch, err := h.NodeComputeTurn(ctx, agentName, ncSystemPrompt(), ncPrompt(n), data.Cwd)
	if err != nil {
		cancel()
		h.NodeFlash(err.Error())
		return nil
	}
	st.busy, st.cancel, st.tool = true, cancel, ""
	h.NodeStore(n.UUID())["animating"] = true // keep the shine tick alive while generating
	return waitNCCmd(n.UUID(), ch)
}

// peelCodeFence extracts the first fenced block ("```lang\n…\n```"); unfenced
// text passes through whole.
func peelCodeFence(text string) (code, lang string) {
	t := strings.TrimSpace(text)
	i := strings.Index(t, "```")
	if i < 0 {
		return t, ""
	}
	rest := t[i+3:]
	nl := strings.IndexByte(rest, '\n')
	if nl < 0 {
		return t, ""
	}
	lang = strings.TrimSpace(rest[:nl])
	body := rest[nl+1:]
	if j := strings.Index(body, "```"); j >= 0 {
		body = body[:j]
	}
	return strings.TrimRight(body, "\n"), lang
}

// ncRender is the NLP version's inline body — the whole instruction is red. While
// generating it SHINES (the ultraloop slide) with no agent trace; otherwise plain
// red. When code exists the block replaces the row (ncBlockCode), so the trailing
// {lang} chip is only ever seen off the main outline (the temp panel).
func ncRender(h editor.NodeHost, n editor.NodeRef) string {
	th := editor.NodeTheme()
	st := ncStateOf(h, n.UUID())
	name := n.Text()
	if st.busy {
		return editor.ShineText(name)
	}
	if d := ncLoad(h, n.UUID()); d.Code != "" {
		label := "{code}"
		if d.Lang != "" {
			label = "{" + d.Lang + "}"
		}
		return th.Red + name + th.Reset + " " + th.Dim + label + th.Reset
	}
	return th.Red + name + th.Reset
}

// ncBlockCode makes the node render AS the borderless code block once a snippet
// exists (replacing the red prose row). While generating it yields to the shining
// prose (ok=false); focused, the live edit buffer + caret drive the block.
func ncBlockCode(h editor.NodeHost, n editor.NodeRef, focused bool) (string, int, bool) {
	st := ncStateOf(h, n.UUID())
	if st.busy {
		return "", -1, false
	}
	d := ncLoad(h, n.UUID())
	if d.Code == "" {
		return "", -1, false
	}
	if focused {
		return st.buf, st.caret, true
	}
	return d.Code, -1, true
}

// ── the code face (alt+e toggle) ────────────────────────────────────────────

// ncView is the editable code face: the same gray block the Code node wears
// (editor.CodeBlockBands), seeded from the generated snippet and flushed back
// to node_output on leave. alt+e/esc flip back to the NLP prose; alt+r
// regenerates. The live buffer lives in ncState (NodeStore).
type ncView struct{}

func (ncView) Enter(h editor.NodeHost, n editor.NodeRef) bool {
	d := ncLoad(h, n.UUID())
	if d.Code == "" {
		h.NodeFlash("no code yet · alt+r computes it")
		return false
	}
	st := ncStateOf(h, n.UUID())
	st.buf, st.caret = d.Code, len([]rune(d.Code))
	return true
}

// Leave flushes the edited buffer back to the cell (node_output).
func (ncView) Leave(h editor.NodeHost, n editor.NodeRef) {
	st := ncStateOf(h, n.UUID())
	if d := ncLoad(h, n.UUID()); st.buf != d.Code {
		d.Code = st.buf
		ncSave(h, n.UUID(), d)
	}
	st.buf, st.caret = "", 0
}

func (ncView) Lines(h editor.NodeHost, n editor.NodeRef, width int) int {
	return 2 + len(strings.Split(ncStateOf(h, n.UUID()).buf, "\n"))
}

// Key edits the code buffer; alt+r regenerates; esc/alt+e fall through to the
// central toggle back to the NLP prose.
func (ncView) Key(h editor.NodeHost, n editor.NodeRef, k tea.KeyMsg) (tea.Cmd, bool) {
	if k.String() == "alt+r" {
		return runNLPCompute(h, n), true
	}
	st := ncStateOf(h, n.UUID())
	buf, caret := st.buf, st.caret
	rl := []rune(buf)
	switch k.String() {
	case "left":
		if caret > 0 {
			caret--
		}
	case "right":
		if caret < len(rl) {
			caret++
		}
	case "up":
		caret = editor.NodeCaretVMove(buf, caret, -1)
	case "down":
		caret = editor.NodeCaretVMove(buf, caret, +1)
	case "home":
		line, _ := editor.NodeCaretLineCol(buf, caret)
		caret = editor.NodeCaretAt(buf, line, 0)
	case "end":
		line, _ := editor.NodeCaretLineCol(buf, caret)
		caret = editor.NodeCaretAt(buf, line, 1<<30)
	case "enter":
		buf = string(rl[:caret]) + "\n" + string(rl[caret:])
		caret++
	case "tab":
		buf = string(rl[:caret]) + "  " + string(rl[caret:])
		caret += 2
	case "backspace":
		if caret > 0 {
			buf = string(rl[:caret-1]) + string(rl[caret:])
			caret--
		}
	default:
		switch {
		case k.Type == tea.KeySpace && !k.Alt:
			buf = string(rl[:caret]) + " " + string(rl[caret:])
			caret++
		case k.Type == tea.KeyRunes && !k.Alt:
			s := string(k.Runes)
			buf = string(rl[:caret]) + s + string(rl[caret:])
			caret += len(k.Runes)
		default:
			return nil, false // esc, alt+e, ctrl+c … → central
		}
	}
	st.buf, st.caret = buf, caret
	return nil, true
}

func (ncView) Bands(h editor.NodeHost, n editor.NodeRef, rail string, width, scroll, winH int, focused bool) []string {
	st := ncStateOf(h, n.UUID())
	caret := st.caret
	if !focused {
		caret = -1
	}
	return editor.CodeBlockBands(st.buf, caret, focused, rail, width, scroll, winH)
}
