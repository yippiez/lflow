package filecodec

import (
	"strings"

	"github.com/lflow/lflow/packages/database"
)

// The .py file codec: a statement composed AS an outline, the
// programming-language sibling of the math node. A node's text is one
// logical line — a compound header (`if x > 3:`) with its body as children,
// or a leaf. The first-class constructs are their own types: `def greet():`
// is a fn node with text `greet()`, `class App:` a class node with text
// `App`, `# c` a comment node — so a function is the same node in every
// language, and each codec puts its own syntax back. The plugin side (span
// coloring, alt+r export) lives in packages/nodes/python.go and reuses
// PythonSpec through RenderCode.

// PythonSpec is exported so the nodes package's plugin side can render a
// subtree through the same rules the codec saves with (alt+r export).
var PythonSpec = LangSpec{
	Name:        "python",
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

// pythonCodec binds a .py file to a node tree: constructs become their
// first-class nodes, everything else is a python statement node, nesting by
// indentation. Rendering re-indents to 4 spaces per level — a first save
// normalizes, after which parse→render round-trips byte-identically. Blank
// lines are empty spacer nodes under the previous line, so spacing survives.
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

	// triple != "" while inside a multi-line string literal: those lines
	// attach VERBATIM (raw, untrimmed, blanks included) to the statement
	// that opened them rather than through the indentation grid, so a
	// docstring's interior column positions survive the 4-space renormalize.
	var triple string
	var open *SrcNode

	for _, raw := range strings.Split(strings.TrimRight(src, "\n"), "\n") {
		if triple != "" {
			open.Text += "\n" + raw
			if strings.Contains(raw, triple) {
				triple = ""
				open = nil
			}
			continue
		}
		line := strings.TrimRight(raw, " \t")
		w, trimmed := indentWidth(line)
		if trimmed == "" {
			// blank: an empty node under the previous line, renders back in
			// place ("" at any depth) without ending the block
			stack[len(stack)-1].n.Kid(&SrcNode{Type: database.TypeEmpty})
			continue
		}
		for len(stack) > 1 && stack[len(stack)-1].indent >= w {
			stack = stack[:len(stack)-1]
		}
		n := stack[len(stack)-1].n.Kid(classifyPython(trimmed))
		stack = append(stack, ent{n: n, indent: w})
		if d, opened := scanTripleOpen(trimmed); opened {
			triple, open = d, n
		}
	}
	return root.Kids, nil
}

// scanTripleOpen scans a fresh statement line (one not already inside a
// multi-line string) for a triple-quote that opens without closing again on
// the same line. A simple ' / " scanner (backslash-escaped) keeps ordinary
// string content from being mistaken for the delimiter, and a bare `#`
// outside any string stops the scan at the comment leader.
func scanTripleOpen(line string) (delim string, opened bool) {
	for i := 0; i < len(line); {
		switch {
		case strings.HasPrefix(line[i:], `"""`), strings.HasPrefix(line[i:], `'''`):
			d := line[i : i+3]
			if j := strings.Index(line[i+3:], d); j >= 0 {
				i += 3 + j + 3
				continue
			}
			return d, true
		case line[i] == '#':
			return "", false
		case line[i] == '\'' || line[i] == '"':
			q := line[i]
			j := i + 1
			for j < len(line) && line[j] != q {
				if line[j] == '\\' {
					j++
				}
				j++
			}
			i = j + 1
		default:
			i++
		}
	}
	return "", false
}

func (pythonCodec) Render(doc []*SrcNode) (string, error) {
	return RenderCode(doc, PythonSpec)
}

// DefaultType: a fresh line in a .py session is a python statement.
func (pythonCodec) DefaultType() string { return database.TypePython }
