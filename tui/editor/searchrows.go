// The generated-row plumbing the SEARCH-OUTWARD nodes share: the web node
// (webnode.go, SearxNG) and the archive node (archivenode.go, archive.org).
// Both have the same shape — the name is the search, alt+r runs it, the hits
// hang under it as REAL child nodes of that search's own generated type — so
// the rows are made, replaced and counted here once, keyed by type. Replacing
// BY TYPE is what lets a re-run touch only the rows the last run made: a note
// filed under the search, or a hit moved out from under it to keep, survives.

package editor

// searchRow is one generated hit row: its display text and its link target.
type searchRow struct {
	text, url string
}

// setSearchRows swaps a search node's children of typ for one row per hit,
// leaving everything else alone. It returns the number of rows made.
func (m *Model) setSearchRows(q *item, rows []searchRow, typ string) int {
	var kept []*item
	for _, c := range q.children {
		if c.typ == typ {
			m.dropGeneratedRow(c)
			continue
		}
		kept = append(kept, c)
	}
	made := make([]*item, 0, len(rows))
	for _, r := range rows {
		c, err := m.tree.newItem()
		if err != nil {
			break
		}
		c.parent = q
		c.typ = typ
		c.name = r.text
		if r.url != "" {
			c.name = m.createLabeledChip(chipKindLink, r.url, r.text)
		}
		made = append(made, c)
	}
	q.children = append(made, kept...)
	m.unsaved = true
	m.refreshRows()
	return len(made)
}

// dropGeneratedRow tombstones one generated row and its subtree, releasing the
// chips its name owned so a re-run cannot leak chip records.
func (m *Model) dropGeneratedRow(it *item) {
	for _, c := range it.children {
		m.dropGeneratedRow(c)
	}
	for _, sp := range anchorSpans([]rune(it.name)) {
		m.deleteChipID(sp.id)
	}
	m.tombstoneItem(it)
}

// searchResultCount counts the generated rows of typ hanging under a search
// node (direct children only).
func searchResultCount(q *item, typ string) int {
	n := 0
	for _, c := range q.children {
		if c.typ == typ {
			n++
		}
	}
	return n
}

// searchRunAt is the unix-seconds of a search node's last run (0 if never). The
// store key is per search kind ("webRunAt", "archiveRunAt"), so a node retyped
// from one search to the other does not inherit the other's timestamp.
func (m *Model) searchRunAt(uuid, key string) int64 {
	v, _ := m.nodeStore(uuid)[key].(int64)
	return v
}
