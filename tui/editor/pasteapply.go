package editor

import (
	"sort"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lflow/lflow/tui/database"
)

// Landing a parsed paste in the outline. parsePaste (pasteparse.go) turns the
// clipboard's text into a SrcNode forest; everything here is about putting that
// forest into the tree the way a person expects a paste to behave:
//
//   - the FIRST node continues the row you pasted into, at the caret — pasting
//     into the middle of a word inserts there, as typing would;
//   - an EMPTY row also takes that node's shape, so pasting "- [x] ship it" onto
//     a fresh row gives a ticked todo rather than a bullet spelling one;
//   - everything else lands below, nesting exactly as the parser read it.
//
// A file-backed session (a .py, a .toml) restricts the types it accepts; a
// detected type outside that set degrades to a bullet instead of writing a
// heading into a Python file.

// pasteInto lands raw clipboard text at the cursor. It is the one paste entry
// point: the multi-line fan-out, the single line that carries a marker, and the
// round trip of lflow's own copy all come through here.
func (m *Model) pasteInto(cur *item, raw string) (tea.Model, tea.Cmd) {
	doc := parsePaste(raw)
	if len(doc) == 0 {
		return m, nil
	}
	first := doc[0]
	runes := []rune(cur.name)
	m.boundCaret(len(runes))
	// an untouched row takes the pasted node's whole shape; a row with text in
	// it only takes text, because the type belongs to what is already there
	head := first.Text
	if cur.name == "" && m.pasteTypeOK(cur, first.Type) {
		m.setNodeType(cur, first.Type) // the ONE path a live node changes type by
		m.applyPasteFields(cur, first)
	} else if cur.name != "" {
		// the marker could not become a type here, so it stays LITERAL: pasting
		// "- [x] done" into the middle of a sentence inserts those characters,
		// it does not quietly drop the box on the floor
		head = pasteFlat(pasteFirstLine(raw))
	} else if strings.Contains(head, "\n") {
		head = pasteFlat(head) // a block body that found no block type to live in
	}
	cur.name = string(runes[:m.caret]) + head + string(runes[m.caret:])

	last := cur
	if it, ok := m.pasteKids(cur, first.Kids); ok {
		last = it
	}
	prev := cur
	for _, n := range doc[1:] {
		it, err := m.tree.insertSiblingAfter(prev)
		if err != nil {
			// a locked parent takes no children: keep what landed, say why
			m.errorFlash(err.Error())
			break
		}
		m.applyPasteNode(it, n)
		last, prev = it, it
		if kid, ok := m.pasteKids(it, n.Kids); ok {
			last = kid
		}
	}

	m.unsaved = true
	m.refreshRows()
	m.cursor = m.rowIndexOf(last)
	m.caret = len([]rune(last.name))
	m.maybeLinkToMirror(last)
	if flash := pasteFlash(doc); flash != "" {
		m.flash = flash
	}
	return m, nil
}

// pasteKids inserts a parsed node's children under parent, depth first,
// returning the deepest-last node created so the caret can follow the paste.
func (m *Model) pasteKids(parent *item, kids []*SrcNode) (*item, bool) {
	var last *item
	var prev *item
	for _, k := range kids {
		var it *item
		var err error
		if prev == nil {
			it, err = m.tree.insertFirstChild(parent)
		} else {
			it, err = m.tree.insertSiblingAfter(prev)
		}
		if err != nil {
			m.errorFlash(err.Error())
			break
		}
		m.applyPasteNode(it, k)
		last, prev = it, it
		if deeper, ok := m.pasteKids(it, k.Kids); ok {
			last = deeper
		}
	}
	return last, last != nil
}

// applyPasteNode gives a freshly inserted item the parsed node's text and
// shape. A body that keeps its line breaks only makes sense once the type that
// holds them is allowed — a code block refused by a file session flattens
// rather than hiding half of itself below an outline row.
func (m *Model) applyPasteNode(it *item, n *SrcNode) {
	if m.pasteTypeOK(it, n.Type) {
		it.name = n.Text
		it.typ = n.Type
		m.applyPasteFields(it, n)
		return
	}
	it.name = n.Text
	if strings.Contains(it.name, "\n") {
		it.name = pasteFlat(it.name)
	}
}

// applyPasteFields carries the per-type payload a plain name cannot hold: a
// todo's tick and a code fence's language tag (which lives in the note, the
// same place the file codecs keep it). The type's own onType hook runs here
// too, so a pasted table or plot lands on its grid face exactly as it does
// when /type makes one.
func (m *Model) applyPasteFields(it *item, n *SrcNode) {
	if n.Completed {
		it.completedAt = time.Now().Unix()
	}
	if n.Note != "" && it.note == "" {
		it.note = n.Note
	}
	if h := typeOf(it.typ).onType; h != nil {
		h(m, it)
	}
}

// pasteTypeOK reports whether a detected type may be applied. A file session
// only accepts what its format can spell (FileSession.AllowedTypes), and a
// paste into the Temporary Domain — or out of it — must not smuggle in a type
// that belongs to the other side.
func (m *Model) pasteTypeOK(it *item, typ string) bool {
	if typ == "" || typ == database.TypeBullets {
		return false
	}
	if m.allowedTypes != nil && !m.allowedTypes[typ] {
		return false
	}
	if typeOf(typ).tempOnly && !m.tempActive {
		return false
	}
	return true
}

// pasteFlash names what the paste actually made — "8 nodes · 3 todo, 1 code" —
// so a detection that read the text differently than you expected is visible
// immediately instead of after scrolling. A single untyped node says nothing:
// pasting a word into a row is not an event.
func pasteFlash(doc []*SrcNode) string {
	counts := map[string]int{}
	total := 0
	var walk func(kids []*SrcNode)
	walk = func(kids []*SrcNode) {
		for _, k := range kids {
			total++
			if k.Type != "" && k.Type != database.TypeBullets {
				counts[k.Type]++
			}
			walk(k.Kids)
		}
	}
	walk(doc)
	if total < 2 && len(counts) == 0 {
		return ""
	}
	out := "pasted " + strconv.Itoa(total) + " node"
	if total != 1 {
		out += "s"
	}
	if len(counts) == 0 {
		return out
	}
	kinds := make([]string, 0, len(counts))
	for t := range counts {
		kinds = append(kinds, t)
	}
	// most-detected first, ties alphabetical, so the line is stable run to run
	sort.Slice(kinds, func(a, b int) bool {
		if counts[kinds[a]] != counts[kinds[b]] {
			return counts[kinds[a]] > counts[kinds[b]]
		}
		return kinds[a] < kinds[b]
	})
	parts := make([]string, 0, len(kinds))
	for _, t := range kinds {
		parts = append(parts, strconv.Itoa(counts[t])+" "+t)
	}
	return out + " · " + strings.Join(parts, ", ")
}

// pasteRunes is the text a key message carries, normalized when it is a paste.
// Typed runes pass through untouched — the normalization is for what arrives
// from outside, and typing must not be routed through a rewriter.
func pasteRunes(k tea.KeyMsg) string {
	if !k.Paste {
		return string(k.Runes)
	}
	return pasteBlock(string(k.Runes))
}

// pasteShapesRow reports whether a ONE-LINE paste carries enough structure to
// reshape the row it lands on. It gates the single-line path in keys.go: a
// pasted URL or a copied phrase must stay text at the caret, but a lone
// "## Findings" dropped on an empty row is a heading like any other.
func pasteShapesRow(cur *item, text string) bool {
	if cur == nil || cur.name != "" {
		return false
	}
	doc := parsePaste(text)
	if len(doc) != 1 || len(doc[0].Kids) > 0 {
		return false
	}
	t := doc[0].Type
	return t != "" && t != database.TypeBullets
}
