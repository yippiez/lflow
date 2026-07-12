package editor

import (
	"encoding/json"
	"os/exec"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// The codesig node: explore a source file's shape with the signatures /
// dshell / cstack CLIs. The node's text (or its first path chip) names the
// file; alt+e lists its signatures (functions, classes, constants — parsed
// from `signatures --format jsonl`), and drilling into an entry runs the
// right tool for its kind: c = cstack (the call tree of a function), d =
// dshell (a structure's shell), enter picks by kind. esc backs out of a
// drill, then closes. Dep-gated on the signatures binary; dshell/cstack are
// checked at drill time (they support fewer languages and may be absent
// independently).

// sigEntry is one signature row from the jsonl stream.
type sigEntry struct {
	Kind   string `json:"kind"`
	Text   string `json:"text"`
	Line   int    `json:"line"`
	Indent int    `json:"indent"`
}

// csState is the per-node ephemeral explorer state (nodeStore, key "codesig").
type csState struct {
	file    string
	entries []sigEntry
	sel     int
	out     []string // drill output; nil = the signature list is showing
	title   string   // drill header, e.g. "cstack EncodeValue"
	err     string
}

func csStateOf(m *Model, it *item) *csState {
	d := m.nodeStore(it.uuid)
	st, _ := d["codesig"].(*csState)
	if st == nil {
		st = &csState{}
		d["codesig"] = st
	}
	return st
}

// csFile resolves the node's file: the first path chip wins, else the node's
// plain text.
func (m *Model) csFile(it *item) string {
	for _, sp := range anchorSpans([]rune(it.name)) {
		if c, ok := m.chips[sp.id]; ok && c.Kind == chipKindPath {
			return c.Value
		}
	}
	return strings.TrimSpace(expandAnchors(it.name, m.chips))
}

// codeSigView is the alt+e signature explorer.
type codeSigView struct{}

// csShowRows is the explorer window height.
const csShowRows = 18

func (codeSigView) Enter(m *Model, it *item) bool {
	st := csStateOf(m, it)
	st.sel, st.out, st.title, st.err = 0, nil, "", ""
	st.file = m.csFile(it)
	if st.file == "" {
		st.err = "name a source file first (text or path chip)"
		return true
	}
	out, err := exec.Command("signatures", "--format", "jsonl", st.file).Output()
	if err != nil {
		st.err = "signatures failed on " + st.file
		return true
	}
	st.entries = st.entries[:0]
	for _, l := range strings.Split(strings.TrimRight(string(out), "\n"), "\n") {
		var e sigEntry
		if json.Unmarshal([]byte(l), &e) == nil && e.Text != "" {
			st.entries = append(st.entries, e)
		}
	}
	if len(st.entries) == 0 {
		st.err = "no signatures in " + st.file
	}
	return true
}

func (codeSigView) Leave(m *Model, it *item) {
	st := csStateOf(m, it)
	st.entries, st.out, st.err = nil, nil, ""
}

func (codeSigView) Lines(m *Model, it *item, width int) int {
	st := csStateOf(m, it)
	if st.err != "" {
		return 2
	}
	if st.out != nil {
		return 1 + min(len(st.out), csShowRows)
	}
	return 1 + min(len(st.entries), csShowRows)
}

// sigIdent extracts the declared identifier from a signature line — the token
// the drill tools take ("func EncodeValue(v any)…" → "EncodeValue",
// "type Req struct" → "Req", "class Foo(Base):" → "Foo").
func sigIdent(text string) string {
	skip := map[string]bool{
		"func": true, "def": true, "fn": true, "function": true, "type": true,
		"class": true, "struct": true, "enum": true, "trait": true, "impl": true,
		"interface": true, "pub": true, "async": true, "static": true, "const": true,
		"var": true, "let": true, "export": true, "public": true, "private": true,
		"protected": true, "final": true, "abstract": true, "@classmethod": true,
	}
	for _, tok := range strings.Fields(text) {
		if skip[tok] {
			continue
		}
		// strip the receiver of a Go method: "(m *Model)" tokens
		if strings.HasPrefix(tok, "(") && !strings.Contains(tok, ")") {
			continue
		}
		if strings.HasSuffix(tok, ")") && !strings.Contains(tok, "(") {
			continue
		}
		for _, cut := range []string{"(", "[", "<", "{", ":", "="} {
			if i := strings.Index(tok, cut); i >= 0 {
				tok = tok[:i]
			}
		}
		if tok != "" {
			return tok
		}
	}
	return ""
}

// csDrill runs one drill tool over the current entry.
func (m *Model) csDrill(st *csState, tool string) {
	if !m.depOK(tool) {
		m.flash = "Missing dependency: " + tool
		return
	}
	if st.sel < 0 || st.sel >= len(st.entries) {
		return
	}
	name := sigIdent(st.entries[st.sel].Text)
	if name == "" {
		m.flash = "no identifier on this line"
		return
	}
	out, err := exec.Command(tool, name, st.file).CombinedOutput()
	text := strings.TrimRight(string(out), "\n")
	if err != nil && text == "" {
		text = tool + " failed on " + name
	}
	st.out = strings.Split(text, "\n")
	st.title = tool + " " + name
}

func (v codeSigView) Key(m *Model, it *item, k tea.KeyMsg) (tea.Cmd, bool) {
	st := csStateOf(m, it)
	// drill output showing: any nav key browses, backspace/q returns to the list
	if st.out != nil {
		switch k.String() {
		case "backspace", "q", "left", "h":
			st.out, st.title = nil, ""
			return nil, true
		}
		return nil, false // esc → central close
	}
	switch k.String() {
	case "up", "k":
		if st.sel > 0 {
			st.sel--
		}
		return nil, true
	case "down", "j":
		if st.sel < len(st.entries)-1 {
			st.sel++
		}
		return nil, true
	case "c": // call tree of a function
		m.csDrill(st, "cstack")
		return nil, true
	case "d": // a data structure's shell
		m.csDrill(st, "dshell")
		return nil, true
	case "enter", " ", "space", "right", "l":
		// pick by kind: functions get their call tree, everything else its shell
		if st.sel >= 0 && st.sel < len(st.entries) && st.entries[st.sel].Kind == "function" {
			m.csDrill(st, "cstack")
		} else {
			m.csDrill(st, "dshell")
		}
		return nil, true
	case "r": // reload the file
		v.Enter(m, it)
		return nil, true
	}
	return nil, false
}

// sigKindColor colors a signature row by its kind.
func sigKindColor(kind string) string {
	switch kind {
	case "function":
		return cYellow
	case "class":
		return cCyan
	case "constant":
		return cDim
	}
	return cFG
}

func (v codeSigView) Bands(m *Model, it *item, rail string, width, scroll, winH int, focused bool) []string {
	st := csStateOf(m, it)
	var content []string
	if st.err != "" {
		content = append(content,
			clip(rail+cReset+cDim+"  codesig"+cReset, width),
			clip(rail+cReset+"  "+cRed+st.err+cReset, width))
		return windowBands(content, scroll, winH)
	}
	if st.out != nil {
		content = append(content, clip(rail+cReset+cDim+"  "+st.title+" · backspace list · esc close"+cReset, width))
		for _, l := range st.out {
			content = append(content, clip(rail+cReset+"  "+l, width))
		}
		return windowBands(content, scroll, winH)
	}
	content = append(content, clip(rail+cReset+cDim+"  "+st.file+" · enter explore · c calls · d shell · r reload"+cReset, width))
	top := 0
	if st.sel >= csShowRows {
		top = st.sel - csShowRows + 1
	}
	for i := top; i < len(st.entries) && i < top+csShowRows; i++ {
		e := st.entries[i]
		marker, style := "  ", sigKindColor(e.Kind)
		if focused && i == st.sel {
			marker = cAccent + "▸ " + cReset
			style = cFG
		}
		ind := strings.Repeat("  ", e.Indent)
		content = append(content, clip(rail+cReset+marker+ind+style+e.Text+cReset, width))
	}
	return windowBands(content, scroll, winH)
}
