package editor

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lflow/lflow/pkg/tui/database"
	"github.com/lflow/lflow/pkg/tui/tag"
)

// The nlpcompute node: natural language as code — a red → arrow and red text
// ("Create a NN, train it on these inputs, return its weights and metrics"),
// an ipynb cell whose source is prose. alt+r launches an agent pinned to the
// node's CWD (recorded the first time it runs — the cell stays tied to that
// repo); the agent reads the surrounding lflow nodes and the underlying repo
// and generates the code snippet implementing the instruction. alt+e switches
// to the CODE version: numbered lines with simple syntax highlighting; esc
// switches back to the NLP version. The cell data ({cwd, code, lang}) lives
// in node_output — local, decoupled from the node row, like a run's output.

// ncData is the persisted cell state (node_output JSON).
type ncData struct {
	Cwd  string `json:"cwd"`
	Code string `json:"code,omitempty"`
	Lang string `json:"lang,omitempty"`
}

// ncState is the in-memory turn state (nodeStore, key "nlpcompute").
type ncState struct {
	busy   bool
	cancel func()
	tool   string // last tool line, shown while generating
}

func ncStateOf(m *Model, it *item) *ncState { return ncStateOfUUID(m, it.uuid) }

func ncStateOfUUID(m *Model, uuid string) *ncState {
	d := m.nodeStore(uuid)
	st, _ := d["nlpcompute"].(*ncState)
	if st == nil {
		st = &ncState{}
		d["nlpcompute"] = st
	}
	return st
}

func (m *Model) ncLoad(uuid string) ncData {
	var d ncData
	if m.db == nil {
		return d
	}
	if raw, err := database.LoadNodeOutput(m.db, uuid); err == nil && raw != "" {
		_ = json.Unmarshal([]byte(raw), &d)
	}
	return d
}

func (m *Model) ncSave(uuid string, d ncData) {
	if m.db == nil {
		return
	}
	if raw, err := json.Marshal(d); err == nil {
		_ = database.SaveNodeOutput(m.db, uuid, string(raw))
	}
}

// ncGlyph: the red arrow — natural language that runs.
func ncGlyph(it *item) (string, string) {
	return "→", cRed
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
func (m *Model) ncPrompt(it *item) string {
	var b strings.Builder
	b.WriteString("<instruction>\n" + expandAnchors(it.name, m.chips) + "\n</instruction>\n\n")
	b.WriteString("<outline-context>\n")
	if p := it.parent; p != nil && p.uuid != "" {
		b.WriteString("parent: " + expandAnchors(m.tree.displayName(p), m.chips) + "\n")
		for _, s := range p.children {
			marker := "- "
			if s == it {
				marker = "- (the instruction) "
			}
			b.WriteString(marker + expandAnchors(m.tree.displayName(s), m.chips) + "\n")
		}
	}
	for _, c := range it.children {
		b.WriteString("  input: " + expandAnchors(m.tree.displayName(c), m.chips) + "\n")
	}
	b.WriteString("</outline-context>")
	return b.String()
}

// ncEvMsg carries one generation-stream event into the update loop.
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

// runNLPCompute (alt+r) launches the generator agent in the cell's cwd.
func runNLPCompute(m *Model, it *item) tea.Cmd {
	st := ncStateOf(m, it)
	if st.busy {
		m.flash = "already computing"
		return nil
	}
	if strings.TrimSpace(it.name) == "" {
		m.flash = "write the instruction first"
		return nil
	}
	data := m.ncLoad(it.uuid)
	// the cell is TIED to the cwd it first ran in; later runs reuse it even
	// if the editor has moved elsewhere
	if data.Cwd == "" {
		if pwd, err := os.Getwd(); err == nil {
			data.Cwd = pwd
		}
		m.ncSave(it.uuid, data)
	}

	agentName := "Pi"
	for _, a := range m.agents {
		agentName = a.Name
		break
	}
	if ag, ok := m.agentByName(agentName); ok {
		if bin, missing := m.agentDepMissing(ag); missing {
			m.flash = "Missing dependency: " + bin
			return nil
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	ch, err := m.ncSend(ctx, agentName, it, data.Cwd)
	if err != nil {
		cancel()
		m.flash = err.Error()
		return nil
	}
	st.busy, st.cancel, st.tool = true, cancel, ""
	return waitNCCmd(it.uuid, ch)
}

// ncSend runs the raw generation turn — on the daemon when connected (the
// client is only a client), locally otherwise.
func (m *Model) ncSend(ctx context.Context, agentName string, it *item, cwd string) (<-chan tag.Event, error) {
	system, prompt := ncSystemPrompt(), m.ncPrompt(it)
	if m.live != nil {
		if ag, ok := m.agentByName(agentName); !ok || (!ag.Mock && ag.URL == "") {
			wch, err := m.live.AgentPrompt(ctx, agentName, system, prompt, cwd, tag.SkillDir())
			if err != nil {
				return nil, err
			}
			out := make(chan tag.Event, 16)
			go func() {
				defer close(out)
				for ev := range wch {
					out <- tag.Event{Op: ev.Op, Text: ev.Text, Tool: ev.Tool, Placement: ev.Placement}
				}
			}()
			return out, nil
		}
	}
	cl, err := m.tagClientFor(tagAgentOrDefault(m, agentName))
	if err != nil {
		return nil, err
	}
	if c, ok := cl.(*tag.CLIClient); ok {
		c.Cwd = cwd
		return c.SendPrompt(ctx, agentName, system, prompt)
	}
	// mock/websocket transports only speak threads — wrap the prompt as one,
	// mention included so a discretionary agent knows it is addressed
	return cl.Send(ctx, agentName, []tag.ThreadNode{{Name: "@" + agentName + " " + prompt, Role: "user", Asked: true}})
}

func tagAgentOrDefault(m *Model, name string) tag.Agent {
	if a, ok := m.agentByName(name); ok {
		return a
	}
	return tag.Agent{Name: name}
}

// handleNCEvent lands one generation event.
func (m *Model) handleNCEvent(msg ncEvMsg) (tea.Model, tea.Cmd) {
	st := ncStateOfUUID(m, msg.uuid)
	switch msg.ev.Op {
	case "tool":
		st.tool = msg.ev.Tool
		return m, waitNCCmd(msg.uuid, msg.ch)
	case "thinking":
		st.tool = ""
		return m, waitNCCmd(msg.uuid, msg.ch)
	case "message":
		code, lang := peelCodeFence(msg.ev.Text)
		data := m.ncLoad(msg.uuid)
		data.Code, data.Lang = code, lang
		m.ncSave(msg.uuid, data)
		m.flash = "code ready · alt+e views it"
		return m, waitNCCmd(msg.uuid, msg.ch)
	case "error":
		m.flash = "compute: " + msg.ev.Text
	}
	// done / error: park the cell
	st.busy, st.tool = false, ""
	if st.cancel != nil {
		st.cancel()
		st.cancel = nil
	}
	return m, nil
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

// ncToContext ships the cell to agents as both versions: the instruction is
// the element line, the generated code its body.
func (m *Model) ncToContext(it *item) contextXML {
	d := m.ncLoad(it.uuid)
	attrs := ""
	if d.Lang != "" {
		attrs = `lang="` + d.Lang + `"`
	}
	return contextXML{tag: "nlpcompute", attrs: attrs, body: d.Code}
}

// ncRender is the NLP version's inline body: the red instruction, with a dim
// state chip when the cell has code or is computing.
func (m *Model) ncRender(it *item) string {
	st := ncStateOf(m, it)
	name := it.name
	if st.busy {
		suffix := "computing…"
		if st.tool != "" {
			suffix = st.tool + "…"
		}
		return name + " " + cDim + "⋯ " + suffix + cReset
	}
	if d := m.ncLoad(it.uuid); d.Code != "" {
		label := "{code}"
		if d.Lang != "" {
			label = "{" + d.Lang + "}"
		}
		return name + " " + cDim + label + cReset
	}
	return name
}

// ── the CODE version (alt+e) ────────────────────────────────────────────────

// ncView shows the generated snippet: numbered lines, simple highlighting.
type ncView struct{}

func (ncView) Enter(m *Model, it *item) bool {
	d := m.ncLoad(it.uuid)
	if d.Code == "" {
		m.flash = "no code yet · alt+r computes it"
		return false
	}
	return true
}

func (ncView) Leave(m *Model, it *item) {}

func (ncView) Lines(m *Model, it *item, width int) int {
	d := m.ncLoad(it.uuid)
	return 1 + len(strings.Split(d.Code, "\n"))
}

func (ncView) Key(m *Model, it *item, k tea.KeyMsg) (tea.Cmd, bool) {
	switch k.String() {
	case "r": // regenerate from the NLP version
		return runNLPCompute(m, it), true
	}
	return nil, false // esc → central: back to the NLP version
}

func (v ncView) Bands(m *Model, it *item, rail string, width, scroll, winH int, focused bool) []string {
	d := m.ncLoad(it.uuid)
	lang := d.Lang
	if lang == "" {
		lang = "code"
	}
	var content []string
	content = append(content, clip(rail+cReset+cDim+"  "+lang+" · r recompute · esc nlp view"+cReset, width))
	lines := strings.Split(d.Code, "\n")
	numW := len(fmt.Sprintf("%d", len(lines)))
	for i, l := range lines {
		content = append(content, clip(fmt.Sprintf("%s%s  %s%*d%s %s",
			rail, cReset, cDim, numW, i+1, cReset, ncColorLine(l)), width))
	}
	return windowBands(content, scroll, winH)
}

// ncKeywords is the shared keyword set for the simple highlighter — deliberately
// small and cross-language (python/go/js flavored); nothing token-perfect.
var ncKeywords = map[string]bool{
	"def": true, "return": true, "if": true, "else": true, "elif": true, "for": true,
	"while": true, "import": true, "from": true, "class": true, "func": true,
	"var": true, "let": true, "const": true, "type": true, "struct": true,
	"range": true, "in": true, "not": true, "and": true, "or": true, "with": true,
	"try": true, "except": true, "finally": true, "raise": true, "lambda": true,
	"go": true, "defer": true, "switch": true, "case": true, "break": true,
	"continue": true, "package": true, "function": true, "async": true, "await": true,
	"True": true, "False": true, "None": true, "true": true, "false": true,
	"nil": true, "null": true, "pass": true, "yield": true, "print": true,
}

// ncColorLine is the simple syntax highlighter: comments dim, strings orange,
// numbers green, keywords blue — tolerant, line-local, nothing clever.
func ncColorLine(line string) string {
	// full-line comment shortcut
	trimmed := strings.TrimSpace(line)
	if strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, "//") {
		return cDim + line + cReset
	}
	r := []rune(line)
	var b strings.Builder
	i := 0
	for i < len(r) {
		c := r[i]
		switch {
		case c == '"' || c == '\'':
			q := c
			j := i + 1
			for j < len(r) && r[j] != q {
				if r[j] == '\\' {
					j++
				}
				j++
			}
			if j < len(r) {
				j++
			}
			b.WriteString(styleColorCode["orange"] + string(r[i:j]) + cReset)
			i = j
		case c == '#' || (c == '/' && i+1 < len(r) && r[i+1] == '/'):
			b.WriteString(cDim + string(r[i:]) + cReset)
			i = len(r)
		case c >= '0' && c <= '9':
			j := i
			for j < len(r) && ((r[j] >= '0' && r[j] <= '9') || r[j] == '.' || r[j] == '_') {
				j++
			}
			b.WriteString(styleColorCode["green"] + string(r[i:j]) + cReset)
			i = j
		case isWordRune(c):
			j := i
			for j < len(r) && isWordRune(r[j]) {
				j++
			}
			word := string(r[i:j])
			if ncKeywords[word] {
				b.WriteString(cAccent + word + cReset)
			} else {
				b.WriteString(word)
			}
			i = j
		default:
			b.WriteRune(c)
			i++
		}
	}
	return b.String()
}

func isWordRune(c rune) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
}
