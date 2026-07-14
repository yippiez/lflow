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

// The datapack node: a Minecraft datapack file written as an outline node. The
// row is a green ◆ instruction in plain English ("give every player night
// vision when they hold a torch", "a loot table that drops 3-5 diamonds"). alt+r
// launches an agent pinned to the node's CWD (recorded the first time it runs —
// the file stays tied to that datapack repo); the agent reads the surrounding
// outline and the underlying pack and generates ONE datapack file implementing
// the instruction — an .mcfunction, or the JSON for an advancement / loot table /
// recipe / predicate / tag. WHILE GENERATING the prose SHINES (editor.ShineText)
// and the node counts toward the status bar's "N thinking" tally like a mention
// thread. Once a file exists the borderless gray code block (the same one the
// Code node wears) REPLACES the prose row (dpBlockCode). The node has TWO faces:
// the generated file and the green prose. alt+e TOGGLES between them; on the code
// face the cursor auto-focuses the block for editing like the Code node
// (AutoFocus — thin caret, type directly, esc collapses back to prose).
//
// The instruction lives in the node text; the generated file and its metadata
// (the cell data {cwd, code, lang, path}) live in node_output — local, decoupled
// from the node row. These are ordinary outline nodes, so /mirror, /move and the
// synced outline share and organize them like any other node: mirror a datapack
// node into another project and the instruction rides along, re-runnable in place.

func init() {
	editor.RegisterNodePlugin(editor.NodePlugin{
		Key: database.TypeDatapack, Label: "Datapack",
		InlineEditable: true, // the prose face: edit the instruction inline
		AutoFocus:      true, // the code face: rest the cursor on it to edit, like Code
		BlockFaces:     true, // alt+e toggles prose ⇄ code (never enters an editor)
		Glyph:          func() (string, string) { return "◆", editor.NodeTheme().Green },
		BaseColor:      func() string { return editor.NodeTheme().Green },
		Render:         dpRender,
		Run:            runDatapack,
		View:           dpView{},
		BlockCode:      dpBlockCode,
		ToContext: func(h editor.NodeHost, n editor.NodeRef) (string, string, string) {
			d := dpLoad(h, n.UUID())
			var attrs []string
			if d.Path != "" {
				attrs = append(attrs, `path="`+d.Path+`"`)
			}
			if d.Lang != "" {
				attrs = append(attrs, `lang="`+d.Lang+`"`)
			}
			return "datapack", strings.Join(attrs, " "), d.Code
		},
		OnRemove: func(h editor.NodeHost, uuid string) {
			delete(h.NodeStore(uuid), "animating")
			if st := dpStateOf(h, uuid); st.cancel != nil {
				st.cancel()
				st.cancel, st.busy = nil, false
			}
		},
	})
}

// dpData is the persisted cell state (node_output JSON).
type dpData struct {
	Cwd  string `json:"cwd"`
	Code string `json:"code,omitempty"`
	Lang string `json:"lang,omitempty"` // mcfunction | json
	Path string `json:"path,omitempty"` // datapack-relative target, e.g. data/ns/function/x.mcfunction
}

// dpState is the in-memory turn state (NodeStore, key "datapack"). It also holds
// the code face's live edit buffer while alt+e has it open.
type dpState struct {
	busy   bool
	cancel func()
	buf    string // code-face edit buffer (seeded on Enter, flushed on Leave)
	caret  int    // caret index into buf
}

func dpStateOf(h editor.NodeHost, uuid string) *dpState {
	d := h.NodeStore(uuid)
	st, _ := d["datapack"].(*dpState)
	if st == nil {
		st = &dpState{}
		d["datapack"] = st
	}
	return st
}

func dpLoad(h editor.NodeHost, uuid string) dpData {
	var d dpData
	db := h.NodeDB()
	if db == nil {
		return d
	}
	if raw, err := database.LoadNodeOutput(db, uuid); err == nil && raw != "" {
		_ = json.Unmarshal([]byte(raw), &d)
	}
	return d
}

func dpSave(h editor.NodeHost, uuid string, d dpData) {
	db := h.NodeDB()
	if db == nil {
		return
	}
	if raw, err := json.Marshal(d); err == nil {
		_ = database.SaveNodeOutput(db, uuid, string(raw))
	}
}

// dpSystemPrompt frames the agent as a Minecraft datapack generator. The output
// contract is deliberately strict so the turn yields ONE datapack file, tagged
// with its datapack-relative path, never a natural-language answer.
func dpSystemPrompt() string {
	return "You are a Minecraft datapack generation engine inside lflow, a " +
		"terminal outline note app. The user wrote a natural-language instruction " +
		"for one datapack file as an outline node. Read the instruction and its " +
		"surrounding outline context; explore the datapack repository in your " +
		"working directory (pack.mcmeta, the data/<namespace>/ tree) and, when the " +
		"context references other notes, the outline itself via the lflow CLI " +
		"(`lflow node grep <text>`, `lflow node list <node>`). Then write ONE " +
		"self-contained datapack file that implements the instruction, valid for a " +
		"current pack format.\n\n" +
		"Pick the right file kind from the instruction:\n" +
		"- an .mcfunction command script (language tag `mcfunction`), OR\n" +
		"- the JSON for an advancement, loot table, recipe, predicate, item " +
		"modifier, dimension, or a tag (language tag `json`).\n\n" +
		"OUTPUT CONTRACT — obey exactly:\n" +
		"- Reply with NOTHING but a single fenced code block.\n" +
		"- The fence info line is the language tag then the datapack-relative " +
		"path, space-separated: ```mcfunction data/<namespace>/function/<name>.mcfunction\n" +
		"  (```json data/<namespace>/loot_table/<name>.json for JSON files).\n" +
		"- No prose, preamble, or sign-off before, after, or between — not one " +
		"sentence. Put any notes as comments INSIDE an .mcfunction (# …); JSON " +
		"carries none.\n" +
		"- Emit the file, never a description of it. If the instruction is " +
		"ambiguous, still emit the best runnable file rather than asking a question."
}

// dpPrompt renders the instruction and its outline neighborhood.
func dpPrompt(n editor.NodeRef) string {
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
		b.WriteString("  detail: " + c.Text() + "\n")
	}
	b.WriteString("</outline-context>")
	return b.String()
}

// dpEvMsg carries one generation-stream event back into the plugin.
type dpEvMsg struct {
	uuid string
	ev   tag.Event
	ch   <-chan tag.Event
}

func waitDPCmd(uuid string, ch <-chan tag.Event) tea.Cmd {
	return func() tea.Msg {
		ev, ok := <-ch
		if !ok {
			return dpEvMsg{uuid: uuid, ev: tag.Event{Op: "done"}}
		}
		return dpEvMsg{uuid: uuid, ev: ev, ch: ch}
	}
}

// HandleNodePlugin lands one generation event (editor.NodePluginMsg).
func (msg dpEvMsg) HandleNodePlugin(h editor.NodeHost) tea.Cmd {
	st := dpStateOf(h, msg.uuid)
	switch msg.ev.Op {
	case "tool", "thinking":
		// narration between tool calls is discarded — only the shine shows progress
		return waitDPCmd(msg.uuid, msg.ch)
	case "message":
		code, info := peelCodeFence(msg.ev.Text)
		lang, path := splitFenceInfo(info)
		data := dpLoad(h, msg.uuid)
		data.Code, data.Lang, data.Path = code, lang, path
		dpSave(h, msg.uuid, data)
		note := "datapack ready · alt+e edits it"
		if path != "" {
			note = path + " ready · alt+e edits it"
		}
		h.NodeFlash(note)
		return waitDPCmd(msg.uuid, msg.ch)
	case "error":
		h.NodeFlash("datapack: " + msg.ev.Text)
	}
	// done / error: park the cell
	st.busy = false
	delete(h.NodeStore(msg.uuid), "animating")
	if st.cancel != nil {
		st.cancel()
		st.cancel = nil
	}
	return nil
}

// runDatapack (alt+r) launches the generator agent in the cell's cwd.
func runDatapack(h editor.NodeHost, n editor.NodeRef) tea.Cmd {
	st := dpStateOf(h, n.UUID())
	if st.busy {
		h.NodeFlash("already generating")
		return nil
	}
	if strings.TrimSpace(n.Text()) == "" {
		h.NodeFlash("write the instruction first")
		return nil
	}
	data := dpLoad(h, n.UUID())
	// the file is TIED to the cwd it first ran in; later runs reuse it even if
	// the editor has moved elsewhere
	if data.Cwd == "" {
		if pwd, err := os.Getwd(); err == nil {
			data.Cwd = pwd
		}
		dpSave(h, n.UUID(), data)
	}

	agentName := h.NodeDefaultAgent()
	if bin, missing := h.NodeAgentGate(agentName); missing {
		h.NodeFlash("Missing dependency: " + bin)
		return nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	ch, err := h.NodeComputeTurn(ctx, agentName, dpSystemPrompt(), dpPrompt(n), data.Cwd)
	if err != nil {
		cancel()
		h.NodeFlash(err.Error())
		return nil
	}
	st.busy, st.cancel = true, cancel
	h.NodeStore(n.UUID())["animating"] = true // keep the shine tick alive while generating
	return waitDPCmd(n.UUID(), ch)
}

// splitFenceInfo splits a fence info line ("mcfunction data/ns/function/x.mcfunction")
// into the language tag and the optional datapack-relative path.
func splitFenceInfo(info string) (lang, path string) {
	fields := strings.Fields(info)
	if len(fields) == 0 {
		return "", ""
	}
	lang = fields[0]
	if len(fields) > 1 {
		path = fields[1]
	}
	return lang, path
}

// dpRender is the datapack node's inline body — the whole instruction is green.
// While generating it SHINES (the ultraloop slide) with no agent trace; otherwise
// plain green. When a file exists the block replaces the row (dpBlockCode), so the
// trailing {path} chip is only ever seen off the main outline (the temp panel).
func dpRender(h editor.NodeHost, n editor.NodeRef) string {
	th := editor.NodeTheme()
	st := dpStateOf(h, n.UUID())
	name := n.Text()
	if st.busy {
		return editor.ShineText(name)
	}
	if d := dpLoad(h, n.UUID()); d.Code != "" {
		label := "{datapack}"
		if d.Path != "" {
			label = "{" + d.Path + "}"
		} else if d.Lang != "" {
			label = "{" + d.Lang + "}"
		}
		return th.Green + name + th.Reset + " " + th.Dim + label + th.Reset
	}
	return th.Green + name + th.Reset
}

// dpBlockCode makes the node render AS the borderless code block once a file
// exists (replacing the green prose row). While generating it yields to the
// shining prose (ok=false); focused, the live edit buffer + caret drive the block.
func dpBlockCode(h editor.NodeHost, n editor.NodeRef, focused bool) (string, int, bool) {
	st := dpStateOf(h, n.UUID())
	if st.busy {
		return "", -1, false
	}
	if editor.NodeBlockFace(h, n.UUID()) == "nlp" {
		return "", -1, false // alt+e flipped to the prose face — show dpRender
	}
	d := dpLoad(h, n.UUID())
	if d.Code == "" {
		return "", -1, false
	}
	if focused {
		return st.buf, st.caret, true
	}
	return d.Code, -1, true
}

// ── the code face (alt+e toggle) ────────────────────────────────────────────

// dpView is the editable code face: the same gray block the Code node wears
// (editor.CodeBlockBands), seeded from the generated file and flushed back to
// node_output on leave. It is auto-focused when the cursor rests on the code face
// (AutoFocus); esc collapses to the prose face, alt+e toggles either way, alt+r
// regenerates. The live buffer lives in dpState (NodeStore).
type dpView struct{}

func (dpView) Enter(h editor.NodeHost, n editor.NodeRef) bool {
	// autoFocus calls this on every key — decline silently on the prose face or
	// before any file exists so the cursor keeps editing the instruction inline.
	if editor.NodeBlockFace(h, n.UUID()) == "nlp" {
		return false
	}
	d := dpLoad(h, n.UUID())
	if d.Code == "" {
		return false
	}
	st := dpStateOf(h, n.UUID())
	st.buf, st.caret = d.Code, len([]rune(d.Code))
	return true
}

// Leave flushes the edited buffer back to the cell (node_output).
func (dpView) Leave(h editor.NodeHost, n editor.NodeRef) {
	st := dpStateOf(h, n.UUID())
	if d := dpLoad(h, n.UUID()); st.buf != d.Code {
		d.Code = st.buf
		dpSave(h, n.UUID(), d)
	}
	st.buf, st.caret = "", 0
}

func (dpView) Lines(h editor.NodeHost, n editor.NodeRef, width int) int {
	return 2 + len(strings.Split(dpStateOf(h, n.UUID()).buf, "\n"))
}

// Key edits the code buffer; alt+r regenerates; esc falls through to the central
// handler (which collapses to the prose face). alt+e never reaches here — it is
// intercepted upstream as the face toggle.
func (dpView) Key(h editor.NodeHost, n editor.NodeRef, k tea.KeyMsg) (tea.Cmd, bool) {
	if k.String() == "alt+r" {
		return runDatapack(h, n), true
	}
	st := dpStateOf(h, n.UUID())
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

func (dpView) Bands(h editor.NodeHost, n editor.NodeRef, rail string, width, scroll, winH int, focused bool) []string {
	st := dpStateOf(h, n.UUID())
	caret := st.caret
	if !focused {
		caret = -1
	}
	return editor.CodeBlockBands(st.buf, caret, focused, rail, width, scroll, winH)
}
