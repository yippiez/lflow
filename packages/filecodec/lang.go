package filecodec

import (
	"strings"

	"github.com/lflow/lflow/packages/database"
)

// The shared code-language machinery behind the python and rust codecs: one
// logical line per node, nesting by indentation (python) or braces (rust),
// and the language-neutral construct types translated per language — fn,
// class, comment, todo, math, nlpcompute all serialize to real syntax here.
// The full matrix lives in examples/README.md.

// LangSpec parameterizes one code language. Name is exported — the nodes
// package's plugin side (alt+r export) reads it for its flash message; the
// rest stays package-private, read only by RenderCode and each codec's own
// Parse.
type LangSpec struct {
	Name        string // codec/plugin name ("python")
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
		database.TypeTodo, database.TypeMath, database.TypeNLPCompute, database.TypeCode,
		database.TypeEmpty)
}

// fallbackMarker is the comment form that carries a foreign node type through
// a code file without losing it: `# lflow <type>: <text>` — parse restores
// the node. Types with richer state (voice, image, table cells…) survive as
// their text only.
const fallbackLead = "lflow "

// RenderCode serializes a document forest for a code language. Exported: the
// nodes package's plugin side reuses it (alt+r subtree export) via the same
// LangSpec the codec renders with.
func RenderCode(doc []*SrcNode, spec LangSpec) (string, error) {
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
			emit(depth, mathExpr(n, spec.Name))
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
		case database.TypeEmpty:
			// a blank line, at any depth — the spacer's position survives
			emit(depth, "")
			kids(n, depth+1)
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

// classifyMarkerBody maps a comment/marker body (lead already stripped) onto
// the node it stands for: `nlp: …` → nlpcompute, `lflow <type>: …` → that
// type restored, and — when todos is set — TODO/DONE → todo. A bare
// `lflow <type>:` (no text, colon at end-of-string) restores with empty
// text: the space after the colon is trimmed off a blank marker on read, so
// the ": " form alone can never match it. Returns nil when body carries no
// marker; callers fall back to a plain comment. Shared by every comment-based
// codec (python/rust/toml) and, with todos=false, by markdown's HTML-comment
// markers (markdown has no comment-based TODO of its own — `- [ ]` covers it).
func classifyMarkerBody(body string, todos bool) *SrcNode {
	if todos {
		switch {
		case strings.HasPrefix(body, "TODO "):
			return &SrcNode{Type: database.TypeTodo, Text: body[len("TODO "):]}
		case body == "TODO":
			return &SrcNode{Type: database.TypeTodo}
		case strings.HasPrefix(body, "DONE "):
			return &SrcNode{Type: database.TypeTodo, Text: body[len("DONE "):], Completed: true}
		}
	}
	if rest, ok := strings.CutPrefix(body, "nlp: "); ok {
		return &SrcNode{Type: database.TypeNLPCompute, Text: rest}
	}
	if rest, ok := strings.CutPrefix(body, fallbackLead); ok {
		if i := strings.Index(rest, ": "); i > 0 && database.ValidTypes[rest[:i]] {
			return &SrcNode{Type: rest[:i], Text: rest[i+2:]}
		}
		if typ, ok := strings.CutSuffix(rest, ":"); ok && database.ValidTypes[typ] {
			return &SrcNode{Type: typ}
		}
	}
	return nil
}

// classifyComment maps a comment body (lead already stripped) onto the node
// it stands for: TODO/DONE → todo, `nlp: …` → nlpcompute, `lflow <type>: …` →
// that type restored, else a plain comment.
func classifyComment(body string) *SrcNode {
	if n := classifyMarkerBody(body, true); n != nil {
		return n
	}
	return &SrcNode{Type: database.TypeComment, Text: body}
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
