package editor

import (
	"time"

	"github.com/lflow/lflow/pkg/tui/database"
	"github.com/lflow/lflow/pkg/utils"
	"github.com/pkg/errors"
)

// item is an in-memory outline node.
type item struct {
	uuid        string
	name        string
	note        string
	typ         string
	style       string // comma-separated style tokens, e.g. "bold,color:blue"
	mirrorOf    string
	completedAt int64
	children    []*item
	parent      *item
	collapsed   bool
	readonly    bool  // node lock: inline edits are no-ops (see canEdit)
	starred     bool  // /star: pinned to the top of the node pickers
	addedOn     int64 // creation time (UnixNano); shown by the log node's time chip
	isNew       bool
}

// snapshot captures a node's persisted state for change detection on save.
type snapshot struct {
	parentUUID  string
	rank        int
	name        string
	note        string
	typ         string
	style       string
	mirrorOf    string
	completedAt int64
	collapsed   bool
	readonly    bool
	starred     bool
}

// tree is the in-memory model of the subtree being edited.
type tree struct {
	db        *database.DB
	root      *item
	snapshots map[string]snapshot
	deleted   []string // uuids of pre-existing nodes deleted in this session
	// resolved names for mirrors whose originals are outside the tree
	externalNames map[string]string
	byUUID        map[string]*item
	// defaultType is the node type new items get (empty = bullets). Currently
	// nothing sets it — it's a hook for a tree to default new nodes to a type
	// other than bullets.
	defaultType string
}

// loadTree loads the subtree rooted at rootUUID into memory.
// cloneItem deep-copies an item subtree, re-linking parents, for the undo stack.
func cloneItem(src, parent *item) *item {
	if src == nil {
		return nil
	}
	c := &item{
		uuid:        src.uuid,
		name:        src.name,
		note:        src.note,
		typ:         src.typ,
		style:       src.style,
		mirrorOf:    src.mirrorOf,
		completedAt: src.completedAt,
		collapsed:   src.collapsed,
		readonly:    src.readonly,
		starred:     src.starred,
		addedOn:     src.addedOn,
		isNew:       src.isNew,
		parent:      parent,
	}
	for _, ch := range src.children {
		c.children = append(c.children, cloneItem(ch, c))
	}
	return c
}

// rebuildByUUID re-indexes byUUID from the current item tree, used after an undo
// swaps in a restored tree. externalNames and snapshots are left untouched.
func (t *tree) rebuildByUUID() {
	t.byUUID = map[string]*item{}
	var walk func(it *item)
	walk = func(it *item) {
		if it.uuid != "" {
			t.byUUID[it.uuid] = it
		}
		for _, c := range it.children {
			walk(c)
		}
	}
	walk(t.root)
}

func loadTree(db *database.DB, rootUUID string) (*tree, error) {
	nodes, err := database.GetSubtree(db, rootUUID)
	if err != nil {
		return nil, err
	}

	t := &tree{
		db:            db,
		snapshots:     map[string]snapshot{},
		externalNames: map[string]string{},
		byUUID:        map[string]*item{},
	}

	items := map[string]*item{}
	for _, n := range nodes {
		items[n.UUID] = &item{
			uuid:        n.UUID,
			name:        n.Name,
			note:        n.Note,
			typ:         n.Type,
			style:       n.Style,
			mirrorOf:    n.MirrorOf,
			completedAt: n.CompletedAt,
			collapsed:   n.Collapsed,
			readonly:    n.Readonly,
			starred:     n.Starred,
			addedOn:     n.AddedOn,
		}
		t.snapshots[n.UUID] = snapshot{
			parentUUID:  n.ParentUUID,
			rank:        n.Rank,
			name:        n.Name,
			note:        n.Note,
			typ:         n.Type,
			style:       n.Style,
			mirrorOf:    n.MirrorOf,
			completedAt: n.CompletedAt,
			collapsed:   n.Collapsed,
			readonly:    n.Readonly,
			starred:     n.Starred,
		}
	}

	for _, n := range nodes {
		it := items[n.UUID]
		if n.UUID == rootUUID {
			t.root = it
			continue
		}
		parent := items[n.ParentUUID]
		if parent == nil {
			parent = items[rootUUID]
		}
		it.parent = parent
		parent.children = append(parent.children, it)
	}

	t.byUUID = items

	// resolve mirror originals that live outside this subtree
	for _, it := range items {
		if it.mirrorOf == "" {
			continue
		}
		if _, inTree := items[it.mirrorOf]; inTree {
			continue
		}
		orig, err := database.GetNode(db, it.mirrorOf)
		if err == nil {
			t.externalNames[it.mirrorOf] = orig.Name
		} else {
			t.externalNames[it.mirrorOf] = "(missing)"
		}
	}

	return t, nil
}

// followMirrorChain walks a node's mirror_of chain from startUUID to its
// terminal identity — the uuid whose node is not a mirror, cannot be looked up,
// or closes a cycle — and returns that uuid. next(uuid) yields the node's
// mirror_of and whether it was found; a seen-set guards against mirror cycles.
// The three mirror resolvers (sourceUUID here, resolveSourceNode over the DB,
// finderRowName over a callback) all delegate to this one walk.
func followMirrorChain(startUUID string, next func(uuid string) (mirrorOf string, ok bool)) string {
	seen := map[string]bool{}
	cur := startUUID
	for {
		mirrorOf, ok := next(cur)
		if !ok || mirrorOf == "" || seen[cur] {
			return cur
		}
		seen[cur] = true
		cur = mirrorOf
	}
}

// sourceUUID resolves the ultimate non-mirror node a new mirror should
// point at. Mirroring a mirror must follow the chain to the original so
// the new mirror's name resolves, rather than landing on an intermediate
// mirror whose name is empty.
func (t *tree) sourceUUID(it *item) string {
	return followMirrorChain(it.uuid, func(uuid string) (string, bool) {
		n, ok := t.byUUID[uuid]
		if !ok {
			return "", false
		}
		return n.mirrorOf, true
	})
}

// displayName resolves the visible name of an item: mirrors show the
// original's name (same node everywhere).
func (t *tree) displayName(it *item) string {
	if it.mirrorOf == "" {
		return it.name
	}
	if orig, ok := t.byUUID[it.mirrorOf]; ok {
		return orig.name
	}
	return t.externalNames[it.mirrorOf]
}

// resolve returns the live source item a mirror stands for, so content edits
// act on the one real node — same node everywhere. A non-mirror, or a mirror
// whose source lives outside the loaded subtree, returns itself.
func (t *tree) resolve(it *item) *item {
	if it == nil || it.mirrorOf == "" {
		return it
	}
	if src, ok := t.byUUID[it.mirrorOf]; ok {
		return src
	}
	return it
}

// displayNote resolves the visible note of an item: a mirror shows its
// original's live note, so an unsaved edit on the source shows through at once.
// When the source is outside the loaded subtree we query the DB for its current
// note rather than fall back to a stale copy on the mirror row.
func (t *tree) displayNote(it *item) string {
	if it.mirrorOf == "" {
		return it.note
	}
	if src, ok := t.byUUID[it.mirrorOf]; ok {
		return src.note
	}
	if n, err := database.GetNode(t.db, it.mirrorOf); err == nil {
		return n.Note
	}
	return ""
}

// row is a visible line of the outline.
type row struct {
	it       *item
	depth    int
	last     bool   // last child of its parent (elbow connector)
	branch   []bool // for each ancestor level: does it have later siblings (draw │)
	mirrored bool   // shown through a mirror: same node, rendered with the ◆ glyph
	ctx      *item  // the mirror this row is shown under, nil at the real location
}

// childItems returns the children to display under it. An expanded mirror shows
// its source's live children so the same node appears everywhere — edits in
// either spot act on the one real node; a normal node shows its own. A mirror
// whose source is outside the loaded subtree shows nothing through.
func (t *tree) childItems(it *item) []*item {
	if it.mirrorOf == "" {
		return it.children
	}
	if src, ok := t.byUUID[it.mirrorOf]; ok {
		return src.children
	}
	return nil
}

// cloneSeen copies a path-visited set so each branch of the walk tracks its own
// ancestors independently when guarding against mirror cycles.
func cloneSeen(m map[*item]bool) map[*item]bool {
	n := make(map[*item]bool, len(m)+1)
	for k := range m {
		n[k] = true
	}
	return n
}

// expandTarget is the node whose children a row expands: a mirror expands its
// source, a normal node expands itself. nil means nothing to expand. The walk
// stops re-expanding a target already on the path so a mirror pointing at an
// ancestor renders as a leaf instead of looping forever.
func (t *tree) expandTarget(it *item) *item {
	if it.mirrorOf == "" {
		return it
	}
	if src, ok := t.byUUID[it.mirrorOf]; ok {
		return src
	}
	return nil
}

// visibleRows flattens the tree below viewRoot into displayable rows,
// honoring collapsed state. The view root itself is not a row. When
// hideCompleted is set, completed nodes (and their subtrees) are skipped —
// the /hide toggle.
func (t *tree) visibleRows(viewRoot *item, hideCompleted bool) []row {
	var rows []row
	var walk func(it *item, depth int, branch []bool, mirrored bool, ctx *item, seen map[*item]bool)
	walk = func(it *item, depth int, branch []bool, mirrored bool, ctx *item, seen map[*item]bool) {
		kids := t.childItems(it)
		// when filtering completed, drop them from the sibling list first so
		// last/branch connectors join the remaining incomplete siblings cleanly
		if hideCompleted {
			filtered := make([]*item, 0, len(kids))
			for _, c := range kids {
				if c.completedAt == 0 {
					filtered = append(filtered, c)
				}
			}
			kids = filtered
		}
		for i, c := range kids {
			last := i == len(kids)-1
			cm := mirrored || c.mirrorOf != ""
			rows = append(rows, row{it: c, depth: depth, last: last, branch: append([]bool(nil), branch...), mirrored: cm, ctx: ctx})
			tgt := t.expandTarget(c)
			if c.collapsed || tgt == nil || seen[tgt] {
				continue
			}
			// crossing into a mirror moves its subtree into that mirror's local
			// context so the cursor can stay there instead of leaking to the original
			childCtx := ctx
			if c.mirrorOf != "" {
				childCtx = c
			}
			next := cloneSeen(seen)
			next[tgt] = true
			walk(c, depth+1, append(branch, !last), cm, childCtx, next)
		}
	}
	walk(viewRoot, 0, nil, false, nil, map[*item]bool{viewRoot: true})
	return rows
}

// allRows flattens the whole loaded tree ignoring collapsed state: the
// scrollback dump on quit is the complete outline, not the current folding.
func (t *tree) allRows() []row {
	var rows []row
	var walk func(it *item, depth int, branch []bool, mirrored bool, ctx *item, seen map[*item]bool)
	walk = func(it *item, depth int, branch []bool, mirrored bool, ctx *item, seen map[*item]bool) {
		kids := t.childItems(it)
		for i, c := range kids {
			last := i == len(kids)-1
			cm := mirrored || c.mirrorOf != ""
			rows = append(rows, row{it: c, depth: depth, last: last, branch: append([]bool(nil), branch...), mirrored: cm, ctx: ctx})
			tgt := t.expandTarget(c)
			if tgt == nil || seen[tgt] {
				continue
			}
			childCtx := ctx
			if c.mirrorOf != "" {
				childCtx = c
			}
			next := cloneSeen(seen)
			next[tgt] = true
			walk(c, depth+1, append(branch, !last), cm, childCtx, next)
		}
	}
	walk(t.root, 0, nil, false, nil, map[*item]bool{t.root: true})
	return rows
}

// indexOf finds the index of it among its parent's children.
func indexOf(it *item) int {
	if it.parent == nil {
		return -1
	}
	for i, c := range it.parent.children {
		if c == it {
			return i
		}
	}
	return -1
}

// newItem creates a fresh local node.
func (t *tree) newItem() (*item, error) {
	uuid, err := utils.GenerateUUID()
	if err != nil {
		return nil, errors.Wrap(err, "generating uuid")
	}
	typ := database.TypeBullets
	if t.defaultType != "" {
		typ = t.defaultType // a tree may default new nodes to a non-bullets type
	}
	it := &item{uuid: uuid, typ: typ, addedOn: time.Now().UnixNano(), isNew: true}
	t.byUUID[uuid] = it
	return it, nil
}

// insertChildAt splices it into parent.children at position idx (0..len),
// shifting the existing children from idx onward down one slot. "after cur" is
// indexOf(cur)+1; "before cur" is indexOf(cur). The caller owns it.parent.
func (t *tree) insertChildAt(parent *item, idx int, it *item) {
	parent.children = append(parent.children, nil)
	copy(parent.children[idx+1:], parent.children[idx:])
	parent.children[idx] = it
}

// insertSiblingAfter inserts a new empty node after the given one.
func (t *tree) insertSiblingAfter(after *item) (*item, error) {
	it, err := t.newItem()
	if err != nil {
		return nil, err
	}
	parent := after.parent
	it.parent = parent
	t.insertChildAt(parent, indexOf(after)+1, it)
	return it, nil
}

// insertSiblingBefore inserts a new empty node before the given one, pushing it
// (and its whole subtree) down a slot.
func (t *tree) insertSiblingBefore(before *item) (*item, error) {
	it, err := t.newItem()
	if err != nil {
		return nil, err
	}
	parent := before.parent
	it.parent = parent
	t.insertChildAt(parent, indexOf(before), it)
	return it, nil
}

// insertFirstChild inserts a new empty node as the first child of parent.
func (t *tree) insertFirstChild(parent *item) (*item, error) {
	it, err := t.newItem()
	if err != nil {
		return nil, err
	}
	it.parent = parent
	parent.children = append([]*item{it}, parent.children...)
	return it, nil
}

// duplicate deep-copies the item's subtree with fresh uuids and inserts the
// copy as its next sibling — a duplicate "next to it". Mirrors and links keep
// pointing at their originals. The view root (no parent) cannot be duplicated.
func (t *tree) duplicate(it *item) (*item, error) {
	if it.parent == nil {
		return nil, errors.New("cannot duplicate the root node")
	}
	clone, err := t.cloneSubtree(it)
	if err != nil {
		return nil, err
	}
	clone.parent = it.parent
	t.insertChildAt(it.parent, indexOf(it)+1, clone)
	return clone, nil
}

// cloneSubtree deep-copies an item subtree, handing out fresh uuids and marking
// the copy as new so it persists on the next save. mirrorOf is preserved so
// duplicated mirrors keep resolving to their originals.
func (t *tree) cloneSubtree(src *item) (*item, error) {
	uuid, err := utils.GenerateUUID()
	if err != nil {
		return nil, errors.Wrap(err, "generating uuid")
	}
	c := &item{
		uuid:        uuid,
		name:        src.name,
		note:        src.note,
		typ:         src.typ,
		style:       src.style,
		mirrorOf:    src.mirrorOf,
		completedAt: src.completedAt,
		collapsed:   src.collapsed,
		isNew:       true,
	}
	t.byUUID[uuid] = c
	for _, ch := range src.children {
		dup, err := t.cloneSubtree(ch)
		if err != nil {
			return nil, err
		}
		dup.parent = c
		c.children = append(c.children, dup)
	}
	return c, nil
}

// remove detaches the item (and its subtree) from the tree and records
// pre-existing nodes for tombstoning on save.
func (t *tree) remove(it *item) {
	idx := indexOf(it)
	if idx < 0 {
		return
	}
	parent := it.parent
	parent.children = append(parent.children[:idx], parent.children[idx+1:]...)

	var collect func(x *item)
	collect = func(x *item) {
		if !x.isNew {
			t.deleted = append(t.deleted, x.uuid)
		}
		delete(t.byUUID, x.uuid)
		for _, c := range x.children {
			collect(c)
		}
	}
	collect(it)
}

// indent makes the item a child of its previous sibling.
func (t *tree) indent(it *item) bool {
	idx := indexOf(it)
	if idx <= 0 {
		return false
	}
	// indenting under a mirror attaches to the one real node, the source, so
	// the child belongs to the original and shows through every mirror of it
	prev := t.resolve(it.parent.children[idx-1])
	it.parent.children = append(it.parent.children[:idx], it.parent.children[idx+1:]...)
	it.parent = prev
	prev.children = append(prev.children, it)
	prev.collapsed = false
	if t.db != nil {
		_ = database.SetCollapsed(t.db, prev.uuid, false) // persist the auto-expand
	}
	return true
}

// outdent makes the item the next sibling of its parent.
func (t *tree) outdent(it *item, viewRoot *item) bool {
	parent := it.parent
	if parent == nil || parent == viewRoot {
		return false
	}
	grandparent := parent.parent
	if grandparent == nil {
		return false
	}
	idx := indexOf(it)
	parent.children = append(parent.children[:idx], parent.children[idx+1:]...)

	it.parent = grandparent
	t.insertChildAt(grandparent, indexOf(parent)+1, it)
	return true
}

// move shifts the item among its siblings. At the top/bottom edge of the
// sibling list it instead crosses into the neighbouring subtree: moving down
// drops the item in as the first child of its parent's next sibling, moving up
// appends it as the last child of its parent's previous sibling. viewRoot bounds
// the crossing so a zoomed-in view never spills items out of itself.
func (t *tree) move(it *item, delta int, viewRoot *item) bool {
	idx := indexOf(it)
	if idx < 0 {
		return false
	}
	sibs := it.parent.children
	if target := idx + delta; target >= 0 && target < len(sibs) {
		sibs[idx], sibs[target] = sibs[target], sibs[idx]
		return true
	}
	// at the edge — slip into the adjacent sibling of the parent
	parent := it.parent
	if parent == viewRoot || parent.parent == nil {
		return false
	}
	nIdx := indexOf(parent) + delta
	uncles := parent.parent.children
	if nIdx < 0 || nIdx >= len(uncles) {
		return false
	}
	// resolve, like indent, so a mirror target attaches the child to the source
	dest := t.resolve(uncles[nIdx])
	parent.children = append(parent.children[:idx], parent.children[idx+1:]...)
	it.parent = dest
	if delta > 0 {
		dest.children = append([]*item{it}, dest.children...)
	} else {
		dest.children = append(dest.children, it)
	}
	dest.collapsed = false
	if t.db != nil {
		_ = database.SetCollapsed(t.db, dest.uuid, false) // persist the auto-expand
	}
	return true
}

// reparent moves the item under a new parent, placed first so it lands at the
// top of the target's children rather than the bottom.
func (t *tree) reparent(it *item, newParent *item) bool {
	// cycle check: newParent must not be inside it's subtree
	for p := newParent; p != nil; p = p.parent {
		if p == it {
			return false
		}
	}
	idx := indexOf(it)
	if idx < 0 {
		return false
	}
	it.parent.children = append(it.parent.children[:idx], it.parent.children[idx+1:]...)
	it.parent = newParent
	newParent.children = append([]*item{it}, newParent.children...)
	return true
}

// stats returns total node count below the view root and the edited count.
func (t *tree) stats() (total int, edited int) {
	var walk func(it *item)
	walk = func(it *item) {
		total++
		if it.isNew || t.changed(it) {
			edited++
		}
		for _, c := range it.children {
			walk(c)
		}
	}
	for _, c := range t.root.children {
		walk(c)
	}
	total++ // include root
	if t.changed(t.root) {
		edited++
	}
	return total, edited
}

// changed reports whether the item's content differs from its snapshot
// (structure changes are detected separately during save).
func (t *tree) changed(it *item) bool {
	if it.isNew {
		return true
	}
	s, ok := t.snapshots[it.uuid]
	if !ok {
		return true
	}
	return s.name != it.name || s.note != it.note || s.typ != it.typ ||
		s.style != it.style || s.mirrorOf != it.mirrorOf ||
		s.completedAt != it.completedAt || s.readonly != it.readonly
}

// save writes the in-memory tree back to the database in one transaction.
// It returns the number of nodes that were inserted or updated.
func (t *tree) save() (int, error) {
	if t.db == nil {
		return 0, nil // the ephemeral Temporary Domain tree is never persisted
	}
	tx, err := t.db.Begin()
	if err != nil {
		return 0, errors.Wrap(err, "beginning a transaction")
	}

	now := time.Now().UnixNano()
	written := 0

	var walk func(it *item, parentUUID string, rank int) error
	walk = func(it *item, parentUUID string, rank int) error {
		s, existed := t.snapshots[it.uuid]
		structChanged := !existed || s.parentUUID != parentUUID || s.rank != rank

		// a brand-new node Upserts: if its uuid somehow already has a (tombstoned)
		// row — a delete that was saved then undone leaves the snapshot gone but the
		// row alive — it is revived instead of crashing on UNIQUE(uuid).
		if it.isNew && !existed {
			n := database.Node{
				UUID:        it.uuid,
				ParentUUID:  parentUUID,
				Rank:        rank,
				Name:        it.name,
				Note:        it.note,
				Type:        it.typ,
				Style:       it.style,
				MirrorOf:    it.mirrorOf,
				CompletedAt: it.completedAt,
				AddedOn:     now,
				EditedOn:    now,
				Collapsed:   it.collapsed,
				Readonly:    it.readonly,
				Starred:     it.starred,
			}
			if err := n.Upsert(tx); err != nil {
				return err
			}
			written++
		} else if t.changed(it) || structChanged {
			if _, err := tx.Exec(`UPDATE nodes SET parent_uuid = ?, rank = ?, name = ?, note = ?, type = ?,
				style = ?, mirror_of = ?, readonly = ?, completed_at = ?, edited_on = ? WHERE uuid = ?`,
				parentUUID, rank, it.name, it.note, it.typ, it.style, it.mirrorOf, it.readonly, it.completedAt, now, it.uuid); err != nil {
				return errors.Wrapf(err, "updating node %s", it.uuid)
			}
			written++
		}

		// collapse and star are local view-state: persist them on save as a
		// backstop to the immediate SetCollapsed/SetStarred writes (new nodes
		// carry both via Insert).
		if existed && s.collapsed != it.collapsed {
			if _, err := tx.Exec("UPDATE nodes SET collapsed = ? WHERE uuid = ?", it.collapsed, it.uuid); err != nil {
				return errors.Wrapf(err, "persisting collapsed for %s", it.uuid)
			}
		}
		if existed && s.starred != it.starred {
			if _, err := tx.Exec("UPDATE nodes SET starred = ? WHERE uuid = ?", it.starred, it.uuid); err != nil {
				return errors.Wrapf(err, "persisting starred for %s", it.uuid)
			}
		}

		for i, c := range it.children {
			if err := walk(c, it.uuid, i); err != nil {
				return err
			}
		}
		return nil
	}

	// the root's own parent/rank are not managed by this editor
	if t.changed(t.root) {
		if _, err := tx.Exec(`UPDATE nodes SET name = ?, note = ?, type = ?, style = ?, completed_at = ?, edited_on = ? WHERE uuid = ?`,
			t.root.name, t.root.note, t.root.typ, t.root.style, t.root.completedAt, now, t.root.uuid); err != nil {
			tx.Rollback()
			return 0, errors.Wrap(err, "updating root node")
		}
		written++
	}
	for i, c := range t.root.children {
		if err := walk(c, t.root.uuid, i); err != nil {
			tx.Rollback()
			return 0, err
		}
	}

	for _, uuid := range t.deleted {
		if _, err := tx.Exec("UPDATE nodes SET deleted = 1 WHERE uuid = ?", uuid); err != nil {
			tx.Rollback()
			return 0, errors.Wrapf(err, "tombstoning node %s", uuid)
		}
		written++
	}

	if err := tx.Commit(); err != nil {
		return 0, errors.Wrap(err, "committing transaction")
	}

	// refresh snapshots so a second save is a no-op
	t.refreshSnapshots()
	t.deleted = nil

	return written, nil
}

func (t *tree) refreshSnapshots() {
	t.snapshots = map[string]snapshot{}
	var walk func(it *item, parentUUID string, rank int)
	walk = func(it *item, parentUUID string, rank int) {
		it.isNew = false
		t.snapshots[it.uuid] = snapshot{
			parentUUID:  parentUUID,
			rank:        rank,
			name:        it.name,
			note:        it.note,
			typ:         it.typ,
			style:       it.style,
			mirrorOf:    it.mirrorOf,
			completedAt: it.completedAt,
			collapsed:   it.collapsed,
			readonly:    it.readonly,
		}
		for i, c := range it.children {
			walk(c, it.uuid, i)
		}
	}
	walk(t.root, "", 0)
}
