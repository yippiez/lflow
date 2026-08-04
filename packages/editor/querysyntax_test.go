package editor

import (
	"testing"
	"time"

	"github.com/lflow/lflow/packages/database"
)

// newSyntaxTree builds a flat outline whose children cover every qualifier the
// flat syntax exposes, so one fixture can drive the whole grammar.
func newSyntaxTree() (*Model, *item) {
	root := &item{uuid: database.RootUUID, name: "Root"}
	q := &item{uuid: "q", typ: database.TypeQuery, parent: root}
	kids := []*item{
		{uuid: "a", name: "buy apples", typ: database.TypeTodo, parent: root},
		{uuid: "b", name: "buy bread", typ: database.TypeTodo, parent: root, completedAt: 1, starred: true},
		{uuid: "c", name: "read carrots paper", typ: database.TypeBullets, parent: root, note: "a note"},
		{uuid: "d", name: "buy carrots", typ: database.TypeBullets, parent: root},
	}
	root.children = append([]*item{q}, kids...)
	byUUID := map[string]*item{database.RootUUID: root, "q": q}
	for _, k := range kids {
		byUUID[k.uuid] = k
	}
	return &Model{tree: &tree{root: root, snapshots: map[string]snapshot{},
		externalNames: map[string]string{}, byUUID: byUUID}}, q
}

func hitUUIDs(m *Model, q *item, name string) []string {
	q.name = name
	var out []string
	for _, n := range m.queryMatches(q) {
		out = append(out, n.UUID)
	}
	return out
}

func sameSet(got []string, want ...string) bool {
	if len(got) != len(want) {
		return false
	}
	seen := map[string]bool{}
	for _, g := range got {
		seen[g] = true
	}
	for _, w := range want {
		if !seen[w] {
			return false
		}
	}
	return true
}

// TestBracketQualifiers is the point of the syntax rework: a filter is
// `key(value)`, not a `:key:value` sandwich.
func TestBracketQualifiers(t *testing.T) {
	m, q := newSyntaxTree()
	if got := hitUUIDs(m, q, "type(todo)"); !sameSet(got, "a", "b") {
		t.Fatalf("type(todo) = %v, want the two todos", got)
	}
	if got := hitUUIDs(m, q, "buy type(bullets)"); !sameSet(got, "d") {
		t.Fatalf("buy type(bullets) = %v, want [d]", got)
	}
}

// TestBracketValueMayContainSpaces is what the bracket form buys over a colon.
// A clock time is separated from its date by a space, so "before:2026-06-20
// 14:30" split into a filter and a stray word and silently lost the time;
// bracketing holds the value together.
func TestBracketValueMayContainSpaces(t *testing.T) {
	now := mustDate("2026-06-25")
	tq := flattenQuery("before(2026-06-20 14:30)", now)
	if tq.before == nil {
		t.Fatal("before(2026-06-20 14:30) did not parse")
	}
	if h, m := tq.before.Hour(), tq.before.Minute(); h != 14 || m != 30 {
		t.Errorf("before = %02d:%02d, want the 14:30 inside the brackets kept", h, m)
	}
	if tq.text != "" {
		t.Errorf("residual text = %q, want the bracketed value fully consumed", tq.text)
	}
}

func TestBooleanFunctionsComposeQueries(t *testing.T) {
	m, q := newSyntaxTree()
	if got := hitUUIDs(m, q, "and(buy, type(todo))"); !sameSet(got, "a", "b") {
		t.Fatalf("and() = %v, want the two todo purchases", got)
	}
	if got := hitUUIDs(m, q, "or(apples, carrots)"); !sameSet(got, "a", "c", "d") {
		t.Fatalf("or() = %v, want apples or carrots", got)
	}
	if got := hitUUIDs(m, q, "and(buy, or(apples, carrots))"); !sameSet(got, "a", "d") {
		t.Fatalf("nested boolean calls = %v, want [a d]", got)
	}
	// The symbolic forms remain aliases because query text is persisted.
	if got := hitUUIDs(m, q, "buy && (apples || carrots)"); !sameSet(got, "a", "d") {
		t.Fatalf("legacy operators = %v, want [a d]", got)
	}
}

// TestGroupingParensStillGroup: only a "(" glued to a known qualifier key opens
// a value — a standalone one keeps grouping the boolean expression.
func TestGroupingParensStillGroup(t *testing.T) {
	m, q := newSyntaxTree()
	if got := hitUUIDs(m, q, "(apples || bread) && type(todo)"); !sameSet(got, "a", "b") {
		t.Fatalf("grouped or = %v, want both todos", got)
	}
	// a space before the "(" makes it a group again, not a value
	if got := hitUUIDs(m, q, "type (todo)"); sameSet(got, "a", "b") {
		t.Errorf("type (todo) = %v, want it NOT read as a qualifier", got)
	}
}

// TestLegacyColonSpellingsStillParse guards the compatibility invariant: query
// text is persisted node text, so pre-existing queries must keep matching.
func TestLegacyColonSpellingsStillParse(t *testing.T) {
	m, q := newSyntaxTree()
	want := hitUUIDs(m, q, "type(todo)")
	for _, legacy := range []string{"type:todo", ":type:todo"} {
		if got := hitUUIDs(m, q, legacy); !sameSet(got, want...) {
			t.Errorf("legacy %s = %v, want same as type(todo) %v", legacy, got, want)
		}
	}
	for _, tree := range []string{"fix as(tree)", "fix as:tree", "fix :breadcrumb:"} {
		if pq := parseQuery(tree, time.Now()); !pq.breadcrumb {
			t.Errorf("%q must set the tree display", tree)
		}
	}
	for _, list := range []string{"fix as(list)", "fix as:list", "fix :list:"} {
		if pq := parseQuery(list, time.Now()); pq.breadcrumb {
			t.Errorf("%q must clear the tree display", list)
		}
	}
}

// TestUnknownQualifierStaysText keeps the flat syntax from swallowing ordinary
// text — only known keys become filters.
func TestUnknownQualifierStaysText(t *testing.T) {
	root := &item{uuid: database.RootUUID, name: "Root"}
	q := &item{uuid: "q", typ: database.TypeQuery, parent: root}
	hit := &item{uuid: "hit", name: "ratio:1.5 measured", parent: root}
	miss := &item{uuid: "miss", name: "ratio unmeasured", parent: root}
	root.children = []*item{q, hit, miss}
	m := &Model{tree: &tree{root: root, snapshots: map[string]snapshot{}, externalNames: map[string]string{},
		byUUID: map[string]*item{database.RootUUID: root, "q": q, "hit": hit, "miss": miss}}}

	if got := hitUUIDs(m, q, "ratio:1.5"); !sameSet(got, "hit") {
		t.Fatalf("ratio:1.5 = %v, want it searched as plain text", got)
	}
}

func TestStateQualifiers(t *testing.T) {
	m, q := newSyntaxTree()
	cases := []struct {
		query string
		want  []string
	}{
		{"is(starred)", []string{"b"}},
		{"is(done)", []string{"b"}},
		{"is(open)", []string{"a", "c", "d"}},
		{"has(note)", []string{"c"}},
		{"buy is(open)", []string{"a", "d"}},
	}
	for _, tc := range cases {
		if got := hitUUIDs(m, q, tc.query); !sameSet(got, tc.want...) {
			t.Errorf("%s = %v, want %v", tc.query, got, tc.want)
		}
	}
	// an unknown predicate matches nothing rather than everything
	if got := hitUUIDs(m, q, "is(nonsense)"); len(got) != 0 {
		t.Errorf("is(nonsense) = %v, want no hits", got)
	}
}

func TestNegationExcludesAtoms(t *testing.T) {
	m, q := newSyntaxTree()
	if got := hitUUIDs(m, q, "buy -type(todo)"); !sameSet(got, "d") {
		t.Fatalf("buy -type(todo) = %v, want [d]", got)
	}
	// an interior hyphen is not an operator — dates and hyphenated words survive
	if pq := flattenQuery("after(2026-06-01)", time.Now()); pq.after == nil {
		t.Fatal("after(2026-06-01) must parse as a date, not a negation")
	}
}

func TestQuotedPhraseParsesAsSemanticAtom(t *testing.T) {
	pq := parseQuery(`"why the build broke"`, time.Now())
	sem, ok := pq.expr.(*qSemantic)
	if !ok {
		t.Fatalf("quoted phrase parsed as %T, want *qSemantic", pq.expr)
	}
	if sem.phrase != "why the build broke" {
		t.Fatalf("phrase = %q, want the text between the quotes", sem.phrase)
	}
	// the phrase stays one atom even though it contains spaces and a qualifier
	// word — quoting suspends the rest of the grammar
	pq = parseQuery(`deploy "type(todo) && x"`, time.Now())
	and, ok := pq.expr.(*qAnd)
	if !ok || len(and.kids) != 2 {
		t.Fatalf("expr = %#v, want text AND one semantic atom", pq.expr)
	}
	if _, ok := and.kids[1].(*qSemantic); !ok {
		t.Fatalf("second atom = %T, want *qSemantic", and.kids[1])
	}
}

// TestUnterminatedQuoteStaysLive keeps a query runnable while it is being typed:
// the closing quote has not been pressed yet, but the atom already means
// something.
func TestUnterminatedQuoteStaysLive(t *testing.T) {
	pq := parseQuery(`"login trouble`, time.Now())
	sem, ok := pq.expr.(*qSemantic)
	if !ok {
		t.Fatalf("unterminated quote parsed as %T, want *qSemantic", pq.expr)
	}
	if sem.phrase != "login trouble" {
		t.Fatalf("phrase = %q", sem.phrase)
	}
}

// TestQuerySpanColorTintsQuotesOnly: the marks are the operator, the phrase
// inside them is ordinary text.
func TestQuerySpanColorTintsQuotesOnly(t *testing.T) {
	runes := []rune(`fix "build broke"`)
	got := querySpanColor(nil, runes)
	if len(got) != 2 {
		t.Fatalf("tinted %d runes, want exactly the two quote marks: %v", len(got), got)
	}
	for i, c := range got {
		if runes[i] != '"' {
			t.Errorf("tinted rune %d (%q), want a quote mark", i, runes[i])
		}
		if c != cYellow {
			t.Errorf("quote color = %q, want yellow", c)
		}
	}
}

func TestFlatScopeQualifier(t *testing.T) {
	root := &item{uuid: database.RootUUID, name: "Root"}
	scope := &item{uuid: "scope", name: "scope", parent: root}
	q := &item{uuid: "q", typ: database.TypeQuery, name: "needle", parent: scope}
	inside := &item{uuid: "inside", name: "needle inside", parent: scope}
	outside := &item{uuid: "outside", name: "needle outside", parent: root}
	scope.children = []*item{q, inside}
	root.children = []*item{scope, outside}
	m := &Model{tree: &tree{root: root, snapshots: map[string]snapshot{}, externalNames: map[string]string{},
		byUUID: map[string]*item{database.RootUUID: root, "scope": scope, "q": q,
			"inside": inside, "outside": outside}},
		chips: map[string]database.Chip{
			"link": {ID: "link", Kind: chipKindLink, Value: nodeLinkURI("scope"), Label: "scope"},
		}}

	for _, spelling := range []string{
		"needle in(" + database.ChipAnchor("link") + ")",
		"needle in:" + database.ChipAnchor("link"),
		"needle :in:" + database.ChipAnchor("link"),
	} {
		if got := hitUUIDs(m, q, spelling); !sameSet(got, "inside") {
			t.Errorf("scope %q = %v, want [inside]", spelling, got)
		}
	}
	// the selector must never leak into text matching
	if raw, _ := m.queryTextAndScope(q); raw != "needle" {
		t.Fatalf("searchable text = %q, want the selector stripped", raw)
	}
}
