package editor

import (
	"context"
	"encoding/json"
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lflow/lflow/tui/database"
	"github.com/lflow/lflow/tui/nlp"
	"github.com/lflow/lflow/tui/utils"
)

// The nlpcompute node: natural language as code — a fully red → arrow AND red
// prose ("Create a NN, train it on these inputs, return its weights and
// metrics"), an ipynb cell whose source is prose. alt+r launches a code
// generation turn for the instruction (nlp.Compute; the semantic engine is not
// implemented yet); while generating the prose SHINES (the ultraloop red
// slide, ShineText) and the cell counts toward the status bar's "N
// thinking" tally like a mention thread. Once a snippet exists the borderless
// gray code block (the same one the Code node wears — line numbers, white
// rule) REPLACES the prose row (ncBlockCode). The node has TWO faces: the code
// block and the red prose. alt+e TOGGLES between them (never an editor); on
// the code face the cursor auto-focuses the block for editing like the Code
// node (AutoFocus — thin red beam caret, type directly, esc collapses back to
// prose). The natural language lives in the node text; the code and its edits
// (the cell data {cwd, code, lang}) live in node_output — local, decoupled
// from the node row.

func init() {
	registerType(nodeType{
		key:            database.TypeNLPCompute,
		label:          "NLP Compute",
		inlineEditable: true, // the prose face: edit the instruction inline
		autoFocus:      true, // the code face: rest the cursor on it to edit, like Code
		blockFaces:     true, // alt+e toggles prose ⇄ code (never enters an editor)
		glyph: func(it *item) (string, string) {
			return "→", ncColor(it)
		},
		// red is the DEFAULT, not the law: the instruction is an ordinary rich
		// row underneath — /color, /bold and the rest restyle it, chips splice
		// into it — so this type reads like every other node until it runs.
		baseColor: func(*item) string { return cRed },
		spanColor: ncSpanColor,
		run:       runNLPCompute,
		view:      ncView{},
		blockCode: ncBlockCode,
		onRemove: func(m *Model, uuid string) {
			delete(ncGenerating, uuid)
			delete(m.nodeStore(uuid), "animating")
			if st := ncStateOf(m, uuid); st.cancel != nil {
				st.cancel()
				st.cancel, st.busy = nil, false
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
	buf    string // code-face edit buffer (seeded on Enter, flushed on Leave)
	caret  int    // caret index into buf
}

func ncStateOf(m *Model, uuid string) *ncState {
	d := m.nodeStore(uuid)
	st, _ := d["nlpcompute"].(*ncState)
	if st == nil {
		st = &ncState{}
		d["nlpcompute"] = st
	}
	return st
}

func ncLoad(m *Model, uuid string) ncData {
	var d ncData
	if m.db == nil {
		return d
	}
	if raw, err := database.LoadNodeOutput(m.db, uuid); err == nil && raw != "" {
		_ = json.Unmarshal([]byte(raw), &d)
	}
	return d
}

func ncSave(m *Model, uuid string, d ncData) {
	if m.db == nil {
		return
	}
	if raw, err := json.Marshal(d); err == nil {
		_ = database.SaveNodeOutput(m.db, uuid, string(raw))
	}
}

// ncDoneMsg carries one generation result back into the update loop.
type ncDoneMsg struct {
	uuid string
	code string
	err  error
}

func waitNCCmd(uuid string, fn func() (string, error)) tea.Cmd {
	return func() tea.Msg {
		code, err := fn()
		return ncDoneMsg{uuid: uuid, code: code, err: err}
	}
}

// computeTurn is the code-generation entry point; tests point it at a fake.
var computeTurn = nlp.Compute

// handleNCDone lands the generation result in the update loop.
func (m *Model) handleNCDone(msg ncDoneMsg) tea.Cmd {
	st := ncStateOf(m, msg.uuid)
	if msg.err != nil {
		m.flash = "compute: " + msg.err.Error()
	} else if msg.code == "" {
		m.flash = "compute returned nothing"
	} else {
		code, lang := peelCodeFence(msg.code)
		data := ncLoad(m, msg.uuid)
		data.Code, data.Lang = code, lang
		ncSave(m, msg.uuid, data)
		m.flash = "code ready · alt+e edits it"
	}
	// park the cell
	st.busy = false
	delete(ncGenerating, msg.uuid)
	delete(m.nodeStore(msg.uuid), "animating")
	if st.cancel != nil {
		st.cancel()
		st.cancel = nil
	}
	return nil
}

// runNLPCompute (alt+r) launches the code-generation turn for the cell's
// instruction.
func runNLPCompute(m *Model, it *item) tea.Cmd {
	st := ncStateOf(m, it.uuid)
	if st.busy {
		m.flash = "already computing"
		return nil
	}
	if strings.TrimSpace(it.name) == "" {
		m.flash = "write the instruction first"
		return nil
	}
	data := ncLoad(m, it.uuid)
	// the cell is TIED to the cwd it first ran in; later runs reuse it even
	// if the editor has moved elsewhere
	if data.Cwd == "" {
		if pwd, err := os.Getwd(); err == nil {
			data.Cwd = pwd
		}
		ncSave(m, it.uuid, data)
	}

	ctx, cancel := context.WithCancel(context.Background())
	st.busy, st.cancel = true, cancel
	ncGenerating[it.uuid] = true             // the render hooks read the turn state here
	m.nodeStore(it.uuid)["animating"] = true // keep the shine tick alive while generating
	return waitNCCmd(it.uuid, func() (string, error) {
		return computeTurn(ctx, it.name, nil)
	})
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

// The prose face has NO render override: the instruction goes through the same
// renderBody every other row does, so a /color or /bold on the node lands, and a
// #tag, a link or a $ chip typed into the instruction renders as that chip
// instead of a raw anchor. What the type still owns is the DEFAULT color (red,
// through baseColor) and the shine while it is generating (ncSpanColor) — both
// applied through renderBody's own per-rune path, so the caret, the horizontal
// selection and every chip keep working underneath them.

// ncColor is the row's foreground: the node's own /color when it has one, the
// type's red otherwise. The glyph follows the text, so a recolored cell is
// recolored whole.
func ncColor(it *item) string {
	if c := styleBaseColor(it.style); c != "" {
		return c
	}
	return cRed
}

// ncGenerating publishes which cells are mid-generation for the Model-less
// render hooks (the same pattern as liveCmdRuns and the agent looks): rendering
// runs off the item alone, so the turn state has to be readable without a Model.
var ncGenerating = map[string]bool{}

// ncSpanColor makes a generating cell SHINE — the ultraloop red slide across
// its own text, one color per rune, through the channel the magic keywords use.
// It replaces the row's foreground only while the turn is in flight; the node's
// own color comes back the moment the code lands.
func ncSpanColor(it *item, runes []rune) map[int]string {
	if !ncGenerating[it.uuid] || len(runes) == 0 {
		return nil
	}
	kw := magicKeywords[1] // ultraloop — red
	out := make(map[int]string, len(runes))
	for j := range runes {
		out[j] = shineColorAt(len(runes), j, animFrame, kw.speed, kw.base, kw.peak)
	}
	return out
}

// ncBlockCode makes the node render AS the borderless code block once a snippet
// exists (replacing the red prose row). While generating it yields to the shining
// prose (ok=false); focused, the live edit buffer + caret drive the block.
func ncBlockCode(m *Model, it *item, focused bool) (string, int, bool) {
	st := ncStateOf(m, it.uuid)
	if st.busy {
		return "", -1, false
	}
	if face, _ := m.nodeStore(it.uuid)["blockFace"].(string); face == "nlp" {
		return "", -1, false // alt+e flipped to the prose face — show ncRender
	}
	d := ncLoad(m, it.uuid)
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
// (CodeBlockBands), seeded from the generated snippet and flushed back
// to node_output on leave. It is auto-focused when the cursor rests on the code
// face (AutoFocus); esc collapses to the prose face, alt+e toggles either way,
// alt+r regenerates. The live buffer lives in ncState (NodeStore).
type ncView struct{}

func (v ncView) enter(m *Model, it *item) bool {
	// autoFocus calls this on every key — decline silently on the prose face or
	// before any code exists so the cursor keeps editing the instruction inline.
	if face, _ := m.nodeStore(it.uuid)["blockFace"].(string); face == "nlp" {
		return false
	}
	d := ncLoad(m, it.uuid)
	if d.Code == "" {
		return false
	}
	st := ncStateOf(m, it.uuid)
	st.buf, st.caret = d.Code, len([]rune(d.Code))
	return true
}

// leave flushes the edited buffer back to the cell (node_output).
func (v ncView) leave(m *Model, it *item) {
	st := ncStateOf(m, it.uuid)
	if d := ncLoad(m, it.uuid); st.buf != d.Code {
		d.Code = st.buf
		ncSave(m, it.uuid, d)
	}
	st.buf, st.caret = "", 0
}

func (v ncView) lines(m *Model, it *item, width int) int {
	return 2 + len(strings.Split(ncStateOf(m, it.uuid).buf, "\n"))
}

// key edits the code buffer; alt+r regenerates; esc falls through to the central
// handler (which collapses to the prose face). alt+e never reaches here — it is
// intercepted upstream as the face toggle.
func (v ncView) key(m *Model, it *item, k tea.KeyMsg) (tea.Cmd, bool) {
	if k.String() == "alt+r" {
		return runNLPCompute(m, it), true
	}
	st := ncStateOf(m, it.uuid)
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
		caret = utils.CaretVMove(buf, caret, -1)
	case "down":
		caret = utils.CaretVMove(buf, caret, +1)
	case "home":
		line, _ := utils.CaretLineCol(buf, caret)
		caret = utils.CaretAt(buf, line, 0)
	case "end":
		line, _ := utils.CaretLineCol(buf, caret)
		caret = utils.CaretAt(buf, line, 1<<30)
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

func (v ncView) bands(m *Model, it *item, rail string, width, scroll, winH int, focused bool) []string {
	st := ncStateOf(m, it.uuid)
	caret := st.caret
	if !focused {
		caret = -1
	}
	return CodeBlockBands(st.buf, caret, focused, rail, width, scroll, winH)
}
