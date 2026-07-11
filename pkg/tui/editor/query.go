package editor

import (
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lflow/lflow/pkg/tui/database"
)

// A live-query node: its name is a search; alt+r searches the user's notes —
// both saved nodes (full live set) and unsaved ones currently in memory — and
// reconciles MIRROR children of the matches (first-order only). The mirrors are
// REAL persisted nodes, so they survive a relaunch; re-running reconciles them in
// place (add new matches, drop stale ones) to minimize churn.

const queryMaxHits = 50

func runQuery(m *Model, it *item) tea.Cmd {
	matches := m.queryMatches(it)
	m.reconcileQueryMirrors(it, matches)
	m.nodeStore(it.uuid)["queryRunAt"] = time.Now().Unix()
	m.qCrumbs = nil // ancestor names may have changed — recompute breadcrumbs
	m.unsaved = true
	m.refreshRows()
	return nil
}

// queryUpdatedAt is the unix-seconds of a query node's last run (0 if never).
func (m *Model) queryUpdatedAt(uuid string) int64 {
	v, _ := m.nodeStore(uuid)["queryRunAt"].(int64)
	return v
}

// queryMatches finds nodes matching the query language in the node's name
// (see querytime.go): text / #tag / :type: / :after: / :before: combined with
// && || > and parens. In-memory tree and DB live nodes are merged; in-memory
// wins on conflict so the freshest name is used. Results are sorted by name
// (starred first); :breadcrumb: re-sorts by ancestor path.
func (m *Model) queryMatches(q *item) []database.Node {
	now := time.Now()
	// resolve the query node's own chips/dates to plain text before parsing, so a
	// ":before:2026-06-01" the editor chipified still reads as text here.
	raw := strings.TrimSpace(database.ExpandAnchors(q.name, m.chips))
	if raw == "" {
		return nil
	}
	pq := parseQuery(raw, now)
	if pq.empty() {
		return nil
	}

	ctx := m.buildQueryCtx(q, now)
	if len(ctx.cands) == 0 {
		return nil
	}
	hitSet := pq.expr.eval(ctx)
	// search-hidden types (agent replies) only surface when the expression
	// names them via :type:
	for u := range hitSet {
		c := ctx.byUUID[u]
		if c == nil {
			delete(hitSet, u)
			continue
		}
		if typeOf(c.typ).searchHidden && !exprNamesType(pq.expr, c.typ) {
			delete(hitSet, u)
		}
	}

	var out []database.Node
	for u := range hitSet {
		c := ctx.byUUID[u]
		if c == nil || c.uuid == q.uuid {
			continue
		}
		out = append(out, database.Node{
			UUID: c.uuid, Name: c.name, Note: c.note, Type: c.typ,
			ParentUUID: c.parent, AddedOn: c.addedOn, Starred: c.starred,
		})
	}

	// /star pins first; name order within each half. Stable so ties keep UUID order.
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Starred != out[j].Starred {
			return out[i].Starred
		}
		if out[i].Name == out[j].Name {
			return out[i].UUID < out[j].UUID
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	// :breadcrumb: groups hits by their ancestor path, so same-parent matches
	// sit together and the render can show one breadcrumb per group
	if pq.breadcrumb {
		m.qCrumbs = nil
		sort.SliceStable(out, func(i, j int) bool {
			return m.crumbOf(out[i].UUID) < m.crumbOf(out[j].UUID)
		})
	}
	return out
}

// buildQueryCtx gathers every searchable candidate (in-memory + DB) and the
// parent map used by the `>` operator.
func (m *Model) buildQueryCtx(q *item, now time.Time) *qCtx {
	ctx := &qCtx{
		m:      m,
		now:    now,
		parent: map[string]string{},
		byUUID: map[string]*qCand{},
	}
	seen := map[string]bool{}

	add := func(c qCand) {
		if c.uuid == "" || c.uuid == q.uuid || c.name == "" || seen[c.uuid] {
			return
		}
		seen[c.uuid] = true
		ctx.cands = append(ctx.cands, c)
		cp := c
		ctx.byUUID[c.uuid] = &cp
		if c.parent != "" {
			ctx.parent[c.uuid] = c.parent
		}
	}

	if m.tree != nil {
		for uuid, it := range m.tree.byUUID {
			if it == q || it.mirrorOf != "" {
				continue
			}
			parent := ""
			if it.parent != nil {
				parent = it.parent.uuid
			}
			add(qCand{
				uuid: uuid, name: it.name, note: it.note, typ: it.typ,
				parent: parent, addedOn: it.addedOn, starred: it.starred,
			})
		}
	}

	if m.db != nil {
		dbm, err := database.AllLiveNodes(m.db)
		if err == nil {
			for _, mn := range dbm {
				if mn.Deleted || seen[mn.UUID] {
					continue
				}
				add(qCand{
					uuid: mn.UUID, name: mn.Name, note: mn.Note, typ: mn.Type,
					parent: mn.ParentUUID, addedOn: mn.AddedOn, starred: mn.Starred,
				})
			}
		}
	}
	return ctx
}

// exprNamesType reports whether e mentions :type:<typ> (case-insensitive).
func exprNamesType(e qExpr, typ string) bool {
	if e == nil {
		return false
	}
	typ = strings.ToLower(typ)
	switch v := e.(type) {
	case *qType:
		return strings.ToLower(v.key) == typ
	case *qOr:
		for _, k := range v.kids {
			if exprNamesType(k, typ) {
				return true
			}
		}
	case *qAnd:
		for _, k := range v.kids {
			if exprNamesType(k, typ) {
				return true
			}
		}
	case *qPipe:
		for _, k := range v.stages {
			if exprNamesType(k, typ) {
				return true
			}
		}
	}
	return false
}

// crumbOf is a node's muted ancestor breadcrumb ("inbox › work › "), memoized
// in m.qCrumbs. In-memory ancestry wins; a hit outside the loaded subtree walks
// parent uuids through the DB (bounded, so a cycle cannot hang the render).
func (m *Model) crumbOf(uuid string) string {
	if c, ok := m.qCrumbs[uuid]; ok {
		return c
	}
	var parts []string
	if it, ok := m.tree.byUUID[uuid]; ok {
		for p := it.parent; p != nil && p.parent != nil; p = p.parent {
			if n := displayAnchors(m.tree.displayName(p), m.chips); n != "" {
				parts = append([]string{n}, parts...)
			}
		}
	} else if m.db != nil {
		cur, err := database.GetNode(m.db, uuid)
		for hops := 0; err == nil && cur.ParentUUID != "" && hops < 6; hops++ {
			p, perr := database.GetNode(m.db, cur.ParentUUID)
			if perr != nil || p.ParentUUID == "" { // stop before the forest root
				break
			}
			if n := displayAnchors(p.Name, m.chips); n != "" {
				parts = append([]string{n}, parts...)
			}
			cur = p
		}
	}
	if len(parts) > 3 {
		parts = parts[len(parts)-3:] // keep the nearest ancestors when deep
	}
	crumb := ""
	if len(parts) > 0 {
		crumb = strings.Join(parts, " › ") + " › "
	}
	if m.qCrumbs == nil {
		m.qCrumbs = map[string]string{}
	}
	m.qCrumbs[uuid] = crumb
	return crumb
}

// rowCrumb is the breadcrumb a row displays: only mirror children of a
// :breadcrumb: query show one, and only the first hit of each same-path group
// — the crumb is the group header, never repeated per node.
func (m *Model) rowCrumb(rows []row, i int) string {
	it := rows[i].it
	if it.mirrorOf == "" || it.parent == nil || it.parent.typ != database.TypeQuery {
		return ""
	}
	raw := strings.ToLower(database.ExpandAnchors(it.parent.name, m.chips))
	if !strings.Contains(raw, ":breadcrumb:") && !strings.HasSuffix(raw, ":breadcrumb") {
		return ""
	}
	crumb := m.crumbOf(m.tree.sourceUUID(it))
	if crumb == "" {
		return ""
	}
	if i > 0 && rows[i-1].it.parent == it.parent && rows[i-1].it.mirrorOf != "" {
		if m.crumbOf(m.tree.sourceUUID(rows[i-1].it)) == crumb {
			return "" // same group as the hit above — the header is already shown
		}
	}
	return crumb
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
		if !m.tree.graftExternal(src) && mn.Name != "" {
			m.tree.externalNames[src] = mn.Name // ungraftable: at least the name resolves
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
