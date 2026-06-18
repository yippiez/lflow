package editor

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/lflow/lflow/pkg/tui/database"
)

// A live-query node: its name is a search; alt+r searches the user's notes and
// reconciles read-only MIRROR children of the matches (first-order only). The
// mirrors are derived/ephemeral (never persisted or synced) and regenerated on
// each run, so moved/renamed findings are always reflected.

const queryMaxHits = 50

func runQuery(m *Model, it *item) tea.Cmd {
	matches, err := database.SearchNodes(m.db, it.name, true)
	if err != nil {
		m.err = err
		return nil
	}
	m.reconcileQueryMirrors(it, matches)
	m.refreshRows()
	return nil
}

// reconcileQueryMirrors rebuilds the query node's first-order mirror children from
// the matches: drop the previous derived mirrors, mirror each current match.
func (m *Model) reconcileQueryMirrors(q *item, matches []database.Node) {
	var kept []*item
	for _, c := range q.children {
		if c.derived {
			delete(m.tree.byUUID, c.uuid)
		} else {
			kept = append(kept, c) // preserve any real children
		}
	}
	n := 0
	for _, mn := range matches {
		if mn.UUID == q.uuid || mn.Deleted || mn.Name == "" {
			continue // skip self, tombstones, and empty/derived rows
		}
		child, err := m.tree.newItem()
		if err != nil {
			break
		}
		child.mirrorOf = mn.UUID
		child.derived = true
		child.isNew = false
		child.collapsed = true // show the hit as one line, not the whole subtree
		child.parent = q
		m.tree.externalNames[mn.UUID] = mn.Name // resolve the mirror's display name
		kept = append(kept, child)
		if n++; n >= queryMaxHits {
			break
		}
	}
	q.children = kept
}

// queryHitCount counts a query node's derived mirror children.
func queryHitCount(q *item) int {
	n := 0
	for _, c := range q.children {
		if c.derived {
			n++
		}
	}
	return n
}
