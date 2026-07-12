package nodes

import (
	"encoding/json"
	"os/exec"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lflow/lflow/pkg/tui/database"
	"github.com/lflow/lflow/pkg/tui/editor"
)

// The codesig node: explore a source file's shape with the signatures /
// dshell / cstack CLIs. The node's text (or its first path chip) names the
// file; alt+e lists its signatures (functions, classes, constants — parsed
// from `signatures --format jsonl`), and drilling into an entry runs the
// right tool for its kind: c = cstack (the call tree of a function), d =
// dshell (a structure's shell), enter picks by kind; backspace backs out.

func init() {
	editor.RegisterNodePlugin(editor.NodePlugin{
		Key: database.TypeCodeSig, Label: "Code Signatures", Sign: "∑ ",
		InlineEditable: true,
		CLIDeps:        []string{"signatures"},
		View:           codeSigView{},
		ToContext: func(h editor.NodeHost, n editor.NodeRef) (string, string, string) {
			return "codesignatures", "", ""
		},
	})
}

// sigEntry is one signature row from the jsonl stream.
type sigEntry struct {
	Kind   string `json:"kind"`
	Text   string `json:"text"`
	Line   int    `json:"line"`
	Indent int    `json:"indent"`
}

// csState is the per-node ephemeral explorer state (NodeStore, key "codesig").
type csState struct {
	file    string
	entries []sigEntry
	sel     int
	out     []string // drill output; nil = the signature list is showing
	title   string   // drill header, e.g. "cstack EncodeValue"
	err     string
}

func csStateOf(h editor.NodeHost, uuid string) *csState {
	d := h.NodeStore(uuid)
	st, _ := d["codesig"].(*csState)
	if st == nil {
		st = &csState{}
		d["codesig"] = st
	}
	return st
}

// csFile resolves the node's file: the first path chip wins, else the plain text.
func csFile(n editor.NodeRef) string {
	if p := n.PathChip(); p != "" {
		return p
	}
	return strings.TrimSpace(n.Text())
}

// codeSigView is the alt+e signature explorer.
type codeSigView struct{}

// csShowRows is the explorer window height.
const csShowRows = 18

func (codeSigView) Enter(h editor.NodeHost, n editor.NodeRef) bool {
	st := csStateOf(h, n.UUID())
	st.sel, st.out, st.title, st.err = 0, nil, "", ""
	st.file = csFile(n)
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

func (codeSigView) Leave(h editor.NodeHost, n editor.NodeRef) {
	st := csStateOf(h, n.UUID())
	st.entries, st.out, st.err = nil, nil, ""
}

func (codeSigView) Lines(h editor.NodeHost, n editor.NodeRef, width int) int {
	st := csStateOf(h, n.UUID())
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
func csDrill(h editor.NodeHost, st *csState, tool string) {
	if !h.NodeDepOK(tool) {
		h.NodeFlash("Missing dependency: " + tool)
		return
	}
	if st.sel < 0 || st.sel >= len(st.entries) {
		return
	}
	name := sigIdent(st.entries[st.sel].Text)
	if name == "" {
		h.NodeFlash("no identifier on this line")
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

func (v codeSigView) Key(h editor.NodeHost, n editor.NodeRef, k tea.KeyMsg) (tea.Cmd, bool) {
	st := csStateOf(h, n.UUID())
	// drill output showing: backspace/q returns to the list
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
		csDrill(h, st, "cstack")
		return nil, true
	case "d": // a data structure's shell
		csDrill(h, st, "dshell")
		return nil, true
	case "enter", " ", "space", "right", "l":
		// pick by kind: functions get their call tree, everything else its shell
		if st.sel >= 0 && st.sel < len(st.entries) && st.entries[st.sel].Kind == "function" {
			csDrill(h, st, "cstack")
		} else {
			csDrill(h, st, "dshell")
		}
		return nil, true
	case "r": // reload the file
		v.Enter(h, n)
		return nil, true
	}
	return nil, false
}

// sigKindColor colors a signature row by its kind.
func sigKindColor(kind string) string {
	th := editor.NodeTheme()
	switch kind {
	case "function":
		return th.Yellow
	case "class":
		return th.Cyan
	case "constant":
		return th.Dim
	}
	return th.FG
}

func (v codeSigView) Bands(h editor.NodeHost, n editor.NodeRef, rail string, width, scroll, winH int, focused bool) []string {
	st := csStateOf(h, n.UUID())
	th := editor.NodeTheme()
	var content []string
	if st.err != "" {
		content = append(content,
			editor.NodeClip(rail+th.Reset+th.Dim+"  codesig"+th.Reset, width),
			editor.NodeClip(rail+th.Reset+"  "+th.Red+st.err+th.Reset, width))
		return editor.NodeWindowBands(content, scroll, winH)
	}
	if st.out != nil {
		content = append(content, editor.NodeClip(rail+th.Reset+th.Dim+"  "+st.title+" · backspace list · esc close"+th.Reset, width))
		for _, l := range st.out {
			content = append(content, editor.NodeClip(rail+th.Reset+"  "+l, width))
		}
		return editor.NodeWindowBands(content, scroll, winH)
	}
	content = append(content, editor.NodeClip(rail+th.Reset+th.Dim+"  "+st.file+" · enter explore · c calls · d shell · r reload"+th.Reset, width))
	top := 0
	if st.sel >= csShowRows {
		top = st.sel - csShowRows + 1
	}
	for i := top; i < len(st.entries) && i < top+csShowRows; i++ {
		e := st.entries[i]
		marker, style := "  ", sigKindColor(e.Kind)
		if focused && i == st.sel {
			marker = th.Accent + "▸ " + th.Reset
			style = th.FG
		}
		ind := strings.Repeat("  ", e.Indent)
		content = append(content, editor.NodeClip(rail+th.Reset+marker+ind+style+e.Text+th.Reset, width))
	}
	return editor.NodeWindowBands(content, scroll, winH)
}
