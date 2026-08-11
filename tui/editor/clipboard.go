package editor

import (
	"encoding/base64"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/lflow/lflow/tui/database"
)

// Yank and cut: y/x act once a horizontal or row selection exists. alt+y and
// alt+x do the same and, with no selection, act on the cursor node's subtree.
// ctrl+c is quit, so ctrl+x stays as the cut twin for terminals that swallow
// alt chords.
//
// The text goes to the HOST clipboard the way the image node grabs one (see
// image.go): probe the platform's tool, first hit wins. On top of that it always
// goes out as an OSC 52 escape, which is the only path that reaches the
// clipboard of the machine you are sitting at when lflow runs over SSH (tmux
// needs set-clipboard on). Nodes travel as plain text — one line per node, two
// spaces per level — so a copied subtree reads anywhere, and chips expand to
// their full value ("[name](url)") rather than leaking their anchor rune.

// clipOut is where the OSC 52 escape is written; a var so tests can capture it.
var clipOut io.Writer = os.Stdout

type clipTool struct {
	name string
	args []string
}

// clipTools is the probe order, first hit wins. A var so tests run without
// depending on what happens to be installed.
var clipTools = []clipTool{
	{"wl-copy", nil}, // Wayland
	{"pbcopy", nil},  // macOS
	{"xclip", []string{"-selection", "clipboard"}}, // X11
	{"xsel", []string{"--clipboard", "--input"}},   // X11
	{"clip.exe", nil}, // WSL/Windows
}

// osc52Max caps the escape payload — terminals drop an oversized OSC 52 write,
// and a whole subtree can be big. The tool path carries any size.
const osc52Max = 64 << 10

// clipWrite puts text on the clipboard, returning the tool that took it
// ("terminal" when only the escape went out) and whether anything did.
func clipWrite(text string) (string, bool) {
	via := ""
	for _, c := range clipTools {
		if _, err := exec.LookPath(c.name); err != nil {
			continue
		}
		cmd := exec.Command(c.name, c.args...)
		cmd.Stdin = strings.NewReader(text)
		if err := cmd.Run(); err == nil {
			via = c.name
			break
		}
	}
	if osc52(text) && via == "" {
		via = "terminal"
	}
	return via, via != ""
}

// osc52 hands the text to the terminal itself. The sequence paints nothing, so
// writing it between frames leaves the inline render untouched.
func osc52(text string) bool {
	if len(text) > osc52Max {
		return false
	}
	seq := "\x1b]52;c;" + base64.StdEncoding.EncodeToString([]byte(text)) + "\a"
	_, err := io.WriteString(clipOut, seq)
	return err == nil
}

// copyCut is alt+y / alt+x. It picks the target the same way /style does — the
// horizontal selection first, then the row selection, then the cursor node —
// copies it as plain text and, when cutting, removes it.
func (m *Model) copyCut(cut bool) {
	verb := "copied"
	if cut {
		verb = "cut"
	}
	if cur, lo, hi, ok := m.textSelection(); ok {
		m.copyCutRun(cur, lo, hi, cut, verb)
		return
	}
	roots := m.selectionRoots()
	if len(roots) == 0 {
		if cur := m.cursorItem(); cur != nil {
			roots = []*item{cur}
		}
	}
	if len(roots) == 0 {
		return
	}
	text := m.nodesAsText(roots)
	_, ok := clipWrite(text)
	if !ok {
		m.errorFlash("no clipboard (need wl-copy/xclip/pbcopy, or a terminal that takes OSC 52)")
		return
	}
	if cut {
		if !m.cutNodes(roots) {
			return
		}
	} else {
		m.clearSel()
	}
	m.flash = m.countNodes(roots) + " " + verb + " to clipboard"
}

// copyCutRun copies [lo,hi) of the node's text; cutting splices it out, taking
// any chip records inside with it and sliding the styled runs that follow.
func (m *Model) copyCutRun(cur *item, lo, hi int, cut bool, verb string) {
	runes := []rune(cur.name)
	spans := anchorSpans(runes)
	// never cut a chip anchor in half — an edge inside one takes the whole chip
	if sp := spanContaining(spans, lo); sp != nil {
		lo = sp.start
	}
	if sp := spanContaining(spans, hi); sp != nil {
		hi = sp.end
	}
	text := expandAnchors(string(runes[lo:hi]), m.chips)
	_, ok := clipWrite(text)
	if !ok {
		m.errorFlash("no clipboard (need wl-copy/xclip/pbcopy, or a terminal that takes OSC 52)")
		return
	}
	if cut {
		tgt := m.editTargetOf(cur)
		if tgt == nil {
			// a fixed row: json/code are edited only in their own editor, a query
			// result belongs to its query — the copy stands, the cut does not happen
			m.flash = "yanked · this node's text is not cut here"
			return
		}
		m.pushUndo("")
		for _, sp := range spans { // drop the chip records the cut removes
			if sp.start >= lo && sp.end <= hi {
				m.deleteChipID(sp.id)
			}
		}
		tgt.name = string(runes[:lo]) + string(runes[hi:])
		shiftSpans(tgt.uuid, lo, lo-hi)
		m.persistSpans(tgt.uuid)
		m.caret = lo
		m.unsaved = true
	}
	m.clearTextSel() // a successful yank or cut always releases the run
	m.flash = verb + " " + trimFlash(text, 24) + " to clipboard"
}

// cutNodes deletes the copied roots, deepest row last so the earlier ones keep
// their positions. It reports false when the tree refused (a locked node).
func (m *Model) cutNodes(roots []*item) bool {
	m.pushUndo("")
	before := len(m.rows)
	for i := len(roots) - 1; i >= 0; i-- {
		m.deleteNode(roots[i])
	}
	m.clearSel()
	m.cursor = clampRow(m.cursor, len(m.rows))
	m.caret = 0
	return len(m.rows) != before
}

// nodesAsText renders the roots and their subtrees as plain lines, two spaces
// per level, with chips expanded to their full value. A mirror carries its
// source's children, so the walk tracks the uuids on its own path — a mirror
// nested under its own source stops instead of recursing forever.
//
// A node whose TYPE is not a plain bullet writes itself in the plain-text
// spelling of that type — "- [x] done", "# Title", "> quoted", "$ make" — the
// markers the paste parser reads back (see pasteparse.go). That is the whole
// clipboard contract: no private format, just text that says what it is and
// that Slack, a commit message and another lflow all understand.
func (m *Model) nodesAsText(roots []*item) string {
	var b strings.Builder
	m.walkSubtrees(roots, func(it *item, depth int) {
		pad := strings.Repeat("  ", depth)
		for _, l := range nodeTextLines(it, expandAnchors(m.tree.displayName(it), m.chips)) {
			b.WriteString(pad)
			b.WriteString(l)
			b.WriteByte('\n')
		}
	})
	return b.String()
}

// nodeTextLines spells one node as clipboard text. Bullets stay bare (a copied
// outline reads as an outline, not as markdown), and a multi-line body — code,
// a JSON document — goes out FENCED, so its lines cannot be mistaken for the
// nodes that follow it.
func nodeTextLines(it *item, name string) []string {
	switch it.typ {
	case database.TypeTodo:
		box := "[ ]"
		if it.completedAt > 0 {
			box = "[x]"
		}
		return []string{"- " + box + " " + name}
	case database.TypeH1, database.TypeH2, database.TypeH3:
		return []string{strings.Repeat("#", headingLevels[it.typ]) + " " + name}
	case database.TypeQuote:
		return []string{"> " + name}
	case database.TypeDivider:
		if name == "" {
			return []string{"---"}
		}
		return []string{"--- " + name + " ---"}
	case database.TypeComment:
		return []string{"<!-- " + name + " -->"}
	case database.TypeBash:
		return []string{"$ " + name}
	case database.TypeCode, database.TypeJSON:
		lang := it.note
		if it.typ == database.TypeJSON {
			lang = "json"
		}
		out := []string{"```" + lang}
		out = append(out, strings.Split(name, "\n")...)
		return append(out, "```")
	}
	return strings.Split(name, "\n")
}

// countNodes phrases the roots (and their subtrees) for the status line.
func (m *Model) countNodes(roots []*item) string {
	n := 0
	m.walkSubtrees(roots, func(*item, int) { n++ })
	if n == 1 {
		return "1 node"
	}
	return strconv.Itoa(n) + " nodes"
}

// walkSubtrees visits each root and everything under it, depth-first, guarding
// against a mirror cycle with the set of uuids on the current path.
func (m *Model) walkSubtrees(roots []*item, visit func(it *item, depth int)) {
	var walk func(it *item, depth int, seen map[string]bool)
	walk = func(it *item, depth int, seen map[string]bool) {
		if seen[it.uuid] {
			return
		}
		visit(it, depth)
		next := make(map[string]bool, len(seen)+1)
		for k := range seen {
			next[k] = true
		}
		next[it.uuid] = true
		for _, c := range m.tree.childItems(it) {
			walk(c, depth+1, next)
		}
	}
	for _, r := range roots {
		walk(r, 0, map[string]bool{})
	}
}
