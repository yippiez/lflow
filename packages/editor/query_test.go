package editor

import (
	"strings"
	"testing"

	"github.com/lflow/lflow/packages/database"
)

// newQueryTree builds a minimal in-memory tree with a query node, enough for
// reconcileQueryMirrors (which never touches the DB).
func newQueryTree() (*Model, *item) {
	root := &item{uuid: "root"}
	q := &item{uuid: "q", typ: database.TypeQuery, name: "buy", parent: root}
	root.children = []*item{q}
	tr := &tree{
		root:          root,
		snapshots:     map[string]snapshot{},
		externalNames: map[string]string{},
		byUUID:        map[string]*item{"root": root, "q": q},
	}
	return &Model{tree: tr}, q
}

func mirrorSources(q *item) []string {
	var out []string
	for _, c := range q.children {
		if c.mirrorOf != "" {
			out = append(out, c.mirrorOf)
		}
	}
	return out
}

// TestQueryMatchesStarredRanksFirst: /star pins a hit above name-sorted peers.
func TestQueryMatchesStarredRanksFirst(t *testing.T) {
	root := &item{uuid: "root"}
	q := &item{uuid: "q", typ: database.TypeQuery, name: "buy", parent: root}
	a := &item{uuid: "a", name: "buy apples", parent: root}
	b := &item{uuid: "b", name: "buy bread", parent: root, starred: true}
	c := &item{uuid: "c", name: "buy carrots", parent: root}
	root.children = []*item{q, a, b, c}
	tr := &tree{
		root:          root,
		snapshots:     map[string]snapshot{},
		externalNames: map[string]string{},
		byUUID:        map[string]*item{"root": root, "q": q, "a": a, "b": b, "c": c},
	}
	m := &Model{tree: tr}
	got := m.queryMatches(q)
	if len(got) != 3 {
		t.Fatalf("want 3 hits, got %d", len(got))
	}
	if got[0].UUID != "b" || !got[0].Starred {
		t.Fatalf("starred hit must rank first, got %s starred=%v", got[0].UUID, got[0].Starred)
	}
	// unstarred tail keeps name order: apples, carrots
	if got[1].UUID != "a" || got[2].UUID != "c" {
		t.Fatalf("unstarred tail must stay name-ordered: %s, %s", got[1].UUID, got[2].UUID)
	}
}

func TestQueryMatchesCommittedTagChips(t *testing.T) {
	root := &item{uuid: database.RootUUID, name: "Root"}
	q := &item{uuid: "q", typ: database.TypeQuery, name: database.ChipAnchor("query-tag"), parent: root}
	hit := &item{uuid: "hit", name: "ship " + database.ChipAnchor("hit-tag"), parent: root}
	miss := &item{uuid: "miss", name: "ship #urgently", parent: root}
	root.children = []*item{q, hit, miss}
	m := &Model{
		tree: &tree{root: root, snapshots: map[string]snapshot{}, externalNames: map[string]string{},
			byUUID: map[string]*item{database.RootUUID: root, "q": q, "hit": hit, "miss": miss}},
		chips: map[string]database.Chip{
			"query-tag": {ID: "query-tag", Kind: chipKindTag, Value: "urgent"},
			"hit-tag":   {ID: "hit-tag", Kind: chipKindTag, Value: "urgent"},
		},
	}

	got := m.queryMatches(q)
	if len(got) != 1 || got[0].UUID != "hit" {
		t.Fatalf("committed #tag query matches = %+v, want the exact tag-chip node", got)
	}
}

func TestQueryScopeDefaultsToRootAndUsesSelectedNode(t *testing.T) {
	root := &item{uuid: database.RootUUID, name: "Root"}
	scope := &item{uuid: "scope", name: "scope", parent: root}
	q := &item{uuid: "q", typ: database.TypeQuery, name: "needle", parent: scope}
	local := &item{uuid: "local", name: "needle local", parent: scope}
	branch := &item{uuid: "branch", name: "branch", parent: scope}
	deep := &item{uuid: "deep", name: "needle deep", parent: branch}
	outside := &item{uuid: "outside", name: "needle outside", parent: root}
	branch.children = []*item{deep}
	scope.children = []*item{q, local, branch}
	root.children = []*item{scope, outside}
	tr := &tree{root: root, snapshots: map[string]snapshot{}, externalNames: map[string]string{},
		byUUID: map[string]*item{"root": root, "scope": scope, "q": q, "local": local,
			"branch": branch, "deep": deep, "outside": outside}}
	m := &Model{tree: tr, chips: map[string]database.Chip{
		"scope-link": {ID: "scope-link", Kind: chipKindLink, Value: nodeLinkURI(scope.uuid), Label: scope.name},
	}}

	if got := len(m.queryMatches(q)); got != 3 {
		t.Fatalf("default-root matches = %d, want 3", got)
	}
	q.name = "needle :in:" + database.ChipAnchor("scope-link")
	got := m.queryMatches(q)
	if len(got) != 2 || got[0].UUID != "deep" || got[1].UUID != "local" {
		t.Fatalf(":in: matches = %+v, want selected-subtree hits only", got)
	}
	if got := stripSGR(queryPrefix(q)); strings.Contains(got, "G") || strings.Contains(got, "L") {
		t.Fatalf("query prefix must not expose obsolete global/local scope: %q", got)
	}
}

// TestQueryScopedNestedTags proves :in: constrains every stage of `>` rather
// than filtering only its final results. This was the #tag :in:node > #other
// freeze regression: the scope root is itself a possible left-hand match and
// the descendant must still be found.
func TestQueryScopedNestedTags(t *testing.T) {
	root := &item{uuid: database.RootUUID}
	scope := &item{uuid: "scope", name: "#parent", parent: root}
	hit := &item{uuid: "hit", name: "#other", parent: scope}
	outsideParent := &item{uuid: "outside-parent", name: "#parent", parent: root}
	outsideHit := &item{uuid: "outside-hit", name: "#other", parent: outsideParent}
	q := &item{uuid: "q", typ: database.TypeQuery, name: "#parent :in:scope > #other", parent: scope}
	root.children = []*item{scope, outsideParent}
	scope.children = []*item{hit, q}
	outsideParent.children = []*item{outsideHit}
	m := &Model{tree: &tree{root: root, snapshots: map[string]snapshot{}, externalNames: map[string]string{},
		byUUID: map[string]*item{database.RootUUID: root, "scope": scope, "hit": hit, "q": q,
			"outside-parent": outsideParent, "outside-hit": outsideHit}}}

	got := m.queryMatches(q)
	if len(got) != 1 || got[0].UUID != "hit" {
		t.Fatalf("scoped nested tag hits = %+v, want only hit", got)
	}
}

func TestQueryScopePickerStoresNodeLink(t *testing.T) {
	m, _ := dbModel(t,
		database.Node{UUID: "query", Name: ":in:", Type: database.TypeQuery},
		database.Node{UUID: "scope", Name: "selected subtree"},
	)
	q := m.tree.byUUID["query"]
	m.cursor = m.rowIndexOf(q)
	m.caret = len([]rune(q.name))
	m.finder.act = actQueryScope
	m.runFinder(database.Node{UUID: "scope", Name: "selected subtree"})

	raw, scope := m.queryTextAndScope(q)
	if raw != "" || scope != "scope" {
		t.Fatalf("picked scope parsed as raw=%q scope=%q, want empty expression in scope", raw, scope)
	}
	if !hasAnchor(q.name) {
		t.Fatalf("picked scope must be stored as a link chip, got %q", q.name)
	}
}

func TestQueryHitHighlightAndStructuralLock(t *testing.T) {
	m, q := newQueryTree()
	q.name = "milk"
	source := &item{uuid: "a", name: "buy milk", parent: m.tree.root}
	m.tree.root.children = append(m.tree.root.children, source)
	m.tree.byUUID[source.uuid] = source
	m.viewStack = []*item{m.tree.root}
	m.width, m.height = 80, 24
	runQuery(m, q)

	hit := q.children[0]
	rendered := renderBody(hit, source.name, -1, false, nil)
	rendered = m.highlightQueryHit(hit, source.name, rendered)
	if !strings.Contains(rendered, bgHit) {
		t.Fatal("matching text must carry the yellow query-hit background")
	}
	// Editing the query does not update its materialized results until alt+r;
	// their explanation highlight must remain tied to that same last run.
	q.name = "buy"
	afterEdit := renderBody(hit, source.name, -1, false, nil)
	afterEdit = m.highlightQueryHit(hit, source.name, afterEdit)
	if afterEdit != rendered {
		t.Fatal("query hit highlight changed before the query was rerun")
	}
	if !hit.structureLocked || hit.readonly {
		t.Fatalf("hit locks = structure:%v content:%v", hit.structureLocked, hit.readonly)
	}
	if m.tree.indent(hit) {
		t.Fatal("query results must not indent")
	}
}

func TestQueryStreamsPersistedCandidates(t *testing.T) {
	m, _ := dbModel(t,
		database.Node{UUID: "query", Name: "needle", Type: database.TypeQuery},
		database.Node{UUID: "hit", Name: "needle in the database"},
	)
	q := m.tree.byUUID["query"]
	cmd := runQuery(m, q)
	if m.queryLoad == nil || cmd == nil {
		t.Fatal("persisted query must start a streamed load")
	}
	if bar := stripSGR(strings.Join(m.bottomBar(120), "\n")); !strings.Contains(bar, "1 query running") {
		t.Fatalf("running-query count missing from bar: %q", bar)
	}
	// The scan streams batches, then hands the finished expression to a worker
	// (queryReadyMsg) so a long evaluation never blocks the UI goroutine.
	sawReady := false
	for steps := 0; m.queryLoad != nil && steps < 100; steps++ {
		switch msg := cmd().(type) {
		case queryLoadMsg:
			cmd = m.handleQueryLoad(msg)
		case queryReadyMsg:
			sawReady = true
			cmd = m.handleQueryReady(msg)
		default:
			t.Fatalf("stream command returned %T, want a query message", msg)
		}
		if cmd == nil {
			break
		}
	}
	if !sawReady {
		t.Fatal("query never completed through the worker (queryReadyMsg)")
	}
	if m.queryLoad != nil {
		t.Fatal("query stream did not finish")
	}
	if got := mirrorSources(q); len(got) != 1 || got[0] != "hit" {
		t.Fatalf("streamed results = %v, want hit", got)
	}
}

func finishPersistedQuery(t *testing.T, m *Model, q *item) {
	t.Helper()
	cmd := runQuery(m, q)
	for steps := 0; m.queryLoad != nil && steps < 100; steps++ {
		if cmd == nil {
			t.Fatal("query stopped before its stream finished")
		}
		switch msg := cmd().(type) {
		case queryLoadMsg:
			cmd = m.handleQueryLoad(msg)
		case queryReadyMsg:
			cmd = m.handleQueryReady(msg)
		default:
			t.Fatalf("stream command returned %T", msg)
		}
	}
	if m.queryLoad != nil {
		t.Fatal("query stream did not finish")
	}
}

// TestPersistedTagAndStateQueries is the reported project-column regression:
// adjacent tags intersect, and is(open) must read completion state from rows
// streamed out of SQLite rather than treating every persisted node as open.
func TestPersistedTagAndStateQueries(t *testing.T) {
	m, _ := dbModel(t,
		database.Node{UUID: "tags-query", Name: "#node #chip", Type: database.TypeQuery},
		database.Node{UUID: "tag-hit", Name: "agent traces #node #chip"},
		database.Node{UUID: "tag-miss", Name: "another #node"},
		database.Node{UUID: "state-query", Name: "#qol is(open)", Type: database.TypeQuery},
		database.Node{UUID: "open", Name: "open task #qol"},
		database.Node{UUID: "done", Name: "done task #qol", CompletedAt: 99},
	)

	tags := m.tree.byUUID["tags-query"]
	finishPersistedQuery(t, m, tags)
	if got := mirrorSources(tags); len(got) != 1 || got[0] != "tag-hit" {
		t.Fatalf("#node #chip = %v, want tag-hit", got)
	}

	state := m.tree.byUUID["state-query"]
	finishPersistedQuery(t, m, state)
	if got := mirrorSources(state); len(got) != 1 || got[0] != "open" {
		t.Fatalf("#qol is(open) = %v, want only open", got)
	}
}

func TestQueryMirrorsUseSourceStyle(t *testing.T) {
	m, q := newQueryTree()
	source := &item{uuid: "source", name: "colored", style: "color:red", parent: m.tree.root}
	m.tree.root.children = append(m.tree.root.children, source)
	m.tree.byUUID[source.uuid] = source
	m.reconcileQueryMirrors(q, []database.Node{{UUID: source.uuid, Name: source.name}})

	hit := q.children[0]
	body := renderBody(m.renderItem(hit), m.tree.displayName(hit), -1, false, nil)
	if !strings.Contains(body, cRed) {
		t.Fatalf("query mirror lost source color: %q", body)
	}
}

func TestQueryHitHighlightsVisibleTagChip(t *testing.T) {
	m, q := newQueryTree()
	m.chips = map[string]database.Chip{
		"query-tag": {ID: "query-tag", Kind: chipKindTag, Value: "urgent"},
		"hit-tag":   {ID: "hit-tag", Kind: chipKindTag, Value: "urgent"},
	}
	q.name = database.ChipAnchor("query-tag")
	name := "ship " + database.ChipAnchor("hit-tag")
	m.reconcileQueryMirrors(q, []database.Node{{UUID: "a", Name: name}})
	hit := q.children[0]
	body := renderBody(hit, name, -1, false, m.chips)
	body = m.highlightQueryHit(hit, name, body)
	if !strings.Contains(body, bgHit) {
		t.Fatal("a visible tag chip in every matching row must be highlighted")
	}
}

func TestQueryReconcileIdempotentAndStale(t *testing.T) {
	m, q := newQueryTree()
	matches := []database.Node{
		{UUID: "a", Name: "buy milk"},
		{UUID: "b", Name: "buy eggs"},
		{UUID: "c", Name: "buy bread"},
	}

	m.reconcileQueryMirrors(q, matches)
	if got := queryHitCount(q); got != 3 {
		t.Fatalf("first run: want 3 mirrors, got %d", got)
	}

	// re-running with the same matches must not duplicate mirrors
	first := append([]string(nil), mirrorSources(q)...)
	m.reconcileQueryMirrors(q, matches)
	if got := queryHitCount(q); got != 3 {
		t.Fatalf("re-run: want 3 mirrors, got %d", got)
	}
	// the mirror children should be the SAME items (kept in place), not recreated
	for i, c := range q.children {
		if c.mirrorOf != "" && c.mirrorOf != first[i] {
			t.Fatalf("re-run reordered/recreated mirror %d: %q != %q", i, c.mirrorOf, first[i])
		}
	}

	// dropping a match tombstones its mirror; preserved children stay
	q.children = append(q.children, &item{uuid: "user", name: "kept", parent: q}) // a user child
	m.reconcileQueryMirrors(q, matches[:2])                                       // only a, b match now
	if got := queryHitCount(q); got != 2 {
		t.Fatalf("after stale drop: want 2 mirrors, got %d", got)
	}
	// the dropped mirror (source c) must be gone, the user child preserved
	for _, c := range q.children {
		if c.mirrorOf == "c" {
			t.Fatal("stale mirror for source c was not removed")
		}
	}
	found := false
	for _, c := range q.children {
		if c.uuid == "user" {
			found = true
		}
	}
	if !found {
		t.Fatal("user (non-mirror) child was not preserved")
	}
}

// TestRunningQueryShimmerInBar: the house loading indicator is the ultraloop
// sliding shine, not a spinner glyph, and the toolbar carries a count.
func TestRunningQueryShimmerInBar(t *testing.T) {
	m, _ := dbModel(t,
		database.Node{UUID: "query", Name: "needle", Type: database.TypeQuery},
		database.Node{UUID: "hit", Name: "needle in the database"},
	)
	q := m.tree.byUUID["query"]
	if n := m.runningQueryCount(); n != 0 {
		t.Fatalf("idle editor reports %d running queries", n)
	}
	runQuery(m, q)
	if n := m.runningQueryCount(); n != 1 {
		t.Fatalf("running queries = %d, want 1", n)
	}
	bar := strings.Join(m.bottomBar(120), "\n")
	if !strings.Contains(stripSGR(bar), "1 query running") {
		t.Fatalf("bar missing the count: %q", stripSGR(bar))
	}
	// the shine paints per-rune color, so the phrase carries SGR runs rather than
	// one flat span — a spinner glyph would not
	if !strings.Contains(bar, ShineText("1 query running")) {
		t.Error("running-query text is not the ultraloop shimmer")
	}
	for _, glyph := range []string{"◜", "◠", "◝", "◞", "◡", "◟"} {
		if strings.Contains(bar, glyph) {
			t.Errorf("bar still shows the old spinner glyph %q", glyph)
		}
	}
	if !m.animActive() {
		t.Error("a running query must keep the animation tick alive")
	}
}

// TestQueryInsideAnOpenedSubtree is the "queries don't work here" regression.
//
// Opening the outline at a subtree — which /goto and every zoom-to-node does —
// gives that subtree's root an in-memory item with no parent item above it. The
// candidate context marked it seen from that reading, so the parent link the
// database was about to supply never landed, the walk up to the outline root
// dead-ended there, and everything under it fell out of the default in:root
// scope. A query sitting in that branch found nothing, including its own
// siblings, which were sitting one row above it.
func TestQueryInsideAnOpenedSubtree(t *testing.T) {
	db := database.InitTestMemoryDB(t)
	if err := database.EnsureRoot(db); err != nil {
		t.Fatal(err)
	}
	for _, n := range []database.Node{
		{UUID: "branch", ParentUUID: database.RootUUID, Rank: 0, Name: "a branch"},
		{UUID: "q", ParentUUID: "branch", Rank: 0, Name: "#qol", Type: database.TypeQuery},
		{UUID: "hit", ParentUUID: "branch", Rank: 1, Name: "fix the caret #qol"},
		{UUID: "far", ParentUUID: database.RootUUID, Rank: 1, Name: "elsewhere #qol"},
		{UUID: "miss", ParentUUID: "branch", Rank: 2, Name: "no tag here"},
	} {
		if n.Type == "" {
			n.Type = database.TypeBullets
		}
		if err := n.Insert(db); err != nil {
			t.Fatal(err)
		}
	}
	// the outline is opened AT the branch, not at the forest root
	tr, err := loadTree(db, "branch")
	if err != nil {
		t.Fatal(err)
	}
	m := &Model{db: db, tree: tr, viewStack: []*item{tr.root}, width: 80, height: 24,
		chips: map[string]database.Chip{}}
	m.refreshRows()

	got := m.queryMatches(m.tree.byUUID["q"])
	found := map[string]bool{}
	for _, n := range got {
		found[n.UUID] = true
	}
	if !found["hit"] {
		t.Errorf("the query missed its own sibling: %v", found)
	}
	if !found["far"] {
		t.Errorf("the query missed a hit outside the opened subtree: %v", found)
	}
	if found["miss"] || found["q"] {
		t.Errorf("the query matched something it should not: %v", found)
	}
	if len(got) != 2 {
		t.Errorf("want 2 hits, got %d", len(got))
	}
}

// TestQueryScopeStillHolds: the fix restores ancestry, it does not weaken
// scoping — an in: scope still keeps hits to that subtree.
func TestQueryScopeStillHolds(t *testing.T) {
	db := database.InitTestMemoryDB(t)
	if err := database.EnsureRoot(db); err != nil {
		t.Fatal(err)
	}
	for _, n := range []database.Node{
		{UUID: "branch", ParentUUID: database.RootUUID, Rank: 0, Name: "a branch"},
		{UUID: "q", ParentUUID: "branch", Rank: 0, Name: "#qol in(branch)", Type: database.TypeQuery},
		{UUID: "hit", ParentUUID: "branch", Rank: 1, Name: "fix the caret #qol"},
		{UUID: "far", ParentUUID: database.RootUUID, Rank: 1, Name: "elsewhere #qol"},
	} {
		if n.Type == "" {
			n.Type = database.TypeBullets
		}
		if err := n.Insert(db); err != nil {
			t.Fatal(err)
		}
	}
	tr, err := loadTree(db, "branch")
	if err != nil {
		t.Fatal(err)
	}
	m := &Model{db: db, tree: tr, viewStack: []*item{tr.root}, width: 80, height: 24,
		chips: map[string]database.Chip{}}
	m.refreshRows()

	got := m.queryMatches(m.tree.byUUID["q"])
	if len(got) != 1 || got[0].UUID != "hit" {
		names := []string{}
		for _, n := range got {
			names = append(names, n.UUID)
		}
		t.Fatalf("scoped query returned %v, want just the in-scope hit", names)
	}
}
