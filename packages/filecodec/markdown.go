package filecodec

import (
	"regexp"
	"strings"

	"github.com/lflow/lflow/packages/database"
)

// markdownCodec binds a .md file to a node subtree. Markdown is NOT forced
// into an outline: prose stays prose (text nodes), lists are lists, and the
// richer node types serialize to their natural markdown form:
//
//	# / ## / ###        ⇄ h1/h2/h3; body nests under its heading
//	plain line          ⇄ text (a paragraph)
//	- [ ] / - [x]       ⇄ todo; - item ⇄ bullets, nesting by 2-space indent
//	> quote             ⇄ quote
//	```lang … ```       ⇄ code node; the language tag survives in Note
//	$$ … $$             ⇄ math (renders the subtree's linear form; reopens
//	                       as a math atom holding that expression)
//	| a | b | grid      ⇄ table (columns = children, cells = their children)
//	<!-- c -->          ⇄ comment
//	<!-- nlp: i -->     ⇄ nlpcompute: instruction comment + its generated
//	  + ```lang fence      code beneath (code reopens as a code node)
//	---                 ⇄ divider
//	fn / class          → readable `fn sig` / head paragraph (no md syntax)
//	anything else       ⇄ <!-- lflow <type>: text --> marker, restored on parse
//
// Normalizations on save: one blank line between blocks, a table's title
// becomes a paragraph above its grid, children of a paragraph flatten to a
// list after it. After one save, parse→render round-trips byte-identically.
type markdownCodec struct{}

func init() { fileCodecs = append(fileCodecs, markdownCodec{}) }

func (markdownCodec) Name() string   { return "markdown" }
func (markdownCodec) Exts() []string { return []string{".md", ".markdown"} }

// Allowed: markdown accepts nearly everything; what it cannot express
// natively still round-trips through marker comments.
func (markdownCodec) Allowed() map[string]bool { return nil }

// headingLevel returns 1..3 for "# ", "## ", "### " lines, else 0.
func headingLevel(s string) int {
	n := 0
	for n < len(s) && s[n] == '#' {
		n++
	}
	if n >= 1 && n <= 3 && n < len(s) && s[n] == ' ' {
		return n
	}
	return 0
}

var headingTypes = [4]string{"", database.TypeH1, database.TypeH2, database.TypeH3}
var headingLevels = map[string]int{database.TypeH1: 1, database.TypeH2: 2, database.TypeH3: 3}

// orderedListMarker matches a GFM ordered-list item lead ("1. ", "2) ", …).
// Markdown has no ordered-list node type, so a matching line only needs to
// stop the paragraph-join loop below; it then falls through to the default
// prose case and round-trips verbatim as its own text node.
var orderedListMarker = regexp.MustCompile(`^\d+[.)] `)

func (markdownCodec) Parse(src string) ([]*SrcNode, error) {
	lines := strings.Split(src, "\n")
	root := &SrcNode{}

	// sections: the open heading chain; a block attaches under the deepest one
	type sec struct {
		n  *SrcNode
		lv int
	}
	secs := []sec{{n: root, lv: 0}}
	section := func() *SrcNode { return secs[len(secs)-1].n }
	// the open list-item chain (indent-nested)
	type li struct {
		n      *SrcNode
		indent int
	}
	var list []li
	// the open paragraph: consecutive prose lines join into ONE text node,
	// the way markdown itself wraps paragraphs
	var openPara *SrcNode

	for i := 0; i < len(lines); i++ {
		line := strings.TrimRight(lines[i], " \t")
		indent, trimmed := indentWidth(line)
		// guard list must mirror every case the switch below recognizes as its
		// own block — anything it misses silently joins into the paragraph
		// and the block's own line gets swallowed as prose text
		if trimmed != "" && openPara != nil && headingLevel(trimmed) == 0 &&
			!strings.HasPrefix(trimmed, "- ") && !strings.HasPrefix(trimmed, "* ") &&
			!strings.HasPrefix(trimmed, "> ") && !strings.HasPrefix(trimmed, "```") &&
			!strings.HasPrefix(trimmed, "<!-- ") && !strings.HasPrefix(trimmed, "| ") &&
			!orderedListMarker.MatchString(trimmed) &&
			trimmed != "---" && trimmed != "$$" {
			openPara.Text += " " + trimmed
			continue
		}
		openPara = nil

		switch {
		case trimmed == "":
			list = nil

		case indent == 0 && headingLevel(trimmed) > 0:
			lv := headingLevel(trimmed)
			for secs[len(secs)-1].lv >= lv {
				secs = secs[:len(secs)-1]
			}
			h := section().Kid(&SrcNode{Type: headingTypes[lv], Text: trimmed[lv+1:]})
			secs = append(secs, sec{n: h, lv: lv})
			list = nil

		case indent == 0 && trimmed == "---":
			section().Kid(&SrcNode{Type: database.TypeDivider})
			list = nil

		case strings.HasPrefix(trimmed, "```"):
			lang := strings.TrimSpace(trimmed[3:])
			var body []string
			j := i + 1
			for ; j < len(lines); j++ {
				if strings.TrimSpace(lines[j]) == "```" {
					break
				}
				body = append(body, lines[j])
			}
			i = j
			section().Kid(&SrcNode{Type: database.TypeCode, Text: strings.Join(body, "\n"), Note: lang})
			list = nil

		case trimmed == "$$":
			var body []string
			j := i + 1
			for ; j < len(lines); j++ {
				if strings.TrimSpace(lines[j]) == "$$" {
					break
				}
				body = append(body, strings.TrimSpace(lines[j]))
			}
			i = j
			section().Kid(&SrcNode{Type: database.TypeMath, Text: strings.Join(body, " ")})
			list = nil

		case strings.HasPrefix(trimmed, "<!-- ") && strings.HasSuffix(trimmed, " -->"):
			body := strings.TrimSuffix(strings.TrimPrefix(trimmed, "<!-- "), " -->")
			if n := classifyMarkerBody(body, false); n != nil {
				section().Kid(n)
			} else {
				section().Kid(&SrcNode{Type: database.TypeComment, Text: body})
			}
			list = nil

		case strings.HasPrefix(trimmed, "| "):
			// a GFM grid: header | separator | rows → a table node
			var grid [][]string
			j := i
			for ; j < len(lines); j++ {
				t := strings.TrimSpace(lines[j])
				if !strings.HasPrefix(t, "|") {
					break
				}
				grid = append(grid, splitTableRow(t))
			}
			i = j - 1
			section().Kid(tableFromGrid(grid))
			list = nil

		case strings.HasPrefix(trimmed, "> "):
			section().Kid(&SrcNode{Type: database.TypeQuote, Text: trimmed[2:]})
			list = nil

		case strings.HasPrefix(trimmed, "- ") || strings.HasPrefix(trimmed, "* "):
			text := trimmed[2:]
			n := &SrcNode{Type: database.TypeBullets, Text: text}
			if rest, ok := strings.CutPrefix(text, "[ ] "); ok {
				n = &SrcNode{Type: database.TypeTodo, Text: rest}
			} else if rest, ok := strings.CutPrefix(text, "[x] "); ok {
				n = &SrcNode{Type: database.TypeTodo, Text: rest, Completed: true}
			} else if rest, ok := strings.CutPrefix(text, "[X] "); ok {
				n = &SrcNode{Type: database.TypeTodo, Text: rest, Completed: true}
			}
			for len(list) > 0 && list[len(list)-1].indent >= indent {
				list = list[:len(list)-1]
			}
			parent := section()
			if len(list) > 0 {
				parent = list[len(list)-1].n
			}
			parent.Kid(n)
			list = append(list, li{n: n, indent: indent})

		default:
			// prose: a paragraph node — markdown is not forced into an outline;
			// following prose lines join it until a blank or another block
			openPara = section().Kid(&SrcNode{Type: database.TypeText, Text: trimmed})
			list = nil
		}
	}
	return root.Kids, nil
}

// splitTableRow parses `| a | b |` into cells.
func splitTableRow(s string) []string {
	s = strings.Trim(s, "|")
	parts := strings.Split(s, "|")
	out := make([]string, len(parts))
	for i, p := range parts {
		out[i] = strings.TrimSpace(p)
	}
	return out
}

// tableRowIsRule reports the |---|---| separator row.
func tableRowIsRule(cells []string) bool {
	for _, c := range cells {
		if strings.Trim(c, "-: ") != "" {
			return false
		}
	}
	return len(cells) > 0
}

// tableFromGrid builds a table node (columns = children, cells = their
// children) from a parsed GFM grid.
func tableFromGrid(grid [][]string) *SrcNode {
	t := &SrcNode{Type: database.TypeTable}
	if len(grid) == 0 {
		return t
	}
	headers := grid[0]
	rows := grid[1:]
	if len(rows) > 0 && tableRowIsRule(rows[0]) {
		rows = rows[1:]
	}
	for c, h := range headers {
		col := t.Kid(&SrcNode{Type: database.TypeBullets, Text: h})
		for _, row := range rows {
			cell := ""
			if c < len(row) {
				cell = row[c]
			}
			col.Kid(&SrcNode{Type: database.TypeBullets, Text: cell})
		}
	}
	return t
}

func (markdownCodec) Render(doc []*SrcNode) (string, error) {
	var out []string

	blank := func() {
		if len(out) > 0 && out[len(out)-1] != "" {
			out = append(out, "")
		}
	}

	var renderList func(n *SrcNode, depth int)
	renderList = func(n *SrcNode, depth int) {
		indent := strings.Repeat("  ", depth)
		switch n.Type {
		case database.TypeTodo:
			box := "[ ]"
			if n.Completed {
				box = "[x]"
			}
			out = append(out, indent+"- "+box+" "+n.Text)
		default:
			out = append(out, indent+"- "+n.Text)
		}
		for _, c := range n.Kids {
			renderList(c, depth+1)
		}
	}

	var renderBlock func(n *SrcNode)
	renderBlock = func(n *SrcNode) {
		switch n.Type {
		case database.TypeH1, database.TypeH2, database.TypeH3:
			blank()
			out = append(out, strings.Repeat("#", headingLevels[n.Type])+" "+n.Text, "")
			for _, c := range n.Kids {
				renderBlock(c)
			}
		case database.TypeText:
			blank()
			out = append(out, n.Text, "")
			for _, c := range n.Kids { // paragraph children flatten to a list after it
				renderList(c, 0)
			}
			if len(n.Kids) > 0 {
				out = append(out, "")
			}
		case database.TypeDivider:
			blank()
			out = append(out, "---", "")
			for _, c := range n.Kids {
				renderBlock(c)
			}
		case database.TypeCode:
			blank()
			out = append(out, "```"+n.Note)
			if n.Text != "" {
				out = append(out, strings.Split(n.Text, "\n")...)
			}
			out = append(out, "```", "")
			for _, c := range n.Kids {
				renderBlock(c)
			}
		case database.TypeMath:
			blank()
			out = append(out, "$$", mathLinear(n), "$$", "")
		case database.TypeComment:
			blank()
			out = append(out, "<!-- "+n.Text+" -->", "")
			for _, c := range n.Kids {
				renderBlock(c)
			}
		case database.TypeNLPCompute:
			blank()
			out = append(out, "<!-- nlp: "+n.Text+" -->")
			if n.Output != "" {
				out = append(out, "```"+n.Note)
				out = append(out, strings.Split(strings.TrimRight(n.Output, "\n"), "\n")...)
				out = append(out, "```")
			}
			out = append(out, "")
			for _, c := range n.Kids {
				renderBlock(c)
			}
		case database.TypeTable:
			blank()
			if n.Text != "" { // a table's title becomes the paragraph above it
				out = append(out, n.Text, "")
			}
			renderTable(n, &out)
			out = append(out, "")
		case database.TypeQuote:
			blank()
			out = append(out, "> "+n.Text)
			for _, c := range n.Kids {
				renderBlock(c)
			}
		case database.TypeBullets, database.TypeTodo:
			renderList(n, 0)
		case database.TypeFn:
			blank()
			out = append(out, "fn "+n.Text, "")
			for _, c := range n.Kids {
				renderBlock(c)
			}
		case database.TypeClass:
			blank()
			out = append(out, "class "+n.Text, "")
			for _, c := range n.Kids {
				renderBlock(c)
			}
		default:
			// foreign type: a marker comment carries it; children follow
			blank()
			out = append(out, "<!-- "+fallbackLead+n.Type+": "+n.Text+" -->", "")
			for _, c := range n.Kids {
				renderBlock(c)
			}
		}
	}

	for _, n := range doc {
		renderBlock(n)
	}
	return ensureTrailingNewline(out), nil
}

// renderTable draws a table node as a GFM grid: columns are children, a
// column's children are its cells top to bottom.
func renderTable(t *SrcNode, out *[]string) {
	if len(t.Kids) == 0 {
		return
	}
	headers := make([]string, len(t.Kids))
	depth := 0
	for i, col := range t.Kids {
		headers[i] = col.Text
		if len(col.Kids) > depth {
			depth = len(col.Kids)
		}
	}
	row := func(cells []string) string { return "| " + strings.Join(cells, " | ") + " |" }
	*out = append(*out, row(headers))
	rule := make([]string, len(headers))
	for i := range rule {
		rule[i] = "---"
	}
	*out = append(*out, row(rule))
	for r := 0; r < depth; r++ {
		cells := make([]string, len(t.Kids))
		for c, col := range t.Kids {
			if r < len(col.Kids) {
				cells[c] = col.Kids[r].Text
			}
		}
		*out = append(*out, row(cells))
	}
}

// DefaultType: fresh lines in a markdown session stay list items — lists are
// the editor's native gesture; prose is a /type text away.
func (markdownCodec) DefaultType() string { return "" }
