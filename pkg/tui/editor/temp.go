package editor

import "github.com/lflow/lflow/pkg/tui/database"

// The Temporary Domain is an ephemeral scratch outline: a second tree with a nil
// db, so it is never persisted or synced. It is ALWAYS visible — a dashed-icon
// panel anchored at the bottom of every page, just above the status bar. Pressing
// Down past the last node of the main outline moves focus into it; Up at its top
// returns focus to the main outline. Everything edits exactly like the main
// outline, but is gone when you quit.

type tempStash struct {
	tree      *tree
	cursor    int
	caret     int
	viewStack []*item
	ancestors []string
}

// ensureTempTree creates the scratch tree if absent and guarantees it always has
// at least one (empty) node, so the persistent panel is never blank.
func (m *Model) ensureTempTree() {
	if m.tempTree == nil {
		root := &item{uuid: "temp-root", typ: database.TypeBullets}
		m.tempTree = &tree{
			root:          root,
			snapshots:     map[string]snapshot{},
			externalNames: map[string]string{},
			byUUID:        map[string]*item{root.uuid: root},
		} // db is nil → save() is a no-op, so it never persists or syncs
	}
	if len(m.tempTree.root.children) == 0 {
		_, _ = m.tempTree.insertFirstChild(m.tempTree.root) // always keep one node
	}
}

// enterTemp moves focus into the Temporary Domain panel. It is reached by pressing
// Down at the bottom of the main outline — no shortcut, no divider.
func (m *Model) enterTemp() {
	m.ensureTempTree()
	m.mainStash = tempStash{tree: m.tree, cursor: m.cursor, caret: m.caret, viewStack: m.viewStack, ancestors: m.ancestors}
	m.tree = m.tempTree
	m.viewStack = []*item{m.tempTree.root}
	m.ancestors = nil
	m.tempActive = true
	m.refreshRows()
	m.cursor = 0
	m.caret = 0
}

func (m *Model) exitTemp() {
	m.tempTree = m.tree // keep the scratch content in-memory for this session
	s := m.mainStash
	m.tree = s.tree
	m.viewStack = s.viewStack
	m.ancestors = s.ancestors
	m.cursor = s.cursor
	m.caret = s.caret
	m.tempActive = false
	m.refreshRows()
	m.clampCursor()
}

// tempRowCount is how many visible rows the scratch outline currently has.
func (m *Model) tempRowCount() int {
	if m.tempActive {
		return len(m.rows) // focused: the live rows already are the temp rows
	}
	if m.tempTree == nil {
		return 0
	}
	return len(m.tempTree.visibleRows(m.tempTree.root))
}

// tempPanelBudget is how many screen lines the persistent temp panel may occupy.
// It grows to fit all of its nodes — no artificial cap — bounded only by the
// physical screen (always leaving at least one row for the main outline).
func (m *Model) tempPanelBudget(rowBudget int) int {
	want := m.tempRowCount()
	if want < 1 {
		want = 1
	}
	if want > rowBudget-1 { // leave at least one row for the main outline
		want = rowBudget - 1
	}
	if want < 1 {
		want = 1
	}
	return want
}

// readonlyRegionLines renders a tree's visible rows as a static (no caret, no
// editing) region exactly `budget` lines tall — padded with blanks so the layout
// stays fixed and the temp panel anchors to the bottom. `dashed` swaps in the ◌
// glyph for every non-mirror node (the Temporary Domain look).
func (m *Model) readonlyRegionLines(tr *tree, viewRoot *item, cursor, budget, maxLine int, dashed bool) []string {
	if budget < 1 {
		budget = 1
	}
	var flat []string
	cursorAt := 0
	if tr != nil && viewRoot != nil {
		rows := tr.visibleRows(viewRoot)
		for i, r := range rows {
			it := r.it
			glyph, glyphColor := glyphFor(it)
			if r.mirrored {
				glyph, glyphColor = glyphMirror, cRed
			}
			if dashed && !r.mirrored {
				glyph = glyphDotted
			}
			name := tr.displayName(it)
			body := renderBody(it, name, -1, false)
			if rm := typeOf(it.typ).renderM; rm != nil {
				body = rm(m, it)
			}
			line := " " + cDim + connector(r) + glyphColor + glyph + cReset + " " + body + m.typeSuffix(it)
			below := i+1 < len(rows) && rows[i+1].depth > r.depth
			if i == cursor {
				cursorAt = len(flat)
			}
			flat = append(flat, wrapLine(line, maxLine, continuationPrefix(r, below))...)
			flat = append(flat, m.noteBandLines(r, maxLine, below, -1)...)
		}
	}

	// viewport: keep the (stashed) cursor row in view
	start := 0
	if cursorAt >= budget {
		start = cursorAt - budget + 1
	}
	if start > len(flat)-budget {
		start = len(flat) - budget
	}
	if start < 0 {
		start = 0
	}
	end := start + budget
	if end > len(flat) {
		end = len(flat)
	}
	return append([]string(nil), flat[start:end]...)
}
