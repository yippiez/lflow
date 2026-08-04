package nodes

import (
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lflow/lflow/packages/database"
	"github.com/lflow/lflow/packages/editor"
	"github.com/lflow/lflow/packages/filecodec"
)

// The Python node: a statement composed AS an outline, the
// programming-language sibling of the math node. A node's text is one logical
// line — a compound header (`if x > 3:`) with its body as children, or a leaf.
// The first-class constructs are their own types: `def greet():` is a fn node
// with text `greet()`, `class App:` a class node with text `App`, `# c` a
// comment node — so a function is the same node in every language, and each
// codec puts its own syntax back. alt+r exports any subtree as real indented
// source; `lflow file open x.py` binds a whole file (packages/filecodec).
// Keywords color yellow like math operators; a header row shows a dim
// `· n lines` tail.

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
func refToSrc(n editor.NodeRef) *filecodec.SrcNode {
	s := &filecodec.SrcNode{Type: n.Type(), Text: n.Text()}
	for _, c := range n.Children() {
		s.Kids = append(s.Kids, refToSrc(c))
	}
	return s
}

// runCodeExport is alt+r for a language statement node: export the subtree as
// real source to the run band.
func runCodeExport(spec filecodec.LangSpec) func(h editor.NodeHost, n editor.NodeRef) tea.Cmd {
	return func(h editor.NodeHost, n editor.NodeRef) tea.Cmd {
		src, err := filecodec.RenderCode([]*filecodec.SrcNode{refToSrc(n)}, spec)
		if err != nil {
			h.NodeFlash("export: " + err.Error())
			return nil
		}
		editor.NodeRunOut(h, n.UUID(), strings.Split(strings.TrimRight(src, "\n"), "\n"))
		h.NodeFlash(spec.Name + " → output")
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
		Run:            runCodeExport(filecodec.PythonSpec),
	})
}
