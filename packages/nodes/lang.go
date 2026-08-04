package nodes

import (
	"strings"

	"github.com/lflow/lflow/packages/database"
)

// The shared code-language machinery behind the python and rust codecs: one
// logical line per node, nesting by indentation (python) or braces (rust),
// and the language-neutral construct types translated per language — fn,
// class, comment, todo, math, nlpcompute all serialize to real syntax here.
// The full matrix lives in examples/README.md.

// langSpec parameterizes one code language.
type langSpec struct {
	name        string // codec name ("python")
	stmtType    string // the per-language statement node type (TypePython/TypeRust)
	commentLead string // "# " or "// "
	indentUnit  string // one level of indentation on render
	braces      bool   // blocks re-emit { } from structure (rust)

	// fn/class translation (see parse counterparts in each codec)
	renderFn    func(sig string) string
	renderClass func(head string) string
}

// codeAllowed is the node-type set every code language accepts.
func codeAllowed(stmtType string) map[string]bool {
	return allowed(stmtType, database.TypeFn, database.TypeClass, database.TypeComment,
		database.TypeTodo, database.TypeMath, database.TypeNLPCompute, database.TypeCode)
}

// fallbackMarker is the comment form that carries a foreign node type through
// a code file without losing it: `# lflow <type>: <text>` — parse restores
// the node. Types with richer state (voice, image, table cells…) survive as
// their text only.
const fallbackLead = "lflow "

// renderCode serializes a document forest for a code language.
func renderCode(doc []*SrcNode, spec langSpec) (string, error) {
	var out []string
	var walk func(n *SrcNode, depth int)

	emit := func(depth int, s string) {
		if s == "" {
			out = append(out, "")
			return
		}
		out = append(out, strings.Repeat(spec.indentUnit, depth)+s)
	}
	kids := func(n *SrcNode, depth int) {
		for _, k := range n.Kids {
			walk(k, depth)
		}
	}
	// a braces language re-emits { … } around a block's children
	block := func(n *SrcNode, depth int, header string) {
		if spec.braces {
			emit(depth, header+" {")
			kids(n, depth+1)
			emit(depth, "}")
			return
		}
		emit(depth, header)
		kids(n, depth+1)
	}

	walk = func(n *SrcNode, depth int) {
		text := n.Text
		switch n.Type {
		case spec.stmtType, "":
			if text == "" && len(n.Kids) == 0 {
				emit(depth, "")
				return
			}
			// Note=="block" marks a statement parsed with braces (rust) so an
			// empty block round-trips; with children, structure implies it
			if spec.braces && (len(n.Kids) > 0 || n.Note == "block") {
				block(n, depth, text)
				return
			}
			emit(depth, text)
			kids(n, depth+1)
		case database.TypeFn:
			block(n, depth, spec.renderFn(text))
		case database.TypeClass:
			block(n, depth, spec.renderClass(text))
		case database.TypeComment:
			emit(depth, spec.commentLead+text)
			kids(n, depth+1)
		case database.TypeTodo:
			mark := "TODO"
			if n.Completed {
				mark = "DONE"
			}
			emit(depth, spec.commentLead+mark+" "+text)
			kids(n, depth+1)
		case database.TypeMath:
			emit(depth, mathExpr(n, spec.name))
		case database.TypeNLPCompute:
			// the instruction as a comment, the generated code beneath it
			emit(depth, spec.commentLead+"nlp: "+text)
			if n.Output != "" {
				for _, l := range strings.Split(strings.TrimRight(n.Output, "\n"), "\n") {
					emit(depth, l)
				}
			}
		case database.TypeCode:
			// a code snippet: its multi-line body verbatim at this level
			for _, l := range strings.Split(n.Text, "\n") {
				emit(depth, l)
			}
		default:
			// foreign type: carried as a marker comment, restored on parse
			emit(depth, spec.commentLead+fallbackLead+n.Type+": "+text)
			kids(n, depth+1)
		}
	}
	for _, n := range doc {
		walk(n, 0)
	}
	return ensureTrailingNewline(out), nil
}

// classifyComment maps a comment body (lead already stripped) onto the node
// it stands for: TODO/DONE → todo, `nlp: …` → nlpcompute, `lflow <type>: …` →
// that type restored, else a plain comment.
func classifyComment(body string) *SrcNode {
	switch {
	case strings.HasPrefix(body, "TODO "):
		return &SrcNode{Type: database.TypeTodo, Text: body[len("TODO "):]}
	case body == "TODO":
		return &SrcNode{Type: database.TypeTodo}
	case strings.HasPrefix(body, "DONE "):
		return &SrcNode{Type: database.TypeTodo, Text: body[len("DONE "):], Completed: true}
	case strings.HasPrefix(body, "nlp: "):
		return &SrcNode{Type: database.TypeNLPCompute, Text: body[len("nlp: "):]}
	case strings.HasPrefix(body, fallbackLead):
		rest := body[len(fallbackLead):]
		if i := strings.Index(rest, ": "); i > 0 {
			typ := rest[:i]
			if database.ValidTypes[typ] {
				return &SrcNode{Type: typ, Text: rest[i+2:]}
			}
		}
		return &SrcNode{Type: database.TypeComment, Text: body}
	default:
		return &SrcNode{Type: database.TypeComment, Text: body}
	}
}

// indentWidth measures leading whitespace (tab = 4) and returns the trimmed line.
func indentWidth(line string) (int, string) {
	trimmed := strings.TrimLeft(line, " \t")
	w := 0
	for _, r := range line[:len(line)-len(trimmed)] {
		if r == '\t' {
			w += 4
		} else {
			w++
		}
	}
	return w, trimmed
}
