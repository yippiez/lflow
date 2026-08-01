package nodes

import (
	"fmt"
	"strings"

	"github.com/lflow/lflow/packages/database"
)

// markdownCodec binds a .md file to a node subtree. The mapping is the
// outline reading of a document:
//
//	# / ## / ###        → h1/h2/h3 nodes; later headings nest under the
//	                      nearest shallower heading, body lines under theirs
//	- [ ] / - [x]       → todo (completed = [x])
//	- item              → bullets; sub-items nest by 2-space indent
//	> quote             → quote
//	```fenced```        → one code node; the body (newlines included) is the name
//	---                 → divider
//	plain paragraph     → bullets (a save normalizes it to a "- " item)
//	blank line          → dropped (render re-emits structural blanks)
//
// The first save therefore normalizes prose paragraphs into list items;
// after that parse→render round-trips byte-identically.
type markdownCodec struct{}

func init() { fileCodecs = append(fileCodecs, markdownCodec{}) }

func (markdownCodec) Name() string  { return "markdown" }
func (markdownCodec) Exts() []string { return []string{".md", ".markdown"} }

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

func (markdownCodec) Parse(db *database.DB, rootUUID, src string) error {
	lines := strings.Split(src, "\n")

	// two independent nesting stacks: sections (headings) and list items.
	// headings[l] = the open heading node at level l; a non-list line attaches
	// under the deepest open heading (or the root).
	sectionParent := func(headings []string) string {
		if len(headings) > 0 {
			return headings[len(headings)-1]
		}
		return rootUUID
	}
	var headings []string   // open heading uuids, shallow→deep
	var headingLv []int     // their levels
	type listEnt struct {
		uuid   string
		indent int
	}
	var list []listEnt // open list-item chain, shallow→deep

	for i := 0; i < len(lines); i++ {
		raw := lines[i]
		line := strings.TrimRight(raw, " \t")
		trimmed := strings.TrimLeft(line, " ")
		indent := len(line) - len(trimmed)

		switch {
		case trimmed == "":
			list = nil // a blank line closes the open list

		case indent == 0 && headingLevel(trimmed) > 0:
			lv := headingLevel(trimmed)
			for len(headingLv) > 0 && headingLv[len(headingLv)-1] >= lv {
				headings = headings[:len(headings)-1]
				headingLv = headingLv[:len(headingLv)-1]
			}
			n, err := insertFileNode(db, sectionParent(headings), trimmed[lv+1:], headingTypes[lv], false)
			if err != nil {
				return err
			}
			headings = append(headings, n.UUID)
			headingLv = append(headingLv, lv)
			list = nil

		case indent == 0 && trimmed == "---":
			if _, err := insertFileNode(db, sectionParent(headings), "", database.TypeDivider, false); err != nil {
				return err
			}
			list = nil

		case strings.HasPrefix(trimmed, "```"):
			// fenced code block: gather to the closing fence, newlines and all
			var body []string
			j := i + 1
			for ; j < len(lines); j++ {
				if strings.TrimSpace(lines[j]) == "```" {
					break
				}
				body = append(body, lines[j])
			}
			i = j // skip past the closing fence (or EOF)
			if _, err := insertFileNode(db, sectionParent(headings), strings.Join(body, "\n"), database.TypeCode, false); err != nil {
				return err
			}
			list = nil

		case strings.HasPrefix(trimmed, "> "):
			if _, err := insertFileNode(db, sectionParent(headings), trimmed[2:], database.TypeQuote, false); err != nil {
				return err
			}
			list = nil

		case strings.HasPrefix(trimmed, "- ") || strings.HasPrefix(trimmed, "* "):
			text := trimmed[2:]
			typ := database.TypeBullets
			completed := false
			if strings.HasPrefix(text, "[ ] ") {
				typ, text = database.TypeTodo, text[4:]
			} else if strings.HasPrefix(text, "[x] ") || strings.HasPrefix(text, "[X] ") {
				typ, text, completed = database.TypeTodo, text[4:], true
			}
			for len(list) > 0 && list[len(list)-1].indent >= indent {
				list = list[:len(list)-1]
			}
			parent := sectionParent(headings)
			if len(list) > 0 {
				parent = list[len(list)-1].uuid
			}
			n, err := insertFileNode(db, parent, text, typ, completed)
			if err != nil {
				return err
			}
			list = append(list, listEnt{uuid: n.UUID, indent: indent})

		default:
			// plain paragraph line → a bullets node (normalized to "- " on save)
			if _, err := insertFileNode(db, sectionParent(headings), trimmed, database.TypeBullets, false); err != nil {
				return err
			}
			list = nil
		}
	}
	return nil
}

func (markdownCodec) Render(db *database.DB, rootUUID string) (string, error) {
	var out []string

	// renderList emits a node as a "- " item at the given list depth, its
	// children as nested items.
	var renderList func(n database.Node, depth int) error
	// renderBlock emits a section-level node (heading child): headings recurse
	// as sections, everything else opens a list.
	var renderBlock func(n database.Node) error

	renderList = func(n database.Node, depth int) error {
		indent := strings.Repeat("  ", depth)
		switch n.Type {
		case database.TypeTodo:
			box := "[ ]"
			if n.CompletedAt > 0 {
				box = "[x]"
			}
			out = append(out, fmt.Sprintf("%s- %s %s", indent, box, n.Name))
		default:
			out = append(out, fmt.Sprintf("%s- %s", indent, n.Name))
		}
		children, err := database.GetChildren(db, n.UUID)
		if err != nil {
			return err
		}
		for _, c := range children {
			if c.Deleted {
				continue
			}
			if err := renderList(c, depth+1); err != nil {
				return err
			}
		}
		return nil
	}

	blank := func() {
		if len(out) > 0 && out[len(out)-1] != "" {
			out = append(out, "")
		}
	}

	renderBlock = func(n database.Node) error {
		switch n.Type {
		case database.TypeH1, database.TypeH2, database.TypeH3:
			lv := map[string]int{database.TypeH1: 1, database.TypeH2: 2, database.TypeH3: 3}[n.Type]
			blank()
			out = append(out, strings.Repeat("#", lv)+" "+n.Name)
			out = append(out, "")
			children, err := database.GetChildren(db, n.UUID)
			if err != nil {
				return err
			}
			for _, c := range children {
				if c.Deleted {
					continue
				}
				if err := renderBlock(c); err != nil {
					return err
				}
			}
			return nil
		case database.TypeDivider:
			blank()
			out = append(out, "---", "")
			return nil
		case database.TypeCode:
			blank()
			out = append(out, "```")
			if n.Name != "" {
				out = append(out, strings.Split(n.Name, "\n")...)
			}
			out = append(out, "```", "")
			return nil
		case database.TypeQuote:
			out = append(out, "> "+n.Name)
			return nil
		default:
			return renderList(n, 0)
		}
	}

	children, err := database.GetChildren(db, rootUUID)
	if err != nil {
		return "", err
	}
	for _, c := range children {
		if c.Deleted {
			continue
		}
		if err := renderBlock(c); err != nil {
			return "", err
		}
	}
	// single trailing newline
	s := strings.Join(out, "\n")
	s = strings.TrimRight(s, "\n")
	if s != "" {
		s += "\n"
	}
	return s, nil
}
