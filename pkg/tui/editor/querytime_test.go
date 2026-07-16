package editor

import (
	"testing"
	"time"

	"github.com/lflow/lflow/pkg/tui/database"
)

func mustDate(s string) time.Time {
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		panic(err)
	}
	return t
}

func TestParseTimeQuerySplitsTokens(t *testing.T) {
	now := mustDate("2026-06-25")
	tq := parseTimeQuery("deploy :after:2026-06-01 :before:2026-06-20 notes", now)
	if tq.text != "deploy notes" {
		t.Errorf("residual text = %q, want %q", tq.text, "deploy notes")
	}
	if tq.after == nil || !tq.after.Equal(mustDate("2026-06-01")) {
		t.Errorf("after = %v, want 2026-06-01", tq.after)
	}
	// a date-only :before extends to the end of that day
	if tq.before == nil || tq.before.Hour() != 23 || tq.before.Day() != 20 {
		t.Errorf("before = %v, want end of 2026-06-20", tq.before)
	}
}

func TestMatchDatesWindow(t *testing.T) {
	now := mustDate("2026-06-25")
	tq := parseTimeQuery(":after:2026-06-01 :before:2026-06-20", now)

	inside := []time.Time{mustDate("2026-06-10")}
	if !tq.matchDates(inside) {
		t.Error("a date inside the window should match")
	}
	before := []time.Time{mustDate("2026-05-30")}
	if tq.matchDates(before) {
		t.Error("a date before the window should not match")
	}
	after := []time.Time{mustDate("2026-06-25")}
	if tq.matchDates(after) {
		t.Error("a date after the window should not match")
	}
	// any one date in range qualifies the node
	mixed := []time.Time{mustDate("2026-01-01"), mustDate("2026-06-10")}
	if !tq.matchDates(mixed) {
		t.Error("a node with one in-range date should match")
	}
}

func TestNodeDatesIncludeCreatedAndChips(t *testing.T) {
	now := mustDate("2026-06-25")
	m := &Model{}
	created := mustDate("2026-06-15")
	// name carries an inline date too (no chips map needed — ExpandAnchors is a
	// no-op without anchors, and detectAllDates finds the plain date)
	dates := m.nodeDates("shipped on 2026-03-02", created.UnixNano(), now)
	var sawCreated, sawInline bool
	for _, d := range dates {
		if d.Year() == 2026 && d.Month() == 6 && d.Day() == 15 {
			sawCreated = true
		}
		if d.Year() == 2026 && d.Month() == 3 && d.Day() == 2 {
			sawInline = true
		}
	}
	if !sawCreated {
		t.Error("node dates should include the creation time")
	}
	if !sawInline {
		t.Error("node dates should include an inline date in the name")
	}
}

// TestQueryMatchesTimeFilter drives the whole queryMatches path (in-memory, no
// DB) with a time filter: nodes are kept or dropped by their creation time and
// by date chips, AND-ed with any residual text.
func TestQueryMatchesTimeFilter(t *testing.T) {
	root := &item{uuid: "root"}
	// three notes created on different days
	mk := func(uuid, name, day string) *item {
		return &item{uuid: uuid, name: name, parent: root, addedOn: mustDate(day).UnixNano()}
	}
	old := mk("old", "deploy old", "2026-01-10")
	mid := mk("mid", "deploy mid", "2026-06-10")
	recent := mk("recent", "deploy recent", "2026-06-24")
	q := &item{uuid: "q", typ: database.TypeQuery, parent: root}
	root.children = []*item{old, mid, recent, q}
	tr := &tree{
		root:          root,
		snapshots:     map[string]snapshot{},
		externalNames: map[string]string{},
		byUUID:        map[string]*item{"old": old, "mid": mid, "recent": recent, "q": q},
	}
	m := &Model{tree: tr} // db nil → in-memory only

	names := func(ns []database.Node) map[string]bool {
		s := map[string]bool{}
		for _, n := range ns {
			s[n.UUID] = true
		}
		return s
	}

	// pure time window: only mid falls in [2026-06-01, 2026-06-20]
	q.name = ":after:2026-06-01 :before:2026-06-20"
	got := names(m.queryMatches(q))
	if !got["mid"] || got["old"] || got["recent"] {
		t.Errorf("window query matched wrong set: %v", got)
	}

	// text AND time: "deploy" matches all, time narrows to recent only
	q.name = "deploy :after:2026-06-20"
	got = names(m.queryMatches(q))
	if !got["recent"] || got["mid"] || got["old"] {
		t.Errorf("text+time query matched wrong set: %v", got)
	}

	// text that excludes everything yields nothing even if time matches
	q.name = "nonsense :after:2026-01-01"
	if got = names(m.queryMatches(q)); len(got) != 0 {
		t.Errorf("non-matching text should yield no hits, got %v", got)
	}
}

// TestQueryMatchesTypeFilter drives queryMatches with a ":type:" filter over the
// in-memory tree: only nodes of the named type(s) are kept, AND-ed with any text.
func TestQueryMatchesTypeFilter(t *testing.T) {
	root := &item{uuid: "root"}
	mk := func(uuid, name, typ string) *item {
		return &item{uuid: uuid, name: name, typ: typ, parent: root}
	}
	buyMilk := mk("t1", "buy milk", database.TypeTodo)
	shipIt := mk("t2", "ship it", database.TypeTodo)
	note := mk("b1", "buy a boat", database.TypeBullets)
	q := &item{uuid: "q", typ: database.TypeQuery, parent: root}
	root.children = []*item{buyMilk, shipIt, note, q}
	tr := &tree{
		root:          root,
		snapshots:     map[string]snapshot{},
		externalNames: map[string]string{},
		byUUID:        map[string]*item{"t1": buyMilk, "t2": shipIt, "b1": note, "q": q},
	}
	m := &Model{tree: tr} // db nil → in-memory only

	names := func(ns []database.Node) map[string]bool {
		s := map[string]bool{}
		for _, n := range ns {
			s[n.UUID] = true
		}
		return s
	}

	// pure type filter: both todos, not the bullet
	q.name = ":type:todo"
	got := names(m.queryMatches(q))
	if !got["t1"] || !got["t2"] || got["b1"] {
		t.Errorf("type filter matched wrong set: %v", got)
	}

	// type AND text: only the todo containing "buy"
	q.name = "buy :type:todo"
	got = names(m.queryMatches(q))
	if !got["t1"] || got["t2"] || got["b1"] {
		t.Errorf("text+type query matched wrong set: %v", got)
	}
}

// TestQueryHidesAgentRepliesUnlessTyped: with no ":type:" filter a query never
// matches search-hidden types (agent replies); naming the type explicitly is
// the one way to query them.
func TestQueryHidesAgentRepliesUnlessTyped(t *testing.T) {
	root := &item{uuid: "root"}
	plain := &item{uuid: "b1", name: "deploy notes", typ: database.TypeBullets, parent: root}
	reply := &item{uuid: "a1", name: "deploy looks fine", typ: database.TypeAgent, parent: root}
	q := &item{uuid: "q", typ: database.TypeQuery, parent: root}
	root.children = []*item{plain, reply, q}
	tr := &tree{
		root:          root,
		snapshots:     map[string]snapshot{},
		externalNames: map[string]string{},
		byUUID:        map[string]*item{"b1": plain, "a1": reply, "q": q},
	}
	m := &Model{tree: tr} // db nil → in-memory only

	names := func(ns []database.Node) map[string]bool {
		s := map[string]bool{}
		for _, n := range ns {
			s[n.UUID] = true
		}
		return s
	}

	q.name = "deploy"
	got := names(m.queryMatches(q))
	if !got["b1"] || got["a1"] {
		t.Errorf("plain query must skip agent replies, got %v", got)
	}

	q.name = "deploy :type:agent"
	got = names(m.queryMatches(q))
	if got["b1"] || !got["a1"] {
		t.Errorf(":type:agent must surface only the reply, got %v", got)
	}
}

// TestQueryBreadcrumbs drives the :breadcrumb: display flag: hits sort by
// ancestor path, and only the first hit of each group renders the breadcrumb.
func TestQueryBreadcrumbs(t *testing.T) {
	root := &item{uuid: "root"}
	work := &item{uuid: "work", name: "work", parent: root}
	home := &item{uuid: "home", name: "home", parent: root}
	w1 := &item{uuid: "w1", name: "fix deploy", parent: work}
	w2 := &item{uuid: "w2", name: "fix tests", parent: work}
	h1 := &item{uuid: "h1", name: "fix the sink", parent: home}
	work.children = []*item{w1, w2}
	home.children = []*item{h1}
	q := &item{uuid: "q", typ: database.TypeQuery, parent: root, name: "fix :breadcrumb:"}
	root.children = []*item{work, home, q}
	tr := &tree{
		root:          root,
		snapshots:     map[string]snapshot{},
		externalNames: map[string]string{},
		byUUID: map[string]*item{"root": root, "work": work, "home": home,
			"w1": w1, "w2": w2, "h1": h1, "q": q},
	}
	m := &Model{tree: tr, viewStack: []*item{root}, width: 100, height: 40}

	runQuery(m, q)

	// Breadcrumbs are real nested generated rows, not text prepended to hits:
	// query → home → h1 and query → work → w1,w2.
	if len(q.children) != 2 || q.children[0].mirrorOf != "home" || q.children[1].mirrorOf != "work" {
		t.Fatalf("top breadcrumb rows = %v, want home/work", mirrorSources(q))
	}
	homeCrumb, workCrumb := q.children[0], q.children[1]
	if !homeCrumb.readonly || !homeCrumb.structureLocked || !workCrumb.readonly || !workCrumb.structureLocked {
		t.Fatal("breadcrumb rows must carry content + structural locks")
	}
	if got := mirrorSources(homeCrumb); len(got) != 1 || got[0] != "h1" {
		t.Fatalf("home children = %v", got)
	}
	if got := mirrorSources(workCrumb); len(got) != 2 || got[0] != "w1" || got[1] != "w2" {
		t.Fatalf("work children = %v", got)
	}
	for _, hit := range append(homeCrumb.children, workCrumb.children...) {
		if hit.readonly || !hit.structureLocked {
			t.Fatal("hits must be movable-locked without wearing the gray content lock")
		}
	}
	if queryHitCount(q) != 3 {
		t.Fatalf("hit count includes breadcrumbs: %d", queryHitCount(q))
	}

	// Without :breadcrumb:, hits return to one flat level.
	q.name = "fix"
	runQuery(m, q)
	if got := mirrorSources(q); len(got) != 3 {
		t.Fatalf("flat query mirrors = %v", got)
	}
}

// TestQueryBoolOps covers ||, &&, and the > descendant operator.
func TestQueryBoolOps(t *testing.T) {
	root := &item{uuid: "root"}
	proj := &item{uuid: "proj", name: "project alpha", typ: database.TypeBullets, parent: root}
	todo1 := &item{uuid: "t1", name: "ship it", typ: database.TypeTodo, parent: proj}
	todo2 := &item{uuid: "t2", name: "unrelated todo", typ: database.TypeTodo, parent: root}
	note := &item{uuid: "n1", name: "release notes", typ: database.TypeBullets, parent: root}
	other := &item{uuid: "o1", name: "deploy scripts", typ: database.TypeBullets, parent: root}
	proj.children = []*item{todo1}
	q := &item{uuid: "q", typ: database.TypeQuery, parent: root}
	root.children = []*item{proj, todo2, note, other, q}
	tr := &tree{
		root:          root,
		snapshots:     map[string]snapshot{},
		externalNames: map[string]string{},
		byUUID: map[string]*item{
			"root": root, "proj": proj, "t1": todo1, "t2": todo2,
			"n1": note, "o1": other, "q": q,
		},
	}
	m := &Model{tree: tr}

	names := func(ns []database.Node) map[string]bool {
		s := map[string]bool{}
		for _, n := range ns {
			s[n.UUID] = true
		}
		return s
	}

	// OR: deploy || release
	q.name = "deploy || release"
	got := names(m.queryMatches(q))
	if !got["o1"] || !got["n1"] || got["t1"] {
		t.Errorf("OR matched wrong set: %v", got)
	}

	// AND with type: (deploy || release) && :type:todo — neither is a todo
	q.name = "(deploy || release) && :type:todo"
	got = names(m.queryMatches(q))
	if len(got) != 0 {
		t.Errorf("AND type should empty, got %v", got)
	}

	// type OR
	q.name = ":type:todo || :type:log"
	got = names(m.queryMatches(q))
	if !got["t1"] || !got["t2"] || got["n1"] {
		t.Errorf("type OR matched wrong set: %v", got)
	}

	// pipe: under project, only todos
	q.name = "project > :type:todo"
	got = names(m.queryMatches(q))
	if !got["t1"] || got["t2"] {
		t.Errorf("pipe should keep only descendant todo, got %v", got)
	}

	// pipe with OR left side
	q.name = "(project || release) > :type:todo"
	got = names(m.queryMatches(q))
	if !got["t1"] || got["t2"] {
		t.Errorf("pipe+OR left: %v", got)
	}
}
