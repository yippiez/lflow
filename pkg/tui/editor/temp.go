package editor

import (
	"github.com/lflow/lflow/pkg/tui/database"
)

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

// crossToNotes moves a top-level node out of the Temporary Domain into the main
// notes (under the current main view root) and follows it there — so alt+shift+up
// at the top of the domain feels like moving across one continuous space. The node
// and its subtree migrate in-memory (byUUID + snapshots) from the temp tree to the
// main tree; the next save reparents it in the DB (the migrated snapshot's old
// parent makes save UPDATE rather than re-INSERT).
func (m *Model) crossToNotes(cur *item) {
	if m.mainStash.tree == nil || len(m.mainStash.viewStack) == 0 {
		return
	}
	dst := m.mainStash.viewStack[len(m.mainStash.viewStack)-1]
	if dst == nil {
		return
	}
	if idx := indexOf(cur); idx >= 0 {
		cur.parent.children = append(cur.parent.children[:idx], cur.parent.children[idx+1:]...)
	}
	var migrate func(it *item)
	migrate = func(it *item) {
		delete(m.tempTree.byUUID, it.uuid)
		m.mainStash.tree.byUUID[it.uuid] = it
		if s, ok := m.tempTree.snapshots[it.uuid]; ok {
			delete(m.tempTree.snapshots, it.uuid)
			m.mainStash.tree.snapshots[it.uuid] = s
		}
		for _, c := range it.children {
			migrate(c)
		}
	}
	migrate(cur)
	cur.parent = dst
	dst.children = append(dst.children, cur)

	m.undoStack = nil // the active tree changes; cross-tree undo would corrupt
	m.exitTemp()      // back to the notes, with the moved node now in them
	if r := m.rowIndexOf(cur); r >= 0 {
		m.cursor = r
	}
	m.clampCursor()
	m.unsaved = true
	m.flash = "moved to notes"
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

// atTopOfTempList reports whether Up should cross from the temp space back into
// the main outline — true at the first temp row.
func (m *Model) atTopOfTempList() bool {
	return m.tempActive && m.cursor == 0
}

// tempRowCount is how many visible rows the scratch outline currently has.
func (m *Model) tempRowCount() int {
	if m.tempActive {
		return len(m.rows) // focused: the live rows already are the temp rows
	}
	if m.tempTree == nil {
		return 0
	}
	return len(m.tempTree.visibleRows(m.tempTree.root, m.hideCompleted))
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
		rows := tr.visibleRows(viewRoot, m.hideCompleted)
		for i, r := range rows {
			it := r.it
			below := i+1 < len(rows) && rows[i+1].depth > r.depth
			// a divider is a full-width rule, not a glyph+body node; render it as the
			// rule here too so the read-only region keeps it (never the cursor color)
			if it.typ == database.TypeDivider {
				if i == cursor {
					cursorAt = len(flat)
				}
				flat = append(flat, dividerLine(r, maxLine, false))
				flat = append(flat, m.noteBandLines(r, maxLine, below, -1)...)
				continue
			}
			glyph, glyphColor := glyphFor(it)
			if dashed && !r.mirrored {
				glyph = glyphDotted
			}
			name := tr.displayName(it)
			body := renderBody(it, name, -1, false, m.chips, false)
			if rm := typeOf(it.typ).renderM; rm != nil {
				body = rm(m, it)
			}
			line := " " + cDim + connector(r) + glyphColor + glyph + cReset + " " + body + m.typeSuffix(it)
			if i == cursor {
				cursorAt = len(flat)
			}
			flat = append(flat, wrapLine(line, maxLine, continuationPrefix(r, below))...)
			flat = append(flat, m.noteBandLines(r, maxLine, below, -1)...)
			// a bash/query node's run output hangs beneath it in the read-only region too
			flat = append(flat, m.runBandLines(r, below, maxLine)...)
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
