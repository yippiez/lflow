package editor

import (
	"strings"

	"github.com/lflow/lflow/tui/database"
)

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

// The .py file codec: a statement composed AS an outline, the
// programming-language sibling of the math node. A node's text is one
// logical line — a compound header (`if x > 3:`) with its body as children,
// or a leaf. The first-class constructs are their own types: `def greet():`
// is a fn node with text `greet()`, `class App:` a class node with text
// `App`, `# c` a comment node — so a function is the same node in every
// language, and each codec puts its own syntax back. The plugin side (span
// coloring, alt+r export) registers the python statement node beside the
// codec in this same file.
//
// PythonSpec is exported so the editor's plugin side can render a subtree
// through the same rules the codec saves with (alt+r export).
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
// lines are empty spacer nodes in the block the FOLLOWING line belongs to, so
// spacing survives without turning the line above into a block header.
type pythonCodec struct{}

func init() {
	fileCodecs = append(fileCodecs, pythonCodec{})
	registerType(nodeType{
		key:            database.TypePython,
		label:          "Python",
		inlineEditable: true,
		spanColor: func(_ *item, runes []rune) map[int]string {
			return keywordSpanColor(pythonKeywords, "#")(runes)
		},
		bodyTail: codeBodyTail,
		run:      runCodeExport(PythonSpec),
	})
}

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

	// pending counts blank lines still waiting for a block to belong to. A
	// blank line carries no indentation of its own, so what it separates is
	// decided by the line AFTER it: the gap between two top-level defs is a
	// document-level spacer, not a child of the deeply nested last statement
	// of the first def. Placing it is therefore deferred until the next real
	// line has popped the stack to its own level. Hanging a spacer off the
	// line above instead makes that line read as a block header — its
	// `· n lines` tail counts the blanks — and a statement added beside the
	// spacer is born one level deeper: `import sys` indented under
	// `import os`, which is not python any more.
	pending := 0

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
			pending++
			continue
		}
		for len(stack) > 1 && stack[len(stack)-1].indent >= w {
			stack = stack[:len(stack)-1]
		}
		// the spacers first, into the block this line lands in
		for ; pending > 0; pending-- {
			stack[len(stack)-1].n.Kid(&SrcNode{Type: database.TypeEmpty})
		}
		n := stack[len(stack)-1].n.Kid(classifyPython(trimmed))
		stack = append(stack, ent{n: n, indent: w})
		if d, opened := scanTripleOpen(trimmed); opened {
			triple, open = d, n
		}
	}
	// blanks left pending are the file's trailing whitespace-only lines, which
	// render trims off every save — keeping them would draw editor rows that no
	// save could ever put back.
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
