package editor

import (
	"fmt"
	"sort"
	"testing"
	"time"

	"github.com/lflow/lflow/tui/database"
)

// semanticCtx builds a candidate context straight from name text, bypassing the
// tree so these tests exercise the matcher and nothing else.
func semanticCtx(docs map[string]string) *qCtx {
	var keys []string
	for k := range docs {
		keys = append(keys, k)
	}
	sort.Strings(keys) // fixed candidate order keeps failures reproducible
	ctx := &qCtx{parent: map[string]string{}, byUUID: map[string]*qCand{}}
	for _, k := range keys {
		ctx.cands = append(ctx.cands, qCand{uuid: k, searchName: docs[k]})
	}
	return ctx
}

func semanticSet(ctx *qCtx, phrase string) []string {
	var out []string
	for u := range semanticHits(ctx, phrase) {
		out = append(out, u)
	}
	sort.Strings(out)
	return out
}

// authCorpus is the standard vocabulary-mismatch fixture: D2 describes the same
// trouble as D1 without sharing a single word with it, and D4 is the node whose
// vocabulary bridges them.
func authCorpus() *qCtx {
	return semanticCtx(map[string]string{
		"D1": "fix the login bug in the auth service",
		"D2": "authentication service crashes on signin flow",
		"D3": "grocery list milk eggs bread",
		"D4": "auth service handles authentication and signin",
	})
}

// TestSemanticFindsVocabularyMismatch is the promise of the quoted atom: a node
// that shares NO word with the phrase still comes back, reached through the
// outline's own co-occurrence evidence.
func TestSemanticFindsVocabularyMismatch(t *testing.T) {
	got := semanticSet(authCorpus(), "auth bug")
	if !containsAll(got, "D2") {
		t.Errorf(`"auth bug" = %v, want it to reach D2 — the paraphrase is the point`, got)
	}
	if !containsAll(got, "D1") {
		t.Errorf(`"auth bug" = %v, want the literal match D1 as well`, got)
	}
	if containsAll(got, "D3") {
		t.Errorf(`"auth bug" = %v, want the unrelated grocery node left out`, got)
	}
}

// TestSemanticEmptyWhenNothingRelates: an absolute floor is what separates
// "everything is weak" from "everything is weak next to one strong hit". With
// no evidence at all the honest answer is no hits, not the top of the noise.
func TestSemanticEmptyWhenNothingRelates(t *testing.T) {
	if got := semanticSet(authCorpus(), "quarterly revenue forecast"); len(got) != 0 {
		t.Errorf(`unrelated phrase = %v, want no hits`, got)
	}
}

// TestSemanticLiteralPhraseAlwaysQualifies: fusion must never let a merely
// related node crowd out one that literally says it.
func TestSemanticLiteralPhraseAlwaysQualifies(t *testing.T) {
	ctx := semanticCtx(map[string]string{
		"exact": "the deployment pipeline is broken",
		"near":  "release tooling needs attention",
		"far":   "water the plants on friday",
	})
	got := semanticSet(ctx, "deployment pipeline broken")
	if !containsAll(got, "exact") {
		t.Fatalf("literal match = %v, want the exact node", got)
	}
	if containsAll(got, "far") {
		t.Errorf("literal match = %v, want the unrelated node left out", got)
	}
}

// TestSemanticTrigramBridgesMorphology covers the channel that carries a fresh
// outline, where no co-occurrence evidence exists yet: "auth" must still reach
// "authentication" on shared character shingles alone.
func TestSemanticTrigramBridgesMorphology(t *testing.T) {
	ctx := semanticCtx(map[string]string{
		"a": "authentication rewrite",
		"b": "gardening notes",
	})
	got := semanticSet(ctx, "auth")
	if !containsAll(got, "a") {
		t.Fatalf(`"auth" = %v, want the authentication node`, got)
	}
	if containsAll(got, "b") {
		t.Errorf(`"auth" = %v, want the unrelated node left out`, got)
	}
}

func TestSemanticStemFoldsInflections(t *testing.T) {
	cases := map[string]string{
		"deploying": "deploy", "deployed": "deploy", "deploys": "deploy",
		"shopping": "shop", // Porter undoubling — "shopp" would share nothing with "shop"
		"tries":    "try",
		"was":      "was", // too short to strip: stemming it would produce noise
		"pass":     "pass",
	}
	for in, want := range cases {
		if got := semanticStem(in); got != want {
			t.Errorf("semanticStem(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestSemanticIsDeterministic: index vectors are derived from node uuids rather
// than drawn at random, so a query cannot reshuffle its own results between two
// presses of alt+r.
func TestSemanticIsDeterministic(t *testing.T) {
	first := semanticSet(authCorpus(), "auth bug")
	for i := 0; i < 5; i++ {
		got := semanticSet(authCorpus(), "auth bug")
		if len(got) != len(first) {
			t.Fatalf("run %d = %v, want the stable %v", i, got, first)
		}
		for j := range got {
			if got[j] != first[j] {
				t.Fatalf("run %d = %v, want the stable %v", i, got, first)
			}
		}
	}
}

// TestSemanticCapsResults keeps a fuzzy atom from returning the whole outline
// when every node is plausibly on-topic.
func TestSemanticCapsResults(t *testing.T) {
	docs := map[string]string{}
	for i := 0; i < 200; i++ {
		docs[fmt.Sprintf("n%03d", i)] = fmt.Sprintf("deploy the release pipeline stage %d", i)
	}
	if got := semanticSet(semanticCtx(docs), "deploy release pipeline"); len(got) > semanticMaxHits {
		t.Fatalf("hits = %d, want at most %d", len(got), semanticMaxHits)
	}
}

// TestSemanticModelMemoizedPerContext: a streamed run re-evaluates after every
// database batch, so a query with two quoted atoms must not build the space twice.
func TestSemanticModelMemoizedPerContext(t *testing.T) {
	ctx := authCorpus()
	if first, second := ctx.semanticModel(), ctx.semanticModel(); first != second {
		t.Fatal("semanticModel rebuilt for an unchanged candidate set")
	}
	ctx.cands = append(ctx.cands, qCand{uuid: "D5", searchName: "a new node arrived"})
	if rebuilt := ctx.semanticModel(); len(rebuilt.docs) != 5 {
		t.Fatalf("model has %d docs after a batch grew the set, want 5", len(rebuilt.docs))
	}
}

// TestSemanticRunsInsideOneFrame: a quoted atom is evaluated on the UI goroutine
// after every streamed batch, so the whole space has to build faster than a
// terminal repaint.
func TestSemanticRunsInsideOneFrame(t *testing.T) {
	docs := map[string]string{}
	for i := 0; i < 500; i++ { // representative medium outline
		docs[fmt.Sprintf("n%03d", i)] = fmt.Sprintf(
			"node %d about deploying the release pipeline and its auth service", i)
	}
	ctx := semanticCtx(docs)
	start := time.Now()
	semanticHits(ctx, "why the build keeps failing")
	if elapsed := time.Since(start); elapsed > 100*time.Millisecond {
		t.Fatalf("semantic search over 500 nodes took %v, want well under a frame", elapsed)
	}
}

// TestQuerySemanticAtomEndToEnd drives a quoted phrase through the real query
// node, proving the atom is wired into evaluation and not just into the parser.
func TestQuerySemanticAtomEndToEnd(t *testing.T) {
	root := &item{uuid: database.RootUUID, name: "Root"}
	q := &item{uuid: "q", typ: database.TypeQuery, parent: root,
		name: `"login is broken"`}
	hit := &item{uuid: "hit", name: "authentication service keeps rejecting signin", parent: root}
	bridge := &item{uuid: "bridge", name: "login authentication signin service", parent: root}
	miss := &item{uuid: "miss", name: "water the plants on friday", parent: root}
	root.children = []*item{q, hit, bridge, miss}
	m := &Model{tree: &tree{root: root, snapshots: map[string]snapshot{}, externalNames: map[string]string{},
		byUUID: map[string]*item{database.RootUUID: root, "q": q,
			"hit": hit, "bridge": bridge, "miss": miss}}}

	var got []string
	for _, n := range m.queryMatches(q) {
		got = append(got, n.UUID)
	}
	if !containsAll(got, "hit") {
		t.Errorf("quoted query = %v, want the paraphrased node", got)
	}
	if containsAll(got, "miss") {
		t.Errorf("quoted query = %v, want the unrelated node left out", got)
	}
}

func containsAll(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}

// --- structure ---------------------------------------------------------------

// treeDoc is one node of a nested fixture: an uuid, its parent, and its text.
// A node with empty text is a structural grouping row — it still shapes the
// space through its fingerprint, but it is never itself a candidate.
type treeDoc struct{ uuid, parent, text string }

func treeCtx(docs []treeDoc) *qCtx {
	ctx := &qCtx{parent: map[string]string{}, byUUID: map[string]*qCand{}}
	for _, d := range docs {
		if d.parent != "" {
			ctx.parent[d.uuid] = d.parent
		}
		if d.text != "" {
			ctx.cands = append(ctx.cands, qCand{uuid: d.uuid, searchName: d.text, parent: d.parent})
		}
	}
	return ctx
}

// nestedOutline is two unrelated subtrees plus a loose node. "fix it" carries no
// topical word of its own — it exists to prove a bare row is still reachable
// through what it was filed under.
var nestedOutline = []treeDoc{
	{"groceries", "", "groceries"},
	{"milk", "groceries", "milk"},
	{"eggs", "groceries", "eggs"},
	{"bread", "groceries", "bread"},
	{"auth", "", "auth rewrite"},
	{"signin", "auth", "signin flow"},
	{"token", "auth", "token refresh"},
	{"fixit", "auth", "fix it"},
	{"weather", "", "the weather is nice today"},
}

// TestStructureParentTopicReachesChildren is the headline structural behavior: a
// query about the container returns what is filed inside it, including a row
// that shares no word with the query at all.
func TestStructureParentTopicReachesChildren(t *testing.T) {
	got := semanticSet(treeCtx(nestedOutline), "groceries")
	for _, want := range []string{"groceries", "milk", "eggs", "bread"} {
		if !containsAll(got, want) {
			t.Errorf(`"groceries" = %v, want it to reach %q`, got, want)
		}
	}
	if containsAll(got, "weather") || containsAll(got, "signin") {
		t.Errorf(`"groceries" = %v, want the other subtree left out`, got)
	}
}

// TestStructureReachesContentlessChild: "fix it" has no topical word of its own.
// Only its position in the tree can explain it, which is the point.
func TestStructureReachesContentlessChild(t *testing.T) {
	got := semanticSet(treeCtx(nestedOutline), "auth rewrite")
	if !containsAll(got, "fixit") {
		t.Fatalf(`"auth rewrite" = %v, want the contentless "fix it" row`, got)
	}
	if containsAll(got, "milk") {
		t.Errorf(`"auth rewrite" = %v, want the grocery subtree left out`, got)
	}
}

// TestStructureChildTopicReachesParent makes a container findable through what
// is filed inside it, not only through its own title.
func TestStructureChildTopicReachesParent(t *testing.T) {
	if got := semanticSet(treeCtx(nestedOutline), "milk"); !containsAll(got, "groceries") {
		t.Fatalf(`"milk" = %v, want the parent container`, got)
	}
}

// TestStructureDoesNotSpreadSideways is the constraint that makes the rest safe.
// Ancestry is shared by every node in a subtree, so a flat weight would make one
// child's query return all of its siblings.
func TestStructureDoesNotSpreadSideways(t *testing.T) {
	got := semanticSet(treeCtx(nestedOutline), "milk")
	for _, sibling := range []string{"eggs", "bread"} {
		if containsAll(got, sibling) {
			t.Errorf(`"milk" = %v, want the sibling %q left out`, got, sibling)
		}
	}
}

// TestStructureLargeBucketDoesNotFlood: a wide parent broadcasts weakly, so a
// query for one of its children returns that child rather than the whole
// subtree. Without the sqrt(children) damping this returned all 25 allowed hits.
func TestStructureLargeBucketDoesNotFlood(t *testing.T) {
	docs := []treeDoc{{"proj", "", "project alpha"}}
	for i := 0; i < 30; i++ {
		docs = append(docs, treeDoc{fmt.Sprintf("c%02d", i), "proj", fmt.Sprintf("task %d widget", i)})
	}
	docs = append(docs, treeDoc{"target", "proj", "rewrite the signin flow"})

	got := semanticSet(treeCtx(docs), "signin")
	if !containsAll(got, "target") {
		t.Fatalf(`"signin" = %v, want the node that actually says it`, got)
	}
	if len(got) > 3 {
		t.Errorf(`"signin" returned %d of 31 siblings (%v) — the subtree flooded`, len(got), got)
	}
}

// TestStructureEmptyAncestorStillBinds: fingerprints come from the uuid, not the
// text, so a bare grouping row with no words of its own still ties its children
// together — which is exactly what someone meant by creating it.
func TestStructureEmptyAncestorStillBinds(t *testing.T) {
	docs := []treeDoc{
		{"group", "", ""}, // a structural row: no text, never a candidate itself
		{"a", "group", "kubernetes ingress certificate"},
		{"b", "group", "renew the letsencrypt chain"},
		{"far", "", "sourdough starter feeding schedule"},
	}
	m := treeCtx(docs).semanticModel()
	if len(m.docs) != 3 {
		t.Fatalf("model has %d docs, want the empty grouping row excluded", len(m.docs))
	}
	bound := cosine(m.docVec["a"], m.docVec["b"])
	loose := cosine(m.docVec["a"], m.docVec["far"])
	if bound <= loose {
		t.Fatalf("siblings under an empty group cos=%.4f, unrelated cos=%.4f — the "+
			"grouping row must still bind", bound, loose)
	}
}

// TestStructureIgnoresForestRoot: every node descends from the root, so letting
// it broadcast would add the same component to every vector — diluting the space
// while discriminating nothing.
func TestStructureIgnoresForestRoot(t *testing.T) {
	docs := []treeDoc{
		{"a", database.RootUUID, "kubernetes ingress certificate"},
		{"b", database.RootUUID, "sourdough starter feeding schedule"},
	}
	m := treeCtx(docs).semanticModel()
	if c := cosine(m.docVec["a"], m.docVec["b"]); c > 0.01 {
		t.Fatalf("top-level nodes cos=%.4f, want ~0 — the root must not bind them", c)
	}
}

// TestStructureSurvivesCyclicAncestry: bad parent data must not hang the walk.
func TestStructureSurvivesCyclicAncestry(t *testing.T) {
	ctx := &qCtx{
		parent: map[string]string{"a": "b", "b": "a"},
		byUUID: map[string]*qCand{},
		cands: []qCand{
			{uuid: "a", searchName: "first node", parent: "b"},
			{uuid: "b", searchName: "second node", parent: "a"},
		},
	}
	done := make(chan bool, 1)
	go func() { semanticHits(ctx, "first"); done <- true }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("cyclic ancestry hung the semantic model build")
	}
}

// --- ranking ------------------------------------------------------------------

// TestQueryHitsSortByScore: a quoted atom ranks what it finds, and the result
// list has to keep that order. Name order would throw the ranking away — the
// point of scoring the candidates is that the best come out on top, not the ones
// beginning with "a".
func TestQueryHitsSortByScore(t *testing.T) {
	root := &item{uuid: database.RootUUID, name: "Root"}
	q := &item{uuid: "q", typ: database.TypeQuery, parent: root,
		name: `"the deployment pipeline keeps failing"`}
	// "aardvark…" sorts first by name and last by relevance; the exact match
	// sorts last by name and first by relevance.
	weak := &item{uuid: "weak", name: "aardvark pipeline notes", parent: root}
	exact := &item{uuid: "exact", name: "the deployment pipeline keeps failing", parent: root}
	mid := &item{uuid: "mid", name: "deployment pipeline flake", parent: root}
	root.children = []*item{q, weak, exact, mid}
	m := &Model{tree: &tree{root: root, snapshots: map[string]snapshot{}, externalNames: map[string]string{},
		byUUID: map[string]*item{database.RootUUID: root, "q": q,
			"weak": weak, "exact": exact, "mid": mid}}}

	got := m.queryMatches(q)
	if len(got) == 0 {
		t.Fatal("no hits")
	}
	if got[0].UUID != "exact" {
		var names []string
		for _, n := range got {
			names = append(names, n.UUID)
		}
		t.Fatalf("hit order = %v, want the exact match first (name order would give weak)", names)
	}
}

// TestQueryStarredStillPinsAboveScore: /star is a user's explicit override and
// outranks the matcher's opinion.
func TestQueryStarredStillPinsAboveScore(t *testing.T) {
	root := &item{uuid: database.RootUUID, name: "Root"}
	q := &item{uuid: "q", typ: database.TypeQuery, parent: root,
		name: `"the deployment pipeline keeps failing"`}
	exact := &item{uuid: "exact", name: "the deployment pipeline keeps failing", parent: root}
	pinned := &item{uuid: "pinned", name: "deployment pipeline flake", parent: root, starred: true}
	root.children = []*item{q, exact, pinned}
	m := &Model{tree: &tree{root: root, snapshots: map[string]snapshot{}, externalNames: map[string]string{},
		byUUID: map[string]*item{database.RootUUID: root, "q": q, "exact": exact, "pinned": pinned}}}

	got := m.queryMatches(q)
	if len(got) < 2 || got[0].UUID != "pinned" {
		t.Fatalf("starred hit must stay pinned above a better-scoring one, got %+v", got)
	}
}

// TestLexicalQueryKeepsNameOrder: a query with no quoted atom has no notion of
// "better", only "matched", so it must not pretend to rank.
func TestLexicalQueryKeepsNameOrder(t *testing.T) {
	root := &item{uuid: database.RootUUID, name: "Root"}
	q := &item{uuid: "q", typ: database.TypeQuery, parent: root, name: "pipeline"}
	b := &item{uuid: "b", name: "beta pipeline", parent: root}
	a := &item{uuid: "a", name: "alpha pipeline", parent: root}
	root.children = []*item{q, b, a}
	m := &Model{tree: &tree{root: root, snapshots: map[string]snapshot{}, externalNames: map[string]string{},
		byUUID: map[string]*item{database.RootUUID: root, "q": q, "a": a, "b": b}}}

	got := m.queryMatches(q)
	if len(got) != 2 || got[0].UUID != "a" {
		t.Fatalf("lexical hits = %+v, want name order (alpha before beta)", got)
	}
}

// TestSemanticScoreTakesStrongestPhrase: two quoted atoms are both describing the
// wanted thing, so a node one of them is sure about must not be dragged down by
// the other's indifference.
func TestSemanticScoreTakesStrongestPhrase(t *testing.T) {
	ctx := semanticCtx(map[string]string{"a": "one two three", "b": "four five six"})
	ctx.recordScore("a", 0.2)
	ctx.recordScore("a", 0.05)
	if got := ctx.score["a"]; got != 0.2 {
		t.Fatalf("score = %v, want the strongest claim (0.2) kept", got)
	}
}
