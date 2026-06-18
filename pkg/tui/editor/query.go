package editor

import (
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lflow/lflow/pkg/tui/database"
)

// A live-query node: its name is a search; alt+r searches the user's notes —
// both saved nodes (FTS over the DB) and unsaved ones currently in memory — and
// reconciles MIRROR children of the matches (first-order only). The mirrors are
// REAL persisted nodes, so they survive a relaunch; re-running reconciles them in
// place (add new matches, drop stale ones) to minimize churn.

const queryMaxHits = 50

func runQuery(m *Model, it *item) tea.Cmd {
	matches := m.queryMatches(it)
	m.reconcileQueryMirrors(it, matches)
	if m.queryRunAt == nil {
		m.queryRunAt = map[string]int64{}
	}
	m.queryRunAt[it.uuid] = time.Now().Unix()
	m.unsaved = true
	m.refreshRows()
	return nil
}

// queryMatches finds nodes whose name or note contains the query, merging the
// in-memory tree (so unsaved nodes are found) with the DB's full-text search (so
// nodes outside the loaded subtree are found too). In-memory wins on conflict so
// the freshest name is used. Results are sorted by name for a stable order.
func (m *Model) queryMatches(q *item) []database.Node {
	query := strings.TrimSpace(q.name)
	if query == "" {
		return nil
	}
	lc := strings.ToLower(query)
	seen := map[string]bool{}
	var out []database.Node

	// in-memory nodes (covers unsaved edits and brand-new nodes)
	for uuid, it := range m.tree.byUUID {
		if it == q || it.mirrorOf != "" || it.name == "" {
			continue // skip self, mirror rows, and empty/derived names
		}
		if strings.Contains(strings.ToLower(it.name), lc) || strings.Contains(strings.ToLower(it.note), lc) {
			out = append(out, database.Node{UUID: uuid, Name: it.name})
			seen[uuid] = true
		}
	}

	// saved nodes from the DB (may live outside the loaded subtree)
	if dbm, err := database.SearchNodes(m.db, query, true); err == nil {
		for _, mn := range dbm {
			if seen[mn.UUID] || mn.Deleted || mn.Name == "" || mn.UUID == q.uuid {
				continue
			}
			out = append(out, mn)
			seen[mn.UUID] = true
		}
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].Name == out[j].Name {
			return out[i].UUID < out[j].UUID
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out
}

// reconcileQueryMirrors brings the query node's mirror children in line with the
// current matches: existing mirrors that still match are kept in place, new
// matches get a fresh mirror, and mirrors whose source no longer matches are
// tombstoned. Non-mirror (user-added) children are preserved untouched.
func (m *Model) reconcileQueryMirrors(q *item, matches []database.Node) {
	want := map[string]database.Node{}
	var order []string
	for _, mn := range matches {
		if mn.UUID == q.uuid {
			continue
		}
		if _, dup := want[mn.UUID]; dup {
			continue
		}
		want[mn.UUID] = mn
		order = append(order, mn.UUID)
		if len(order) >= queryMaxHits {
			break
		}
	}

	// index the query node's existing mirror children by their source uuid;
	// collect user (non-mirror) children to preserve as-is.
	existing := map[string]*item{}
	var others []*item
	for _, c := range q.children {
		if c.mirrorOf == "" {
			others = append(others, c)
			continue
		}
		src := m.tree.sourceUUID(c)
		if _, kept := existing[src]; kept || want[src].UUID == "" {
			m.tombstoneItem(c) // a stale or duplicate query mirror
			continue
		}
		existing[src] = c
	}

	var kids []*item
	for _, src := range order {
		mn := want[src]
		if mn.Name != "" {
			m.tree.externalNames[src] = mn.Name // so the mirror resolves its name
		}
		if c, ok := existing[src]; ok {
			kids = append(kids, c)
			continue
		}
		child, err := m.tree.newItem()
		if err != nil {
			break
		}
		child.mirrorOf = src
		child.collapsed = true // show the hit as one line, not its whole subtree
		child.parent = q
		kids = append(kids, child)
	}
	q.children = append(kids, others...)
}

// tombstoneItem detaches a (mirror) node from the tree, recording it for deletion
// on the next save if it was already persisted.
func (m *Model) tombstoneItem(it *item) {
	if !it.isNew {
		m.tree.deleted = append(m.tree.deleted, it.uuid)
	}
	delete(m.tree.byUUID, it.uuid)
}

// queryHitCount counts a query node's mirror children (its results).
func queryHitCount(q *item) int {
	n := 0
	for _, c := range q.children {
		if c.mirrorOf != "" {
			n++
		}
	}
	return n
}
