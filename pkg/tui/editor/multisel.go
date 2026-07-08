package editor

// Multi-select: shift+↑/↓ grows a contiguous row selection from the cursor;
// esc (or any plain movement/typing) drops it. Structural operations act on the
// selection ROOTS — the selected items whose parent is not itself selected — so
// selecting a parent carries its subtree exactly once. Type/style apply to
// every selected node. The selection is view-state only: nothing about it is
// persisted.

// startOrExtendSel begins a selection at the cursor (first shift+arrow) — the
// caller then moves the cursor to grow it.
func (m *Model) startOrExtendSel() {
	if !m.selOn {
		m.selOn = true
		m.selAnchor = m.cursor
	}
}

// clearSel drops the selection.
func (m *Model) clearSel() {
	m.selOn = false
}

// selectionBounds returns the ordered [lo, hi] row range of the selection.
func (m *Model) selectionBounds() (int, int) {
	lo, hi := m.selAnchor, m.cursor
	if lo > hi {
		lo, hi = hi, lo
	}
	if lo < 0 {
		lo = 0
	}
	if hi >= len(m.rows) {
		hi = len(m.rows) - 1
	}
	return lo, hi
}

// inSelection reports whether row i is inside the active selection.
func (m *Model) inSelection(i int) bool {
	if !m.selOn {
		return false
	}
	lo, hi := m.selectionBounds()
	return i >= lo && i <= hi
}

// selectedItems returns every item in the selected rows, in row order.
func (m *Model) selectedItems() []*item {
	if !m.selOn {
		return nil
	}
	lo, hi := m.selectionBounds()
	var out []*item
	for i := lo; i <= hi && i < len(m.rows); i++ {
		out = append(out, m.rows[i].it)
	}
	return out
}

// selectionRoots returns the selected items whose parent is not selected —
// the units structural ops move/indent/delete (children ride along).
func (m *Model) selectionRoots() []*item {
	items := m.selectedItems()
	inSel := map[*item]bool{}
	for _, it := range items {
		inSel[it] = true
	}
	var roots []*item
	for _, it := range items {
		if !inSel[it.parent] {
			roots = append(roots, it)
		}
	}
	return roots
}

// reselect re-anchors the selection on the given items after a structural op
// reshuffled the rows (the block stays highlighted where it landed).
func (m *Model) reselect(first, last *item) {
	f, l := m.rowIndexOf(first), m.rowIndexOf(last)
	if f < 0 || l < 0 {
		m.clearSel()
		return
	}
	m.selOn = true
	m.selAnchor = f
	m.cursor = l
}

// selIndent indents the whole selection under the first root's previous
// sibling — the group form of tab.
func (m *Model) selIndent() {
	roots := m.selectionRoots()
	if len(roots) == 0 {
		return
	}
	first := roots[0]
	idx := indexOf(first)
	if idx <= 0 {
		return // nothing above to indent under
	}
	dest := first.parent.children[idx-1]
	m.pushUndo("")
	// reparent prepends, so walking the roots in reverse preserves their order
	for i := len(roots) - 1; i >= 0; i-- {
		m.tree.reparent(roots[i], dest)
	}
	dest.collapsed = false
	m.unsaved = true
	m.refreshRows()
	m.reselect(roots[0], roots[len(roots)-1])
}

// selOutdent outdents every selection root — the group form of shift+tab.
func (m *Model) selOutdent() {
	roots := m.selectionRoots()
	if len(roots) == 0 {
		return
	}
	mc := m.mirrorContext()
	m.pushUndo("")
	moved := false
	// outdent places a node right after its old parent: reverse order keeps the
	// block's own order intact
	for i := len(roots) - 1; i >= 0; i-- {
		if m.tree.outdent(roots[i], mc.localRoot) {
			moved = true
		}
	}
	if !moved {
		return
	}
	m.unsaved = true
	m.refreshRows()
	m.reselect(roots[0], roots[len(roots)-1])
}

// selMove moves the whole selection up (-1) or down (+1) among its siblings.
func (m *Model) selMove(delta int) {
	roots := m.selectionRoots()
	if len(roots) == 0 {
		return
	}
	// the boundary root must be able to move or the block stays put
	boundary := roots[0]
	if delta > 0 {
		boundary = roots[len(roots)-1]
	}
	if idx := indexOf(boundary); idx < 0 ||
		(delta < 0 && idx == 0) ||
		(delta > 0 && idx >= len(boundary.parent.children)-1) {
		return
	}
	m.pushUndo("")
	if delta < 0 {
		for _, r := range roots {
			m.tree.move(r, delta, m.viewRoot())
		}
	} else {
		for i := len(roots) - 1; i >= 0; i-- {
			m.tree.move(roots[i], delta, m.viewRoot())
		}
	}
	m.unsaved = true
	m.refreshRows()
	m.reselect(roots[0], roots[len(roots)-1])
}

// selDelete deletes every selection root (subtrees included).
func (m *Model) selDelete() {
	roots := m.selectionRoots()
	if len(roots) == 0 {
		return
	}
	m.pushUndo("")
	for i := len(roots) - 1; i >= 0; i-- {
		m.deleteNode(roots[i])
	}
	m.clearSel()
	m.cursor = clampRow(m.cursor, len(m.rows))
}

// selHasChildren reports whether any selection root carries a subtree (which
// makes delete ask for confirmation first).
func (m *Model) selHasChildren() bool {
	for _, r := range m.selectionRoots() {
		if len(r.children) > 0 {
			return true
		}
	}
	return false
}
