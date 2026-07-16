package editor

import (
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/lflow/lflow/pkg/tui/database"
	"github.com/lflow/lflow/pkg/utils"
	"github.com/lflow/lflow/pkg/utils/browser"
	"github.com/pkg/errors"
)

func (m *Model) openFinder(act finderAction) {
	m.mode = modeFinder
	m.finder.open(m, act, nodeFinderBackend{})
}

// nodeFinderBackend is the finderBackend that fronts the outline's nodes: it
// searches the DB (plus the Agent Domain for /move:here), commits a pick via
// runFinder, and links a URL query straight to a website for "[[".
type nodeFinderBackend struct{}

func (nodeFinderBackend) search(m *Model, query string) []finderRow {
	cur := m.cursorItem()
	var hits []database.Node
	var err error

	if m.finder.act == actBacklinks {
		// /backlinks lists only nodes that reference the cursor node — mirrors
		// and [[ link chips — rather than the whole outline. Resolve mirrors so
		// a backlinks look at a through-row still finds the original's refs.
		if m.db == nil {
			return nil
		}
		srcUUID := ""
		if cur != nil {
			srcUUID = m.tree.sourceUUID(cur)
		}
		hits, err = database.BacklinkNodes(m.db, srcUUID)
		if err != nil {
			return nil
		}
	} else if strings.TrimSpace(query) == "" {
		// an empty query matches everything, recent first: the picker starts full and
		// narrows as you type. :in: additionally offers the otherwise-hidden Root,
		// because it is the explicit spelling of the default query scope.
		hits, err = database.RecentNodes(m.db)
		if m.finder.act == actQueryScope && m.db != nil {
			if root, rootErr := database.GetNode(m.db, database.RootUUID); rootErr == nil {
				hits = append([]database.Node{root}, hits...)
			}
		}
	} else {
		hits, err = database.SearchNodes(m.db, query, true)
	}
	if err != nil {
		return nil
	}

	q := strings.ToLower(strings.TrimSpace(query))
	var rows []finderRow
	for _, h := range hits {
		// the node being acted on is never a valid target
		if cur != nil && h.UUID == cur.uuid {
			continue
		}
		if m.finder.act == actBacklinks {
			// backlinks KEEP mirrors (they are the references) and empty-name
			// mirror rows; only search-hidden types stay out. An optional query
			// filters by the resolved display name.
			if typeOf(h.Type).searchHidden {
				continue
			}
			if q != "" {
				name := finderRowName(h, func(uuid string) (database.Node, bool) {
					n, e := database.GetNode(m.db, uuid)
					return n, e == nil
				})
				if !strings.Contains(strings.ToLower(name), q) {
					continue
				}
			}
		} else {
			// every other picker hides empty nodes (noise), mirror rows (a pick
			// on a mirror resolves to its original anyway, so listing both has
			// no value), and search-hidden types (agent replies)
			if h.Name == "" || h.MirrorOf != "" || typeOf(h.Type).searchHidden {
				continue
			}
		}
		rows = append(rows, m.finderRowFor(h))
	}
	// /move:here can also pull a node out of the (ephemeral, DB-less) Temporary Domain,
	// so surface its nodes alongside the saved nodes — most recent first.
	if m.finder.act == actBringHere {
		var temp []finderRow
		for _, n := range m.tempFinderHits(cur, query) {
			temp = append(temp, m.finderRowFor(n))
		}
		rows = append(temp, rows...)
	}
	// picker rank: 1) starred  2) more children (subtree weight)  3) newer
	sort.SliceStable(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if a.node.Starred != b.node.Starred {
			return a.node.Starred
		}
		if a.count != b.count {
			return a.count > b.count
		}
		return a.node.EditedOn > b.node.EditedOn
	})
	return rows
}

func (nodeFinderBackend) onSelect(m *Model, row finderRow) (tea.Model, tea.Cmd) {
	return m.runFinder(row.node)
}

func (nodeFinderBackend) interceptEnter(m *Model, query string) (bool, tea.Model, tea.Cmd) {
	// [[ accepts a URL typed/pasted straight into the query — link to the website
	// instead of a node
	if m.finder.act == actLinkInsert && browser.IsURL(query) {
		mm, cmd := m.insertURLLink(query)
		return true, mm, cmd
	}
	return false, m, nil
}

func (nodeFinderBackend) queryAffordance(m *Model, query string) string {
	if m.finder.act == actLinkInsert && browser.IsURL(query) {
		return cAccent + " ↵ " + cReset + cDim + "link to " + cFG + browser.Normalize(query) + cReset
	}
	return ""
}

func (nodeFinderBackend) label(m *Model) string {
	switch m.finder.act {
	case actMirrorHere:
		return "/mirror:from"
	case actMirrorFrom:
		return "/mirror:to"
	case actMoveTo:
		return "/move:to"
	case actGoto:
		return "/goto"
	case actBringHere:
		return "/move:here"
	case actLinkInsert:
		return "[[ link"
	case actBacklinks:
		return "/backlinks"
	case actQueryScope:
		return ":in:"
	}
	return ""
}

func (nodeFinderBackend) hint(m *Model) string {
	switch m.finder.act {
	case actMirrorHere:
		return "enter · Mirror that node here"
	case actMirrorFrom:
		return "enter · Mirror this node there"
	case actMoveTo:
		return "enter · Move this node there"
	case actGoto:
		return "enter · Open"
	case actBringHere:
		return "enter · Move that node here"
	case actLinkInsert:
		return "enter · Link to node, or type a URL"
	case actBacklinks:
		return "enter · Open · mirrors and [[ links to this node"
	case actQueryScope:
		return "enter · Search this node and its subtree"
	}
	return ""
}

// finderRowFor decorates a node with its subtree count for the finder list. A
// count error (or a synthetic Agent-Domain node not in the DB) falls back to 1,
// matching the pre-refactor lazy count.
func (m *Model) finderRowFor(n database.Node) finderRow {
	count, err := database.CountSubtree(m.db, n.UUID)
	if err != nil {
		count = 1
	}
	return finderRow{node: n, count: count}
}

// tempFinderHits returns the Temporary Domain's named nodes as finder candidates,
// synthesized as database.Node so they sit in the same picker list as saved nodes.
// Empty (unnamed) nodes and the cursor node are skipped.
func (m *Model) tempFinderHits(cur *item, query string) []database.Node {
	if m.tempTree == nil || m.tempTree == m.tree {
		return nil // no domain, or we're already inside it
	}
	q := strings.ToLower(strings.TrimSpace(query))
	var hits []database.Node
	for _, it := range m.tempTree.root.children {
		name := strings.TrimSpace(it.name)
		if name == "" || (cur != nil && it.uuid == cur.uuid) || typeOf(it.typ).searchHidden {
			continue
		}
		if q != "" && !strings.Contains(strings.ToLower(name), q) {
			continue
		}
		hits = append(hits, database.Node{UUID: it.uuid, Name: it.name, Type: it.typ})
	}
	return hits
}

func (m *Model) runFinder(target database.Node) (tea.Model, tea.Cmd) {
	m.mode = modeOutline
	cur := m.cursorItem()
	if cur == nil {
		return m, nil
	}

	switch m.finder.act {
	case actMirrorHere:
		m.pushUndo("")
		target = m.resolveSourceNode(target)
		if cur.name == "" && cur.mirrorOf == "" && len(cur.children) == 0 {
			// the empty node where "/" was typed becomes the mirror
			cur.mirrorOf = target.UUID
		} else {
			it, err := m.tree.insertSiblingAfter(cur)
			if err != nil {
				m.err = err
				return m.quit()
			}
			it.mirrorOf = target.UUID
			m.refreshRows()
			m.cursor = m.rowIndexOf(it)
		}
		if !m.tree.graftExternal(target.UUID) {
			m.tree.externalNames[target.UUID] = target.Name // ungraftable: name stub
		}
		m.unsaved = true
	case actMirrorFrom:
		// the dual of /mirror:to: plant a mirror OF this node at the top of the
		// picked target's children (matching /move:to), original stays put
		m.pushUndo("")
		src := m.tree.resolve(cur)
		srcUUID := src.uuid
		if src.mirrorOf != "" {
			// cur mirrors an ungrafted node: follow the chain in the DB so the
			// new mirror points at the real original
			orig := m.resolveSourceNode(database.Node{UUID: src.uuid, MirrorOf: src.mirrorOf})
			srcUUID = orig.UUID
		}
		if targetItem, inTree := m.tree.byUUID[target.UUID]; inTree {
			it, err := m.tree.newItem()
			if err != nil {
				m.err = err
				return m.quit()
			}
			it.mirrorOf = srcUUID
			it.parent = targetItem
			m.tree.insertChildAt(targetItem, 0, it)
			m.tree.graftExternal(srcUUID) // no-op when the source is loaded
			m.unsaved = true
		} else if err := m.mirrorToDB(srcUUID, target); err != nil {
			m.err = err
			return m.quit()
		}
		m.flash = "mirrored → " + clipStr(target.Name, 24)
	case actMoveTo:
		m.pushUndo("")
		// after a move the cursor stays put visually: it lands on the row that
		// slid up into the moved node's old place, so you keep working in flow
		oldRow := m.rowIndexOf(cur)
		movers := []*item{cur}
		if m.selOn {
			movers = m.selectionRoots() // /move carries the whole selection
			if row := m.rowIndexOf(movers[0]); row >= 0 {
				oldRow = row
			}
			m.clearSel()
		}
		if targetItem, inTree := m.tree.byUUID[target.UUID]; inTree {
			// the group lands where the target's priority points, order intact
			moved := m.tree.reparentAll(movers, targetItem)
			if moved {
				m.unsaved = true
				m.refreshRows()
				m.cursor = clampRow(oldRow, len(m.rows))
			}
		} else {
			// moving out of the open subtree: persist everything, then move in db.
			// A priority-up target stacks each move on top, so the walk runs
			// reversed there to keep the block's own order (mirrors reparentAll).
			if target.Priority == database.PriorityUp {
				for i, j := 0, len(movers)-1; i < j; i, j = i+1, j-1 {
					movers[i], movers[j] = movers[j], movers[i]
				}
			}
			for _, mv := range movers {
				if err := m.moveToDB(mv, target); err != nil {
					m.err = err
					return m.quit()
				}
			}
			m.cursor = clampRow(oldRow, len(m.rows))
		}
	case actGoto, actBacklinks:
		// save, then reopen on the target (/backlinks picks jump the same way)
		if _, err := m.saveAll(); err != nil {
			m.err = err
			return m.quit()
		}
		t, err := loadTree(m.db, target.UUID)
		if err != nil {
			m.err = err
			return m.quit()
		}
		m.tree = t
		m.viewStack = []*item{t.root}
		m.undoStack = nil
		m.refreshAncestors()
		m.cursor = 0
		m.caret = 0
		m.unsaved = false
	case actQueryScope:
		// Persist the selected node identity as a regular node-link chip. Query
		// parsing consumes this chip after :in:, so a rename cannot change scope.
		dst := m.resolveSourceNode(target)
		label := displayAnchors(dst.Name, m.chips)
		anchor := m.createLabeledChip(chipKindLink, nodeLinkURI(dst.UUID), label)
		if anchor != "" {
			m.pushUndo("")
			runes := []rune(cur.name)
			m.boundCaret(len(runes))
			cur.name = string(runes[:m.caret]) + anchor + " " + string(runes[m.caret:])
			m.caret += len([]rune(anchor)) + 1
			m.unsaved = true
			m.flash = "query in → " + clipStr(label, 24)
		}
	case actLinkInsert:
		// insert an inline link chip pointing at the picked node (the original,
		// never a mirror), its name defaulting to the node's name. Resolve the
		// target's chip anchors to display text first: a node whose title carries
		// a chip (e.g. a #tag) stores a raw "￼id￼" anchor in its name, and that
		// must never become a link label — it leaks the chip id and corrupts the
		// editor's anchor invariant (see createLabeledChip's sentinel guard).
		dst := m.resolveSourceNode(target)
		label := displayAnchors(dst.Name, m.chips)
		m.insertLinkChip(nodeLinkURI(dst.UUID), label)
		m.flash = "linked → " + clipStr(label, 24)
	case actBringHere:
		// move the picked node (and its subtree) to the cursor location.
		m.pushUndo("")
		if src, ok := m.tempTree.byUUID[target.UUID]; ok && m.tempTree != m.tree {
			m.bringFromTemp(src, cur) // pull a node out of the Temporary Domain
		} else if it, inTree := m.tree.byUUID[target.UUID]; inTree {
			m.bringWithin(it, cur) // already in the open subtree
		} else if err := m.bringFromDB(target, cur); err != nil {
			m.err = err
			return m.quit()
		}
	}

	m.refreshRows()
	return m, nil
}

// clampRow bounds a row index into [0, n-1] (0 when the list is empty).
func clampRow(i, n int) int {
	if i >= n {
		i = n - 1
	}
	if i < 0 {
		i = 0
	}
	return i
}

// mirrorToDB plants a mirror of srcUUID under a target that lives outside the
// open subtree, where the target's priority points (up = top, down = bottom).
// Everything saves first so the new row lands next to a persisted original.
func (m *Model) mirrorToDB(srcUUID string, target database.Node) error {
	if _, err := m.saveAll(); err != nil {
		return err
	}
	m.unsaved = false

	uuid, err := utils.GenerateUUID()
	if err != nil {
		return errors.Wrap(err, "generating uuid")
	}
	rank, err := database.PlaceRank(m.db, target.UUID)
	if err != nil {
		return err
	}
	now := time.Now().UnixNano()
	n := database.Node{
		UUID: uuid, ParentUUID: target.UUID, Rank: rank,
		Type: database.TypeBullets, MirrorOf: srcUUID,
		Priority: database.PriorityUp, // new nodes default up
		AddedOn:  now, EditedOn: now,
	}
	return errors.Wrap(n.Insert(m.db), "mirroring node")
}

func (m *Model) moveToDB(cur *item, target database.Node) error {
	if _, err := m.saveAll(); err != nil {
		return err
	}
	m.unsaved = false

	// /move:to drops the node where the target's priority points: up = top,
	// down = bottom of its children
	rank, err := database.PlaceRank(m.db, target.UUID)
	if err != nil {
		return err
	}
	if err := database.Reparent(m.db, cur.uuid, target.UUID, rank); err != nil {
		return errors.Wrap(err, "moving node")
	}

	// detach from the in-memory tree without tombstoning
	if idx := indexOf(cur); idx >= 0 {
		cur.parent.children = append(cur.parent.children[:idx], cur.parent.children[idx+1:]...)
	}
	m.refreshRows()
	return nil
}

// placeBrought splices an already-detached subtree in as a sibling right after cur,
// registers it (and its descendants) in the current tree's index, and moves the
// cursor onto it. Used by /bring once the source has been unhooked from its origin.
func (m *Model) placeBrought(it, cur *item) {
	parent := cur.parent
	it.parent = parent
	m.tree.insertChildAt(parent, indexOf(cur)+1, it)

	var reg func(x *item)
	reg = func(x *item) {
		m.tree.byUUID[x.uuid] = x
		for _, c := range x.children {
			reg(c)
		}
	}
	reg(it)

	m.unsaved = true
	m.refreshRows()
	m.cursor = m.rowIndexOf(it)
	m.flash = "brought here"
}

// bringFromTemp migrates a node (and its subtree) out of the Temporary Domain
// into the main tree at the cursor. Any live process keeps running — the run
// machinery is keyed by uuid, not by which tree owns the node.
func (m *Model) bringFromTemp(src, cur *item) {
	if idx := indexOf(src); idx >= 0 {
		src.parent.children = append(src.parent.children[:idx], src.parent.children[idx+1:]...)
	}
	var migrate func(x *item)
	migrate = func(x *item) {
		delete(m.tempTree.byUUID, x.uuid)
		if s, ok := m.tempTree.snapshots[x.uuid]; ok {
			delete(m.tempTree.snapshots, x.uuid)
			m.tree.snapshots[x.uuid] = s
		}
		for _, c := range x.children {
			migrate(c)
		}
	}
	migrate(src)
	m.placeBrought(src, cur)
}

// bringWithin relocates a node already loaded in the open subtree to sit right after
// cur. Bringing a node into its own subtree is a no-op.
func (m *Model) bringWithin(it, cur *item) {
	for p := cur; p != nil; p = p.parent {
		if p == it {
			m.flash = "can't bring a node into itself"
			return
		}
	}
	if idx := indexOf(it); idx >= 0 {
		it.parent.children = append(it.parent.children[:idx], it.parent.children[idx+1:]...)
	}
	// placeBrought re-splices after cur and re-registers the subtree in byUUID;
	// this node is already indexed there, so the re-register is a harmless no-op.
	m.placeBrought(it, cur)
}

// bringFromDB moves a node that lives elsewhere in the database under the cursor's
// parent, then reloads the open view so the brought subtree appears. Like moveToDB
// but in the opposite direction (target → here rather than here → target).
func (m *Model) bringFromDB(target database.Node, cur *item) error {
	if _, err := m.saveAll(); err != nil {
		return err
	}
	m.unsaved = false

	parentUUID := cur.parent.uuid
	rank, err := database.NextRank(m.db, parentUUID)
	if err != nil {
		return err
	}
	if err := database.Reparent(m.db, target.UUID, parentUUID, rank); err != nil {
		return errors.Wrap(err, "bringing node")
	}

	root := m.viewRoot()
	t, err := loadTree(m.db, root.uuid)
	if err != nil {
		return err
	}
	m.tree = t
	m.viewStack = []*item{t.root}
	m.refreshAncestors()
	m.refreshRows()
	if it, ok := t.byUUID[target.UUID]; ok {
		m.cursor = m.rowIndexOf(it)
	}
	m.clampCursor()
	m.flash = "brought here"
	return nil
}

// finderRowName resolves the name shown for a finder row. A mirror node
// carries an empty name in the database, so follow its mirror_of chain to
// the source node and show that name, suffixed to mark it a mirror. resolve
// looks up a node by uuid; a missing source falls back to a placeholder.
func finderRowName(n database.Node, resolve func(string) (database.Node, bool)) string {
	if n.MirrorOf == "" {
		return n.Name
	}
	// the start node is in hand, so serve its mirror_of directly rather than
	// resolve(n.UUID); the walk then follows via the resolve callback.
	term := followMirrorChain(n.UUID, func(uuid string) (string, bool) {
		if uuid == n.UUID {
			return n.MirrorOf, true
		}
		src, ok := resolve(uuid)
		if !ok {
			return "", false
		}
		return src.MirrorOf, true
	})
	src, ok := resolve(term)
	if !ok {
		return "(missing) - mirror"
	}
	return src.Name + " - mirror"
}

func (m *Model) viewFinder(maxLine int) []string {
	return m.finder.view(m, nodeFinderBackend{}, maxLine)
}
