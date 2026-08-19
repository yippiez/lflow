package editor

import (
	"encoding/base64"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"unicode"

	"github.com/lflow/lflow/tui/database"
)

// Yank and cut: alt+y / alt+x act on the row selection, else the cursor
// node's subtree. With a text run live, a YANK takes the cursor node's whole
// branch — a run is a pointer at its node, and the clipboard should carry the
// tree it belongs to — while a CUT stays run-precise and splices exactly the
// selected text. ctrl+c is quit, so ctrl+x stays as the cut twin for terminals
// that swallow alt chords.
//
// The text goes to the HOST clipboard the way the image node grabs one (see
// image.go): probe the platform's tool, first hit wins. On top of that it always
// goes out as an OSC 52 escape, which is the only path that reaches the
// clipboard of the machine you are sitting at when lflow runs over SSH (tmux
// needs set-clipboard on).
//
// Nodes travel as the outline DRAWS them — rail, glyphs and all, one line per
// node (see nodesAsRail) — so what lands in the other window is the tree you
// were looking at, not a re-typing of it. Chips expand to their full value
// ("[name](url)") rather than leaking their anchor rune. Coming back the other
// way, the paste fan-out reads the rail (see readRail) and rebuilds the tree
// from it, so lflow → lflow still round-trips.

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
// row selection first, then the cursor node — copies its branches as they are
// drawn and, when cutting, removes them. A yank with a text run live takes the cursor
// node's whole branch; a cut with one stays run-precise (see copyCutRun).
func (m *Model) copyCut(cut bool) {
	verb := "copied"
	if cut {
		verb = "cut"
	}
	if cur, lo, hi, ok := m.textSelection(); ok {
		if cut {
			// cutting stays precise: the run is what the user selected
			m.copyCutRun(cur, lo, hi, cut, verb)
			return
		}
		// yanking takes the branch the run belongs to — a selection is a
		// pointer at its node, and the clipboard carries the tree
		m.clearTextSel()
		if cur := m.cursorItem(); cur != nil {
			m.copyCutRoots([]*item{cur}, cut, verb)
		}
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
	m.copyCutRoots(roots, cut, verb)
}

// copyCutRoots puts the roots' drawn subtrees on the clipboard and, when
// cutting, removes them.
func (m *Model) copyCutRoots(roots []*item, cut bool, verb string) {
	text := m.nodesAsRail(roots)
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

// nodesAsRail renders the roots and their subtrees as the outline draws them:
// the leading margin, the connector rail, each node's glyph, then its text with
// chips expanded. Colors are left behind — the clipboard carries the shape, not
// the palette.
//
// The rail is rebuilt for the COPIED forest instead of being lifted off the
// screen: the roots stand at depth 0 wherever they sat in the outline, so
// copying one nested node does not drag its ancestors' columns along. A
// collapsed node whose children are being copied wears the OPEN glyph — they
// are right there underneath it.
func (m *Model) nodesAsRail(roots []*item) string {
	var b strings.Builder
	m.walkForest(roots, func(it *item, r row, kids bool) {
		shown := m.tree.resolve(it)
		rail := " " + connector(r)
		name := expandAnchors(m.tree.displayName(it), m.chips)
		switch shown.typ {
		case database.TypeEmpty:
			// a spacer row: the rail carries through the gap, nothing else
			b.WriteString(strings.TrimRight(rail, " "))
			b.WriteByte('\n')
			return
		case database.TypeDivider:
			b.WriteString(rail + dividerRule(name))
			b.WriteByte('\n')
			return
		}
		glyph, _ := glyphFor(shown)
		if glyph == glyphCollapsed && kids {
			glyph = glyphOpen
		}
		// a multi-line name (a code block) hangs its remaining lines under the
		// text column, on the same rail the outline would draw there
		hang := stripSGR(continuationPrefix(r, kids))
		for i, line := range strings.Split(name, "\n") {
			if i == 0 {
				b.WriteString(rail + glyph + " " + line)
			} else {
				b.WriteString(hang + line)
			}
			b.WriteByte('\n')
		}
	})
	return b.String()
}

// ── reading a copied outline back in ───────────────────────────────────────

// pasteLine is one line of a paste reduced to what the fan-out needs: how deep
// it sits and what the node is called.
type pasteLine struct {
	indent int
	text   string
}

// readRail reads one line as a drawn outline row (see nodesAsRail): the rail's
// width in columns, and the text after the glyph. rail says the line is shaped
// like a drawn row; sure says nothing BUT a drawn row looks like this — it
// carries a connector, or a glyph no prose starts a word with. Ordinary text
// can accidentally satisfy rail (" a b" reads as glyph "a", text "b"), so the
// block only parses as an outline when some line is sure (see railBlock).
func readRail(line string) (pasteLine, bool, bool) {
	runes := []rune(line)
	i := 0
	for i < len(runes) && (runes[i] == ' ' || runes[i] == '│') {
		i++
	}
	if i == 0 {
		return pasteLine{}, false, false // every drawn row opens with the left margin
	}
	elbow := false
	if i+2 < len(runes) && (runes[i] == '├' || runes[i] == '╰') && runes[i+1] == '─' && runes[i+2] == ' ' {
		i, elbow = i+3, true
	}
	if i >= len(runes) {
		return pasteLine{indent: i}, true, elbow // a spacer row: rail, nothing on it
	}
	if g, n := firstCluster(runes[i:]); n > 0 && i+n < len(runes) && runes[i+n] == ' ' && isGlyph(g) {
		return pasteLine{indent: i, text: string(runes[i+n+1:])}, true, elbow || !isASCII(g)
	}
	// no glyph: a divider's rule is the row (see dividerRule)
	return pasteLine{indent: i, text: string(runes[i:])}, elbow, elbow
}

// isGlyph reports whether a cluster can be the bullet a drawn row wears. A
// letter never is, and neither is an ASCII list marker — " - item" is a pasted
// markdown list, not a rail, and its dash belongs to the text.
func isGlyph(g string) bool {
	r := []rune(g)
	if len(r) == 0 || unicode.IsLetter(r[0]) {
		return false
	}
	switch r[0] {
	case '-', '*', '+', '#', '>':
		return false
	}
	return true
}

// isASCII reports whether the cluster is plain 7-bit — a heading's digit glyph
// is, every drawn bullet (○ □ ◆ ⌕ …) and every icon is not.
func isASCII(g string) bool {
	for _, r := range g {
		if r > unicode.MaxASCII {
			return false
		}
	}
	return true
}

// railBlock reads a whole paste as a copied outline, or reports that it is not
// one. Every non-blank line has to be rail-shaped and at least one has to be
// unmistakable, so an indented list or a block of prose still pastes as the
// text it is.
func railBlock(lines []string) ([]pasteLine, bool) {
	out := make([]pasteLine, len(lines))
	sure := false
	for i, l := range lines {
		if strings.TrimSpace(l) == "" {
			continue
		}
		pl, rail, certain := readRail(l)
		if !rail {
			return nil, false
		}
		out[i] = pl
		sure = sure || certain
	}
	return out, sure
}

// pasteLines reduces a paste to one pasteLine per line: a copied outline read
// off its rail, or — for everything else — leading spaces as depth.
func readPasteLines(lines []string) []pasteLine {
	if out, ok := railBlock(lines); ok {
		return out
	}
	out := make([]pasteLine, len(lines))
	for i, l := range lines {
		text := strings.TrimLeft(l, " ")
		out[i] = pasteLine{indent: len(l) - len(text), text: text}
	}
	return out
}

// dividerRule is a divider node as plain text: a short rule, with the node's
// own text sitting on it the way the outline centers it.
func dividerRule(name string) string {
	if name == "" {
		return strings.Repeat("─", 12)
	}
	return strings.Repeat("─", 4) + " " + name + " " + strings.Repeat("─", 4)
}

// nodesAsText renders the roots and their subtrees as plain lines, two spaces
// per level, with chips expanded to their full value. This is the shape the
// paste fan-out inverts (see pasteFanOut) and the one a code block's body keeps
// (see fuseSelectionToCode); the clipboard carries the drawn rail instead.
func (m *Model) nodesAsText(roots []*item) string {
	var b strings.Builder
	m.walkForest(roots, func(it *item, r row, _ bool) {
		b.WriteString(strings.Repeat("  ", r.depth))
		b.WriteString(expandAnchors(m.tree.displayName(it), m.chips))
		b.WriteByte('\n')
	})
	return b.String()
}

// countNodes phrases the roots (and their subtrees) for the status line.
func (m *Model) countNodes(roots []*item) string {
	n := 0
	m.walkForest(roots, func(*item, row, bool) { n++ })
	if n == 1 {
		return "1 node"
	}
	return strconv.Itoa(n) + " nodes"
}

// walkForest visits each root and everything under it, depth-first, handing the
// visitor the row the node WOULD be drawn as — the roots at depth 0, each level
// carrying the branch columns its siblings imply — and whether children follow
// underneath it. A mirror carries its source's children, so the walk tracks the
// uuids on its own path: a mirror nested under its own source stops there
// instead of recursing forever, and its children are not counted as following.
func (m *Model) walkForest(roots []*item, visit func(it *item, r row, kids bool)) {
	var walk func(items []*item, depth int, branch []bool, seen map[string]bool)
	walk = func(items []*item, depth int, branch []bool, seen map[string]bool) {
		for i, it := range items {
			if seen[it.uuid] {
				continue
			}
			next := make(map[string]bool, len(seen)+1)
			for k := range seen {
				next[k] = true
			}
			next[it.uuid] = true
			var kids []*item
			for _, c := range m.tree.childItems(it) {
				if !next[c.uuid] {
					kids = append(kids, c)
				}
			}
			last := i == len(items)-1
			visit(it, row{it: it, depth: depth, last: last, branch: append([]bool(nil), branch...)}, len(kids) > 0)
			walk(kids, depth+1, append(branch, !last), next)
		}
	}
	walk(roots, 0, nil, map[string]bool{})
}
