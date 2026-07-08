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


// TestQueryTreeBreadcrumbs drives the :tree: display flag: hits sort by
// ancestor path, and only the first hit of each group renders the breadcrumb.
func TestQueryTreeBreadcrumbs(t *testing.T) {
	root := &item{uuid: "root"}
	work := &item{uuid: "work", name: "work", parent: root}
	home := &item{uuid: "home", name: "home", parent: root}
	w1 := &item{uuid: "w1", name: "fix deploy", parent: work}
	w2 := &item{uuid: "w2", name: "fix tests", parent: work}
	h1 := &item{uuid: "h1", name: "fix the sink", parent: home}
	work.children = []*item{w1, w2}
	home.children = []*item{h1}
	q := &item{uuid: "q", typ: database.TypeQuery, parent: root, name: "fix :tree:"}
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

	// hits grouped by parent: home's hit and work's two hits are contiguous
	var srcs []string
	for _, c := range q.children {
		if c.mirrorOf != "" {
			srcs = append(srcs, c.mirrorOf)
		}
	}
	if len(srcs) != 3 {
		t.Fatalf("want 3 hits, got %v", srcs)
	}
	if !(srcs[0] == "h1" && srcs[1] == "w1" && srcs[2] == "w2") {
		t.Fatalf("hits not grouped by path: %v", srcs)
	}

	// crumbs: first of each group shows one, the second work hit shows none
	m.refreshRows()
	crumbs := map[string]string{}
	for i, r := range m.rows {
		if r.it.mirrorOf != "" {
			crumbs[r.it.mirrorOf] = m.rowCrumb(m.rows, i)
		}
	}
	if crumbs["h1"] != "home › " {
		t.Errorf("h1 crumb = %q, want 'home › '", crumbs["h1"])
	}
	if crumbs["w1"] != "work › " {
		t.Errorf("w1 crumb = %q, want 'work › '", crumbs["w1"])
	}
	if crumbs["w2"] != "" {
		t.Errorf("w2 crumb = %q, want suppressed (same group)", crumbs["w2"])
	}

	// without :tree: no crumbs render at all
	q.name = "fix"
	runQuery(m, q)
	m.refreshRows()
	for i, r := range m.rows {
		if r.it.mirrorOf != "" && m.rowCrumb(m.rows, i) != "" {
			t.Fatal("flat query must not render crumbs")
		}
	}
}
