package nodes

import (
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lflow/lflow/packages/database"
	"github.com/lflow/lflow/packages/editor"
)

// The Python node: a statement composed AS an outline, the programming-language
// sibling of the math node. A node's text is one logical line — a compound
// statement header (`if x > 3:`, `def foo(a):`) with its BODY as children, or a
// simple statement leaf. The subtree renders back to real indented source:
// alt+r exports it to the run band, and `lflow file open x.py` binds a whole
// file to such a tree (see pythonCodec below).
//
// Keywords color yellow like math operators; a header row shows a dim
// `· n lines` tail for its folded body size.

// pythonKeywords drives both span coloring and nothing else — one table, like
// mathSym.
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

// pythonSpanColor tints keyword runes yellow, comments dim. Pure over the runes.
func pythonSpanColor(runes []rune) map[int]string {
	th := editor.NodeTheme()
	colors := map[int]string{}
	// a comment dims from '#' on (strings are not tracked — good enough inline)
	for i, r := range runes {
		if r == '#' {
			for j := i; j < len(runes); j++ {
				colors[j] = th.Dim
			}
			break
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
		if pythonKeywords[string(runes[i:j])] && colors[i] == "" {
			for k := i; k < j; k++ {
				colors[k] = th.Yellow
			}
		}
		i = j
	}
	return colors
}

// pythonLineCount counts the lines a subtree renders to.
func pythonLineCount(n editor.NodeRef) int {
	total := 1
	for _, c := range n.Children() {
		total += pythonLineCount(c)
	}
	return total
}

// pythonBodyTail shows a header's folded body size: `· 12 lines`. It runs on
// the structure-only render path — Children() only, never Text().
func pythonBodyTail(n editor.NodeRef) string {
	kids := n.Children()
	if len(kids) == 0 {
		return ""
	}
	th := editor.NodeTheme()
	lines := pythonLineCount(n) - 1
	word := "lines"
	if lines == 1 {
		word = "line"
	}
	return th.Dim + " · " + strconv.Itoa(lines) + " " + word + th.Reset
}

// pythonSource renders a node subtree to indented source, depth*4 spaces.
func pythonSource(n editor.NodeRef, depth int, out *[]string) {
	text := n.Text()
	if text == "" && len(n.Children()) == 0 {
		*out = append(*out, "")
	} else {
		*out = append(*out, strings.Repeat("    ", depth)+text)
	}
	for _, c := range n.Children() {
		pythonSource(c, depth+1, out)
	}
}

// runPythonSource is alt+r: export this subtree as python source to the run band.
func runPythonSource(h editor.NodeHost, n editor.NodeRef) tea.Cmd {
	var lines []string
	pythonSource(n, 0, &lines)
	editor.NodeRunOut(h, n.UUID(), lines)
	h.NodeFlash("python → output")
	return nil
}

func init() {
	editor.RegisterNodePlugin(editor.NodePlugin{
		Key:            database.TypePython,
		Label:          "Python",
		InlineEditable: true,
		SpanColor:      pythonSpanColor,
		BodyTail:       pythonBodyTail,
		Run:            runPythonSource,
		ToContext: func(h editor.NodeHost, n editor.NodeRef) (string, string, string) {
			return "python", "", n.Text()
		},
	})
}

// ── the .py file codec ──────────────────────────────────────────────────────

// pythonCodec binds a .py file to a python-node tree: one node per logical
// line, nesting by indentation. Rendering re-indents to 4 spaces per level —
// a first save normalizes a differently-indented file, after which
// parse→render round-trips byte-identically. Blank lines are empty nodes at
// top level, so section spacing survives. Continuation lines inside
// triple-quoted strings follow plain indentation rules: content is never
// lost, but a docstring indented off the 4-space grid is re-aligned on save.
type pythonCodec struct{}

func init() { fileCodecs = append(fileCodecs, pythonCodec{}) }

func (pythonCodec) Name() string   { return "python" }
func (pythonCodec) Exts() []string { return []string{".py"} }

func (pythonCodec) Parse(db *database.DB, rootUUID, src string) error {
	lines := strings.Split(strings.TrimRight(src, "\n"), "\n")
	type ent struct {
		uuid   string
		indent int
	}
	stack := []ent{{uuid: rootUUID, indent: -1}}

	for _, raw := range lines {
		line := strings.TrimRight(raw, " \t")
		trimmed := strings.TrimLeft(line, " \t")
		if trimmed == "" {
			// blank line: an empty node under the previous line's node, so it
			// renders back in place ("" at any depth) without ending the block
			// (a blank inside a body does not dedent in Python)
			if _, err := insertFileNode(db, stack[len(stack)-1].uuid, "", database.TypePython, false); err != nil {
				return err
			}
			continue
		}
		// tabs count as 4 for nesting decisions
		indent := 0
		for _, r := range line[:len(line)-len(trimmed)] {
			if r == '\t' {
				indent += 4
			} else {
				indent++
			}
		}
		for len(stack) > 1 && stack[len(stack)-1].indent >= indent {
			stack = stack[:len(stack)-1]
		}
		n, err := insertFileNode(db, stack[len(stack)-1].uuid, trimmed, database.TypePython, false)
		if err != nil {
			return err
		}
		stack = append(stack, ent{uuid: n.UUID, indent: indent})
	}
	return nil
}

func (pythonCodec) Render(db *database.DB, rootUUID string) (string, error) {
	var out []string
	var walk func(uuid string, depth int) error
	walk = func(uuid string, depth int) error {
		children, err := database.GetChildren(db, uuid)
		if err != nil {
			return err
		}
		for _, c := range children {
			if c.Deleted {
				continue
			}
			if c.Name == "" {
				out = append(out, "")
			} else {
				out = append(out, strings.Repeat("    ", depth)+c.Name)
			}
			if err := walk(c.UUID, depth+1); err != nil {
				return err
			}
		}
		return nil
	}
	if err := walk(rootUUID, 0); err != nil {
		return "", err
	}
	s := strings.Join(out, "\n")
	s = strings.TrimRight(s, "\n \t")
	if s != "" {
		s += "\n"
	}
	return s, nil
}
