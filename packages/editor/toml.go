package editor

import (
	"strings"

	"github.com/lflow/lflow/packages/database"
)

// tomlCodec binds a .toml config file to a node subtree, line-based:
//
//	[section] / [[table]]  ⇄ a text node (brackets kept) with its keys as children
//	key = value            ⇄ a text leaf, stored verbatim
//	# comment              ⇄ a comment node
//	anything else          ⇄ a text leaf verbatim (multi-line strings/arrays
//	                         pass through untouched, one node per line)
//
// Sections stay flat like TOML itself ([a.b] is its own header, not nested
// under [a]). Todos are allowed as `# TODO …` comments. Rendering emits one
// blank line before each section; other spacing is normalized away.
type tomlCodec struct{}

func init() { fileCodecs = append(fileCodecs, tomlCodec{}) }

func (tomlCodec) Name() string   { return "toml" }
func (tomlCodec) Exts() []string { return []string{".toml"} }
func (tomlCodec) Allowed() map[string]bool {
	return allowed(database.TypeText, database.TypeComment, database.TypeTodo)
}

func isTomlSection(s string) bool {
	return strings.HasPrefix(s, "[") && strings.HasSuffix(s, "]")
}

func (tomlCodec) Parse(src string) ([]*SrcNode, error) {
	root := &SrcNode{}
	cur := root // the open section

	// triple != "" while inside a """/''' multi-line string: those lines
	// attach VERBATIM to the key that opened them — TrimSpace'ing every line
	// (and dropping blanks) would delete the string's own interior
	// indentation and blank lines, and an interior line shaped like [x]
	// would misparse as a section header.
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
		trimmed := strings.TrimSpace(raw)
		switch {
		case trimmed == "":
			// spacing regenerates on render
		case isTomlSection(trimmed):
			cur = root.Kid(&SrcNode{Type: database.TypeText, Text: trimmed})
		case strings.HasPrefix(trimmed, "# "):
			cur.Kid(classifyComment(trimmed[2:]))
		case trimmed == "#":
			cur.Kid(&SrcNode{Type: database.TypeComment})
		default:
			n := cur.Kid(&SrcNode{Type: database.TypeText, Text: trimmed})
			if d, opened := scanTripleOpen(trimmed); opened {
				triple, open = d, n
			}
		}
	}
	return root.Kids, nil
}

func (tomlCodec) Render(doc []*SrcNode) (string, error) {
	var out []string
	var walk func(n *SrcNode)
	walk = func(n *SrcNode) {
		switch n.Type {
		case database.TypeComment:
			out = append(out, "# "+n.Text)
		case database.TypeTodo:
			mark := "TODO"
			if n.Completed {
				mark = "DONE"
			}
			out = append(out, "# "+mark+" "+n.Text)
		case database.TypeText, "":
			if isTomlSection(strings.TrimSpace(n.Text)) && len(out) > 0 && out[len(out)-1] != "" {
				out = append(out, "")
			}
			out = append(out, n.Text)
		default:
			// foreign type: a marker comment carries it, restored on parse
			out = append(out, "# "+fallbackLead+n.Type+": "+n.Text)
		}
		for _, c := range n.Kids {
			walk(c)
		}
	}
	for _, n := range doc {
		walk(n)
	}
	return ensureTrailingNewline(out), nil
}

// DefaultType: a fresh line in a .toml session is a text line.
func (tomlCodec) DefaultType() string { return database.TypeText }
