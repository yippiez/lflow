package editor

import (
	"time"

	"github.com/lflow/lflow/pkg/tui/database"
	"github.com/lflow/lflow/pkg/utils"
	"github.com/pkg/errors"
)

// item is an in-memory outline node.
type item struct {
	uuid            string
	name            string
	note            string
	typ             string
	style           string // comma-separated style tokens, e.g. "bold,color:blue"
	mirrorOf        string
	completedAt     int64
	children        []*item
	parent          *item
	collapsed       bool
	readonly        bool   // LOCK_READ_WRITE: inline/content edits are no-ops
	structureLocked bool   // LOCK_INDENT_UNINDENT: hierarchy/order cannot change
	starred         bool   // /star: pinned to the top of pickers and search hits
	priority        string // /priority: incoming nodes land on top ("up") or at the bottom ("down"/"")
	addedOn         int64  // creation time (UnixNano); shown by the log node's time chip
	isNew           bool
}

// lockMode joins the independent in-memory lock flags for persistence in the
// legacy readonly integer column.
func (it *item) lockMode() database.LockMode {
	var l database.LockMode
	if it.readonly {
		l |= database.LockReadWrite
	}
	if it.structureLocked {
		l |= database.LockIndentOutdent
	}
	return l
}

// queryGenerated reports a materialized query-view row. Query results and their
// breadcrumb scaffolding are mirrors with a structural lock; ordinary mirrors
// keep their normal through-edit behavior.
func (it *item) queryGenerated() bool {
	if it == nil || it.mirrorOf == "" || !it.structureLocked {
		return false
	}
	for p := it.parent; p != nil; p = p.parent {
		if p.typ == database.TypeQuery {
			return true
		}
	}
	return false
}

// snapshot captures a node's persisted state for change detection on save.
type snapshot struct {
	parentUUID      string
	rank            int
	name            string
	note            string
	typ             string
	style           string
	mirrorOf        string
	completedAt     int64
	collapsed       bool
	readonly        bool
	structureLocked bool
	starred         bool
	priority        string
}

// tree is the in-memory model of the subtree being edited.
type tree struct {
	db        *database.DB
	root      *item
	snapshots map[string]snapshot
	deleted   []string // uuids of pre-existing nodes deleted in this session
	// resolved names for mirrors whose originals could not be grafted —
	// missing, or overlapping nodes already loaded (see graftExternal)
	externalNames map[string]string
	// external holds grafted subtree roots: the live sources of mirrors that
	// point outside the loaded subtree, loaded so those mirrors show through.
	// They are detached (parent == nil; the DB owns their position) — save,
	// undo and rebuildByUUID walk them alongside root.
	external []*item
	byUUID   map[string]*item
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
		uuid:            src.uuid,
		name:            src.name,
		note:            src.note,
		typ:             src.typ,
		style:           src.style,
		mirrorOf:        src.mirrorOf,
		completedAt:     src.completedAt,
		collapsed:       src.collapsed,
		readonly:        src.readonly,
		structureLocked: src.structureLocked,
		starred:         src.starred,
		priority:        src.priority,
		addedOn:         src.addedOn,
		isNew:           src.isNew,
		parent:          parent,
	}
	for _, ch := range src.children {
		c.children = append(c.children, cloneItem(ch, c))
	}
	return c
}

// rebuildByUUID re-indexes byUUID from the current item tree — root and the
// grafted external subtrees — used after an undo swaps in a restored tree.
// externalNames and snapshots are left untouched.
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
	for _, ex := range t.external {
		walk(ex)
	}
}

// itemFromNode builds an in-memory item from a DB row; the caller links parent.
func itemFromNode(n database.Node) *item {
	return &item{
		uuid:            n.UUID,
		name:            n.Name,
		note:            n.Note,
		typ:             n.Type,
		style:           n.Style,
		mirrorOf:        n.MirrorOf,
		completedAt:     n.CompletedAt,
		collapsed:       n.Collapsed,
		readonly:        n.LockValue().Has(database.LockReadWrite),
		structureLocked: n.LockValue().Has(database.LockIndentOutdent),
		starred:         n.Starred,
		priority:        n.Priority,
		addedOn:         n.AddedOn,
	}
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
		items[n.UUID] = itemFromNode(n)
		t.snapshots[n.UUID] = snapFromNode(n)
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

	// mirrors whose originals live outside this subtree: graft each source's
	// live subtree so the mirror shows through; failures leave a name stub
	t.graftExternalSources()

	return t, nil
}

// graftExternalSources grafts the live subtree of every mirror source that is
// not loaded, repeating until closure since a grafted subtree can itself
// contain mirrors pointing further outside. Sources that fail to graft are
// remembered so a broken mirror cannot loop the walk. Reports whether any
// source was grafted — the live-sync fold uses it to refresh the rows.
func (t *tree) graftExternalSources() bool {
	failed := map[string]bool{}
	grafted := false
	for {
		var pending []string
		for _, it := range t.byUUID {
			if it.mirrorOf == "" || failed[it.mirrorOf] {
				continue
			}
			if _, in := t.byUUID[it.mirrorOf]; !in {
				pending = append(pending, it.mirrorOf)
			}
		}
		if len(pending) == 0 {
			return grafted
		}
		for _, uuid := range pending {
			if t.graftExternal(uuid) {
				grafted = true
			} else {
				failed[uuid] = true
			}
		}
	}
}

// graftExternal loads the live subtree of a mirror source that lives outside
// the loaded tree and grafts it in as a detached external root, so the mirror
// shows its children through and edits there act on the real nodes. A source
// that cannot be fetched or is tombstoned leaves a "(missing)" stub; one whose
// subtree overlaps nodes already loaded (it is an ancestor of the tree, or of
// an earlier graft) leaves a name stub instead — byUUID must stay one item per
// uuid. Reports whether the source ended up loaded.
func (t *tree) graftExternal(srcUUID string) bool {
	if _, in := t.byUUID[srcUUID]; in {
		return true
	}
	if t.db == nil {
		return false
	}
	nodes, err := database.GetSubtree(t.db, srcUUID)
	if err != nil || nodes[0].Deleted {
		t.externalNames[srcUUID] = "(missing)"
		return false
	}
	for _, n := range nodes {
		if _, in := t.byUUID[n.UUID]; in {
			t.externalNames[srcUUID] = nodes[0].Name
			return false
		}
	}

	items := map[string]*item{}
	for _, n := range nodes {
		items[n.UUID] = itemFromNode(n)
		t.snapshots[n.UUID] = snapFromNode(n)
	}
	for _, n := range nodes {
		if n.UUID == srcUUID {
			continue
		}
		it := items[n.UUID]
		parent := items[n.ParentUUID]
		if parent == nil {
			parent = items[srcUUID]
		}
		it.parent = parent
		parent.children = append(parent.children, it)
	}
	for uuid, it := range items {
		t.byUUID[uuid] = it
	}
	t.external = append(t.external, items[srcUUID])
	return true
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
// act on the one real node — same node everywhere. Out-of-subtree sources are
// grafted at load, so byUUID normally hits; a non-mirror, or a mirror whose
// source failed to graft, returns itself.
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
	mirrored bool   // shown through a mirror: same node, rendered read-only
	ctx      *item  // the mirror this row is shown under, nil at the real location
	// cycleDepth > 0: this mirror row re-enters a node already open on the
	// path — the nth repetition of the same mirror. cycled marks a repetition
	// held back by the unroll budget: children exist but stay folded until the
	// next expand press raises the budget (see visibleRows).
	cycleDepth int
	cycled     bool
}

// childItems returns the children to display under it. An expanded mirror shows
// its source's live children so the same node appears everywhere — edits in
// either spot act on the one real node; a normal node shows its own. A mirror
// whose source could not be grafted (see graftExternal) shows nothing through.
func (t *tree) childItems(it *item) []*item {
	if it.mirrorOf == "" || it.queryGenerated() {
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

// cloneSpent copies a path-local unroll-consumption map, like cloneSeen, so
// sibling branches spend their cycle budgets independently.
func cloneSpent(m map[string]int) map[string]int {
	n := make(map[string]int, len(m)+1)
	for k, v := range m {
		n[k] = v
	}
	return n
}

// expandTarget is the node whose children a row expands: a mirror expands its
// source, a normal node expands itself. nil means nothing to expand. The walk
// re-expands a target already on the path only within the mirror's unroll
// budget, so a mirror pointing at an ancestor nests one paid level at a time
// instead of looping forever.
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
// the /hide:complete toggle.
//
// unroll (uuid → levels) is the per-mirror cycle budget: a mirror whose target
// is already open on the path may re-enter it that many times, so a mirror of
// an ancestor nests one level per expand press. Every cycle in the walk passes
// through such a mirror edge (parent links alone form a forest), so plain
// nodes repeated inside a paid level recurse freely and the walk still
// terminates. A repetition past the budget lands as a cycled row — a foldable
// leaf whose expand press raises the budget.
func (t *tree) visibleRows(viewRoot *item, hideCompleted bool, unroll map[string]int) []row {
	var rows []row
	var walk func(it *item, depth int, branch []bool, mirrored bool, ctx *item, seen map[*item]bool, spent map[string]int)
	walk = func(it *item, depth int, branch []bool, mirrored bool, ctx *item, seen map[*item]bool, spent map[string]int) {
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
			r := row{it: c, depth: depth, last: last, branch: append([]bool(nil), branch...), mirrored: cm, ctx: ctx}
			tgt := t.expandTarget(c)
			childSpent := spent
			if tgt != nil && c.mirrorOf != "" && seen[tgt] {
				r.cycleDepth = spent[c.uuid] + 1
				if unroll[c.uuid] >= r.cycleDepth {
					childSpent = cloneSpent(spent)
					childSpent[c.uuid] = r.cycleDepth
				} else {
					r.cycled = !c.collapsed && len(t.childItems(c)) > 0
					rows = append(rows, r)
					continue
				}
			}
			rows = append(rows, r)
			if c.collapsed || tgt == nil {
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
			walk(c, depth+1, append(branch, !last), cm, childCtx, next, childSpent)
		}
	}
	walk(viewRoot, 0, nil, false, nil, map[*item]bool{viewRoot: true}, map[string]int{})
	return rows
}

// allRows flattens the whole loaded tree ignoring collapsed state: the
// scrollback dump on quit is the complete outline, not the current folding.
// Cycles dump one level regardless of any interactive unroll — the dump must
// stay finite.
func (t *tree) allRows() []row {
	var rows []row
	var walk func(it *item, depth int, branch []bool, mirrored bool, ctx *item, seen map[*item]bool)
	walk = func(it *item, depth int, branch []bool, mirrored bool, ctx *item, seen map[*item]bool) {
		kids := t.childItems(it)
		for i, c := range kids {
			last := i == len(kids)-1
			cm := mirrored || c.mirrorOf != ""
			tgt := t.expandTarget(c)
			cycled := tgt != nil && seen[tgt] && len(t.childItems(c)) > 0
			rows = append(rows, row{it: c, depth: depth, last: last, branch: append([]bool(nil), branch...), mirrored: cm, ctx: ctx, cycled: cycled})
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
	// new nodes default to priority up (incoming children land on top); nodes
	// that predate the priority column were backfilled to down (lm39)
	it := &item{uuid: uuid, typ: typ, priority: database.PriorityUp, addedOn: time.Now().UnixNano(), isNew: true}
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
	if parent != nil && parent.structureLocked {
		return nil, errors.New("node structure is locked")
	}
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
	if it.structureLocked {
		return nil, errors.New("node structure is locked")
	}
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
		priority:    src.priority,
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
	if it == nil || it.structureLocked {
		return false
	}
	idx := indexOf(it)
	if idx <= 0 {
		return false
	}
	// indenting under a mirror attaches to the one real node, the source, so
	// the child belongs to the original and shows through every mirror of it
	prev := t.resolve(it.parent.children[idx-1])
	if prev == nil || prev.structureLocked {
		return false
	}
	it.parent.children = append(it.parent.children[:idx], it.parent.children[idx+1:]...)
	it.parent = prev
	// the landing slot honors the target's priority: up keeps the node adjacent
	// to the new parent line, down appends below its existing children
	if prev.priority == database.PriorityUp {
		prev.children = append([]*item{it}, prev.children...)
	} else {
		prev.children = append(prev.children, it)
	}
	prev.collapsed = false
	if t.db != nil {
		_ = database.SetCollapsed(t.db, prev.uuid, false) // persist the auto-expand
	}
	return true
}

// outdent makes the item the next sibling of its parent.
// viewRoot bounds the move so a zoomed view never spills items out of itself.
// When parent == viewRoot and escape is non-nil (the mirror the cursor is
// working through), the item leaves the source and lands as the next sibling
// of escape — so shift+tab on a through-child exits the mirrored subtree, and
// both the original and every mirror of it stop showing the child.
func (t *tree) outdent(it *item, viewRoot *item, escape *item) bool {
	if it == nil || it.structureLocked {
		return false
	}
	parent := it.parent
	if parent == nil || parent.structureLocked {
		return false
	}
	if parent == viewRoot {
		if escape == nil || escape.parent == nil || escape.parent.structureLocked {
			return false
		}
		// refuse a cycle: escape must not sit inside the node being moved
		for p := escape; p != nil; p = p.parent {
			if p == it {
				return false
			}
		}
		idx := indexOf(it)
		if idx < 0 {
			return false
		}
		parent.children = append(parent.children[:idx], parent.children[idx+1:]...)
		it.parent = escape.parent
		t.insertChildAt(escape.parent, indexOf(escape)+1, it)
		return true
	}
	grandparent := parent.parent
	if grandparent == nil || grandparent.structureLocked {
		return false
	}
	idx := indexOf(it)
	if idx < 0 {
		return false
	}
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
	if it == nil || it.structureLocked {
		return false
	}
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
	if dest == nil || dest.structureLocked {
		return false
	}
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

// reparent moves the item under a new parent, landing where the target's
// priority points: up → top of its children, down → bottom.
func (t *tree) reparent(it *item, newParent *item) bool {
	if it == nil || newParent == nil || it.structureLocked || newParent.structureLocked {
		return false
	}
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
	if newParent.priority == database.PriorityUp {
		newParent.children = append([]*item{it}, newParent.children...)
	} else {
		newParent.children = append(newParent.children, it)
	}
	return true
}

// reparentAll moves the items under dest preserving the block's own order:
// dest's priority lands each node on top (up) or at the bottom (down), so the
// walk runs reversed under an up target and forward under a down one.
func (t *tree) reparentAll(items []*item, dest *item) bool {
	moved := false
	if dest.priority == database.PriorityUp {
		for i := len(items) - 1; i >= 0; i-- {
			if t.reparent(items[i], dest) {
				moved = true
			}
		}
		return moved
	}
	for _, it := range items {
		if t.reparent(it, dest) {
			moved = true
		}
	}
	return moved
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
		s.completedAt != it.completedAt || s.readonly != it.readonly ||
		s.structureLocked != it.structureLocked
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
				Lock:        it.lockMode(),
				Starred:     it.starred,
				Priority:    it.priority,
			}
			if err := n.Upsert(tx); err != nil {
				return err
			}
			written++
		} else if t.changed(it) || structChanged {
			if _, err := tx.Exec(`UPDATE nodes SET parent_uuid = ?, rank = ?, name = ?, note = ?, type = ?,
				style = ?, mirror_of = ?, readonly = ?, completed_at = ?, edited_on = ? WHERE uuid = ?`,
				parentUUID, rank, it.name, it.note, it.typ, it.style, it.mirrorOf, it.lockMode(), it.completedAt, now, it.uuid); err != nil {
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
		if existed && s.priority != it.priority {
			if _, err := tx.Exec("UPDATE nodes SET priority = ? WHERE uuid = ?", it.priority, it.uuid); err != nil {
				return errors.Wrapf(err, "persisting priority for %s", it.uuid)
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

	// grafted external subtrees persist edits made through mirrors; the roots
	// keep their DB position (snapshot parent/rank), only their content and
	// subtrees are editable here
	for _, ex := range t.external {
		s, ok := t.snapshots[ex.uuid]
		if !ok {
			continue
		}
		if err := walk(ex, s.parentUUID, s.rank); err != nil {
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
	old := t.snapshots
	t.snapshots = map[string]snapshot{}
	var walk func(it *item, parentUUID string, rank int)
	walk = func(it *item, parentUUID string, rank int) {
		it.isNew = false
		t.snapshots[it.uuid] = snapshot{
			parentUUID:      parentUUID,
			rank:            rank,
			name:            it.name,
			note:            it.note,
			typ:             it.typ,
			style:           it.style,
			mirrorOf:        it.mirrorOf,
			completedAt:     it.completedAt,
			collapsed:       it.collapsed,
			readonly:        it.readonly,
			structureLocked: it.structureLocked,
			starred:         it.starred,
			priority:        it.priority,
		}
		for i, c := range it.children {
			walk(c, it.uuid, i)
		}
	}
	walk(t.root, "", 0)
	// external roots carry their DB position forward from the previous snapshot
	for _, ex := range t.external {
		if s, ok := old[ex.uuid]; ok {
			walk(ex, s.parentUUID, s.rank)
		}
	}
}
