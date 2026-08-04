package nodes

import (
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lflow/lflow/packages/database"
	"github.com/lflow/lflow/packages/editor"
)

// The Python node + codec: a statement composed AS an outline, the
// programming-language sibling of the math node. A node's text is one logical
// line — a compound header (`if x > 3:`) with its body as children, or a leaf.
// The first-class constructs are their own types: `def greet():` is a fn node
// with text `greet()`, `class App:` a class node with text `App`, `# c` a
// comment node — so a function is the same node in every language, and each
// codec puts its own syntax back. alt+r exports any subtree as real indented
// source; `lflow file open x.py` binds a whole file. Keywords color yellow
// like math operators; a header row shows a dim `· n lines` tail.

var pythonSpec = langSpec{
	name:        "python",
	stmtType:    database.TypePython,
	commentLead: "# ",
	indentUnit:  "    ",
	braces:      false,
	renderFn: func(sig string) string {
		if rest, ok := strings.CutPrefix(sig, "async "); ok {
			return "async def " + rest + ":"
		}
		return "def " + sig + ":"
	},
	renderClass: func(head string) string { return "class " + head + ":" },
}

// pythonKeywords drives span coloring — one table, like mathSym.
var pythonKeywords = map[string]bool{
	"False": true, "None": true, "True": true, "and": true, "as": true,
	"assert": true, "async": true, "await": true, "break": true, "class": true,
	"continue": true, "def": true, "del": true, "elif": true, "else": true,
	"except": true, "finally": true, "for": true, "from": true, "global": true,
	"if": true, "import": true, "in": true, "is": true, "lambda": true,
	"nonlocal": true, "not": true, "or": true, "pass": true, "raise": true,
	"return": true, "try": true, "while": true, "with": true, "yield": true,
}

func isWordRune(r rune) bool {
	return r == '_' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
}

// keywordSpanColor tints keyword runes yellow and comments dim — shared by the
// python and rust statement nodes.
func keywordSpanColor(keywords map[string]bool, commentMark string) func(runes []rune) map[int]string {
	return func(runes []rune) map[int]string {
		th := editor.NodeTheme()
		colors := map[int]string{}
		if i := strings.Index(string(runes), commentMark); i >= 0 {
			pre := []rune(string(runes)[:i])
			for j := len(pre); j < len(runes); j++ {
				colors[j] = th.Dim
			}
		}
		for i := 0; i < len(runes); {
			if !isWordRune(runes[i]) {
				i++
				continue
			}
			j := i
			for j < len(runes) && isWordRune(runes[j]) {
				j++
			}
			if keywords[string(runes[i:j])] && colors[i] == "" {
				for k := i; k < j; k++ {
					colors[k] = th.Yellow
				}
			}
			i = j
		}
		return colors
	}
}

// codeLineCount counts the lines a subtree renders to.
func codeLineCount(n editor.NodeRef) int {
	total := 1
	for _, c := range n.Children() {
		total += codeLineCount(c)
	}
	return total
}

// codeBodyTail shows a header's folded body size: `· 12 lines`. Structure-only
// render path — Children() only, never Text().
func codeBodyTail(n editor.NodeRef) string {
	if len(n.Children()) == 0 {
		return ""
	}
	th := editor.NodeTheme()
	lines := codeLineCount(n) - 1
	word := "lines"
	if lines == 1 {
		word = "line"
	}
	return th.Dim + " · " + strconv.Itoa(lines) + " " + word + th.Reset
}

// refToSrc projects an editor subtree onto the neutral SrcNode form so alt+r
// exports through the same renderer as the file codec.
func refToSrc(n editor.NodeRef) *SrcNode {
	s := &SrcNode{Type: n.Type(), Text: n.Text()}
	for _, c := range n.Children() {
		s.Kids = append(s.Kids, refToSrc(c))
	}
	return s
}

// runCodeExport is alt+r for a language statement node: export the subtree as
// real source to the run band.
func runCodeExport(spec langSpec) func(h editor.NodeHost, n editor.NodeRef) tea.Cmd {
	return func(h editor.NodeHost, n editor.NodeRef) tea.Cmd {
		src, err := renderCode([]*SrcNode{refToSrc(n)}, spec)
		if err != nil {
			h.NodeFlash("export: " + err.Error())
			return nil
		}
		editor.NodeRunOut(h, n.UUID(), strings.Split(strings.TrimRight(src, "\n"), "\n"))
		h.NodeFlash(spec.name + " → output")
		return nil
	}
}

func init() {
	editor.RegisterNodePlugin(editor.NodePlugin{
		Key:            database.TypePython,
		Label:          "Python",
		InlineEditable: true,
		SpanColor:      keywordSpanColor(pythonKeywords, "#"),
		BodyTail:       codeBodyTail,
		Run:            runCodeExport(pythonSpec),
		ToContext: func(h editor.NodeHost, n editor.NodeRef) (string, string, string) {
			return "python", "", n.Text()
		},
	})
}

// ── the .py file codec ──────────────────────────────────────────────────────

// pythonCodec binds a .py file to a node tree: constructs become their
// first-class nodes, everything else is a python statement node, nesting by
// indentation. Rendering re-indents to 4 spaces per level — a first save
// normalizes, after which parse→render round-trips byte-identically. Blank
// lines are empty nodes under the previous line, so spacing survives.
type pythonCodec struct{}

func init() { fileCodecs = append(fileCodecs, pythonCodec{}) }

func (pythonCodec) Name() string             { return "python" }
func (pythonCodec) Exts() []string           { return []string{".py"} }
func (pythonCodec) Allowed() map[string]bool { return codeAllowed(database.TypePython) }

// classifyPython maps one trimmed line onto its node.
func classifyPython(trimmed string) *SrcNode {
	switch {
	case strings.HasPrefix(trimmed, "# "):
		return classifyComment(trimmed[2:])
	case trimmed == "#":
		return &SrcNode{Type: database.TypeComment}
	case strings.HasPrefix(trimmed, "def ") && strings.HasSuffix(trimmed, ":"):
		return &SrcNode{Type: database.TypeFn, Text: strings.TrimSuffix(trimmed[4:], ":")}
	case strings.HasPrefix(trimmed, "async def ") && strings.HasSuffix(trimmed, ":"):
		return &SrcNode{Type: database.TypeFn, Text: "async " + strings.TrimSuffix(trimmed[10:], ":")}
	case strings.HasPrefix(trimmed, "class ") && strings.HasSuffix(trimmed, ":"):
		return &SrcNode{Type: database.TypeClass, Text: strings.TrimSuffix(trimmed[6:], ":")}
	default:
		return &SrcNode{Type: database.TypePython, Text: trimmed}
	}
}

func (pythonCodec) Parse(src string) ([]*SrcNode, error) {
	root := &SrcNode{}
	type ent struct {
		n      *SrcNode
		indent int
	}
	stack := []ent{{n: root, indent: -1}}

	for _, raw := range strings.Split(strings.TrimRight(src, "\n"), "\n") {
		line := strings.TrimRight(raw, " \t")
		w, trimmed := indentWidth(line)
		if trimmed == "" {
			// blank: an empty node under the previous line, renders back in
			// place ("" at any depth) without ending the block
			stack[len(stack)-1].n.Kid(&SrcNode{Type: database.TypePython})
			continue
		}
		for len(stack) > 1 && stack[len(stack)-1].indent >= w {
			stack = stack[:len(stack)-1]
		}
		n := stack[len(stack)-1].n.Kid(classifyPython(trimmed))
		stack = append(stack, ent{n: n, indent: w})
	}
	return root.Kids, nil
}

func (pythonCodec) Render(doc []*SrcNode) (string, error) {
	return renderCode(doc, pythonSpec)
}

// DefaultType: a fresh line in a .py session is a python statement.
func (pythonCodec) DefaultType() string { return database.TypePython }
