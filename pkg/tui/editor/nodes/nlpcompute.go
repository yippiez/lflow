package nodes

import (
	"context"
	"encoding/json"
	"fmt"
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
// and generates the code snippet implementing the instruction. alt+e switches
// to the CODE version: numbered lines with simple syntax highlighting; esc
// switches back to the NLP version. The cell data ({cwd, code, lang}) lives
// in node_output — local, decoupled from the node row.

func init() {
	editor.RegisterNodePlugin(editor.NodePlugin{
		Key: database.TypeNLPCompute, Label: "NLP Compute",
		InlineEditable: true,
		Glyph:          func() (string, string) { return "→", editor.NodeTheme().Red },
		BaseColor:      func() string { return editor.NodeTheme().Red },
		Render:         ncRender,
		Run:            runNLPCompute,
		View:           ncView{},
		ToContext: func(h editor.NodeHost, n editor.NodeRef) (string, string, string) {
			d := ncLoad(h, n.UUID())
			attrs := ""
			if d.Lang != "" {
				attrs = `lang="` + d.Lang + `"`
			}
			return "nlpcompute", attrs, d.Code
		},
		OnRemove: func(h editor.NodeHost, uuid string) {
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

// ncState is the in-memory turn state (NodeStore, key "nlpcompute").
type ncState struct {
	busy   bool
	cancel func()
	tool   string // last tool line, shown while generating
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
		h.NodeFlash("code ready · alt+e views it")
		return waitNCCmd(msg.uuid, msg.ch)
	case "error":
		h.NodeFlash("compute: " + msg.ev.Text)
	}
	// done / error: park the cell
	st.busy, st.tool = false, ""
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

// ncRender is the NLP version's inline body: the red instruction, with a dim
// state chip when the cell has code or is computing.
func ncRender(h editor.NodeHost, n editor.NodeRef) string {
	th := editor.NodeTheme()
	st := ncStateOf(h, n.UUID())
	name := n.Text()
	if st.busy {
		suffix := "computing…"
		if st.tool != "" {
			suffix = st.tool + "…"
		}
		return name + " " + th.Dim + "⋯ " + suffix + th.Reset
	}
	if d := ncLoad(h, n.UUID()); d.Code != "" {
		label := "{code}"
		if d.Lang != "" {
			label = "{" + d.Lang + "}"
		}
		return name + " " + th.Dim + label + th.Reset
	}
	return name
}

// ── the CODE version (alt+e) ────────────────────────────────────────────────

// ncView shows the generated snippet: numbered lines, simple highlighting.
type ncView struct{}

func (ncView) Enter(h editor.NodeHost, n editor.NodeRef) bool {
	if ncLoad(h, n.UUID()).Code == "" {
		h.NodeFlash("no code yet · alt+r computes it")
		return false
	}
	return true
}

func (ncView) Leave(h editor.NodeHost, n editor.NodeRef) {}

func (ncView) Lines(h editor.NodeHost, n editor.NodeRef, width int) int {
	return 1 + len(strings.Split(ncLoad(h, n.UUID()).Code, "\n"))
}

func (ncView) Key(h editor.NodeHost, n editor.NodeRef, k tea.KeyMsg) (tea.Cmd, bool) {
	switch k.String() {
	case "r": // regenerate from the NLP version
		return runNLPCompute(h, n), true
	}
	return nil, false // esc → central: back to the NLP version
}

func (v ncView) Bands(h editor.NodeHost, n editor.NodeRef, rail string, width, scroll, winH int, focused bool) []string {
	th := editor.NodeTheme()
	d := ncLoad(h, n.UUID())
	lang := d.Lang
	if lang == "" {
		lang = "code"
	}
	var content []string
	content = append(content, editor.NodeClip(rail+th.Reset+th.Dim+"  "+lang+" · r recompute · esc nlp view"+th.Reset, width))
	lines := strings.Split(d.Code, "\n")
	numW := len(fmt.Sprintf("%d", len(lines)))
	for i, l := range lines {
		content = append(content, editor.NodeClip(fmt.Sprintf("%s%s  %s%*d%s %s",
			rail, th.Reset, th.Dim, numW, i+1, th.Reset, ncColorLine(l)), width))
	}
	return editor.NodeWindowBands(content, scroll, winH)
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
	th := editor.NodeTheme()
	trimmed := strings.TrimSpace(line)
	if strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, "//") {
		return th.Dim + line + th.Reset
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
			b.WriteString(editor.NodeColor("orange") + string(r[i:j]) + th.Reset)
			i = j
		case c == '#' || (c == '/' && i+1 < len(r) && r[i+1] == '/'):
			b.WriteString(th.Dim + string(r[i:]) + th.Reset)
			i = len(r)
		case c >= '0' && c <= '9':
			j := i
			for j < len(r) && ((r[j] >= '0' && r[j] <= '9') || r[j] == '.' || r[j] == '_') {
				j++
			}
			b.WriteString(editor.NodeColor("green") + string(r[i:j]) + th.Reset)
			i = j
		case isWordRune(c):
			j := i
			for j < len(r) && isWordRune(r[j]) {
				j++
			}
			word := string(r[i:j])
			if ncKeywords[word] {
				b.WriteString(th.Accent + word + th.Reset)
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
