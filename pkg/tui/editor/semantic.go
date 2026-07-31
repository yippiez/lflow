package editor

import (
	"hash/fnv"
	"math"
	"sort"
	"strings"
	"unicode"

	"github.com/lflow/lflow/pkg/tui/database"
)

// Semantic search — the `"quoted phrase"` atom of the query language.
//
// A quoted phrase matches by MEANING rather than by letters: `"why the build
// broke"` can return a node reading "release pipeline keeps failing" even though
// the two share no word. The model behind it is distributional — words that keep
// turning up in the same nodes end up near each other — and it is built from the
// outline itself, on the fly, every run.
//
// WARNING (invariant): semantic search stays self-contained. No model file, no
// embedding download, no network call, no daemon round-trip — a quoted phrase
// must resolve from the candidate set already in memory, or the query node stops
// being something you can run on a plane. The vector space is therefore
// RANDOM INDEXING (Kanerva/Sahlgren): each node gets a sparse random ±1 index
// vector, a word's vector is the weighted sum of the vectors of the nodes it
// appears in, and a node's vector is the weighted sum of its words'. That
// approximates the term-document SVD of LSA at a fraction of the cost, without
// ever materializing the full matrix.
//
// Three channels score every candidate and their RANKINGS fuse with Reciprocal
// Rank Fusion, which needs no shared scale between them:
//
//	vector    random-indexing cosine — the actual semantics
//	lexical   Okapi BM25 — a phrase that IS present literally always wins
//	shape     character-trigram Dice — "auth" ↔ "authentication", and typos
//
// The shape channel is what carries a short outline row when the vector channel
// has nothing to work with. Random indexing can only relate two words that the
// outline has already put near each other; on a fresh outline that evidence does
// not exist yet, and a purely distributional matcher would return nothing.
//
// # Structure is evidence
//
// A flat bag-of-nodes model throws away the one thing an OUTLINE knows that a
// pile of documents does not: a child is written about its parent. "milk" filed
// under "grocery list" is grocery-milk; "fix it" under "auth service" is about
// auth. That relation is free, it is everywhere, and the words alone cannot
// recover it — an outline row is a handful of words precisely BECAUSE its
// ancestors already said the rest.
//
// So the tree enters the vector channel twice, at the two points where it means
// two different things:
//
//	pass 1  a node's fingerprint is its own PLUS its ancestors', decayed, so
//	        terms filed under one parent share a component of the space.
//	pass 2  a node's vector blends a damped share of its ancestors' and its
//	        children's own vectors.
//
// What that buys, measured on a nested fixture (semantic_test.go):
//
//	parent topic → children   0.38   "groceries" reaches a bare "milk" row
//	child topic → parent      0.36   "milk" reaches the "groceries" container
//	sibling → sibling         0.11   BELOW the floor, deliberately
//
// The sibling number is the whole design constraint. Ancestry is shared by every
// node in a subtree, so weighting it flatly makes a large subtree collapse into
// one blob where everything is "related" to everything — a query for one child
// came back with all thirty of its siblings and drowned the exact match. Damping
// each ancestor by sqrt(children) is what fixes it: a parent of three is making a
// claim about all three, a bucket of fifty is barely making one about any. Nodes
// still reach their parents and their children; they no longer reach sideways.
//
// An ancestor that is EMPTY still has a fingerprint (fingerprints come from the
// uuid, not the text), so a bare structural grouping row still binds its
// children together — which is exactly what a person meant by making it.
//
// The lexical and shape channels stay strictly local. They are the "it really
// says this" evidence, and inheriting ancestor text into them would make every
// node in a subtree a literal match for its parent's words.

const (
	semanticDims    = 256  // random-index width; JL lemma keeps distances at this size
	semanticSeeds   = 6    // non-zero entries per index vector (sparse ternary)
	semanticMaxHits = 25   // a phrase is a fuzzy atom — it must not return the outline
	semanticRRFK    = 60.0 // standard RRF damping constant

	// Qualifying floors are ABSOLUTE, not a fraction of the best score. A
	// relative floor cannot tell "everything here is weak" from "everything here
	// is weak RELATIVE to one strong hit": a phrase with no match at all would
	// rank noise against noise and return the top of it, and a phrase that DID
	// match one node literally would bury every related node under it — losing
	// exactly the cross-vocabulary hits quoting is for.
	semanticMinCos  = 0.15 // below this the vector space is not claiming a relation
	semanticMinDice = 0.18 // below this the shared trigrams are incidental

	// Structural constants. All three are deliberately well under 1: ancestry is
	// a hint about what a node is about, never a claim that the node says it.
	semanticParentDecay  = 0.5 // fingerprint inherited per hop up the tree
	semanticContextHops  = 3   // ancestors deep enough to matter; beyond is topic drift
	semanticAncestorPull = 0.4 // how much a node is described by what it is filed under
	semanticChildPull    = 0.2 // how much a node is described by what is filed inside it
)

// bm25K1 / bm25B are the textbook Okapi BM25 constants: term-frequency
// saturation and length normalization.
const (
	bm25K1 = 1.2
	bm25B  = 0.75
)

// semanticStop are words carrying no topical signal. They are dropped from both
// sides, so "why the build broke" and "the release keeps failing" compare on
// build/broke against release/keeps/failing rather than on "the".
var semanticStop = map[string]bool{
	"a": true, "an": true, "and": true, "any": true, "are": true, "as": true,
	"at": true, "be": true, "been": true, "but": true, "by": true, "can": true,
	"did": true, "do": true, "does": true, "for": true, "from": true, "get": true,
	"had": true, "has": true, "have": true, "how": true, "i": true, "if": true,
	"in": true, "into": true, "is": true, "it": true, "its": true, "me": true,
	"my": true, "no": true, "not": true, "of": true, "on": true, "or": true,
	"our": true, "out": true, "so": true, "than": true, "that": true, "the": true,
	"their": true, "them": true, "then": true, "there": true, "these": true,
	"they": true, "this": true, "to": true, "too": true, "up": true, "was": true,
	"we": true, "were": true, "what": true, "when": true, "which": true,
	"who": true, "why": true, "will": true, "with": true, "would": true,
	"you": true, "your": true,
}

// semanticDoc is one candidate reduced to its term frequencies and the
// character trigrams of its text.
type semanticDoc struct {
	uuid string
	tf   map[string]float64
	tri  map[string]bool
	len  float64
}

// semanticModel is the vector space for one query run. It is derived entirely
// from the candidates, so it drifts with the outline instead of encoding some
// frozen snapshot of English.
type semanticModel struct {
	docs    []semanticDoc
	df      map[string]int
	kin     *kinship // the outline's shape
	termVec map[string][]float64
	docVec  map[string][]float64
	avgLen  float64
	n       int
}

// semanticHits is the qSemantic evaluator: the candidates whose meaning is close
// enough to phrase, capped so a fuzzy atom stays a filter and not a firehose.
func semanticHits(ctx *qCtx, phrase string) map[string]bool {
	out := map[string]bool{}
	terms := semanticTerms(phrase)
	if len(terms) == 0 || len(ctx.cands) == 0 {
		return out
	}
	model := ctx.semanticModel()

	qVec := model.queryVector(terms)
	qTri := trigrams(terms)
	cos := make(map[string]float64, len(model.docs))
	lex := make(map[string]float64, len(model.docs))
	shape := make(map[string]float64, len(model.docs))

	// A candidate qualifies on ANY channel. Requiring agreement would throw away
	// exactly the vocabulary-mismatch hits that make the phrase worth quoting —
	// the channel that finds them is by definition the one the others missed.
	var qualified []string
	for _, d := range model.docs {
		c := cosine(qVec, model.docVec[d.uuid])
		l := model.bm25(terms, d)
		s := diceCoefficient(qTri, d.tri)
		cos[d.uuid], lex[d.uuid], shape[d.uuid] = c, l, s
		// Any BM25 at all is a shared, idf-weighted word — real evidence on its
		// own terms, so it needs no floor beyond being non-zero.
		if c >= semanticMinCos || l > 0 || s >= semanticMinDice {
			qualified = append(qualified, d.uuid)
		}
	}
	if len(qualified) == 0 {
		return out // nothing here is about that. An empty result is an answer.
	}

	fused := reciprocalRankFusion(qualified, cos, lex, shape)
	sort.SliceStable(qualified, func(i, j int) bool {
		if fused[qualified[i]] != fused[qualified[j]] {
			return fused[qualified[i]] > fused[qualified[j]]
		}
		return qualified[i] < qualified[j] // stable across runs, for tests
	})
	if len(qualified) > semanticMaxHits {
		qualified = qualified[:semanticMaxHits]
	}
	for _, u := range qualified {
		out[u] = true
	}
	return out
}

// semanticModel memoizes the vector space on the context. A streamed run
// re-evaluates after every database batch, so the model is rebuilt only when the
// candidate set actually grew.
func (ctx *qCtx) semanticModel() *semanticModel {
	if ctx.sem != nil && ctx.semN == len(ctx.cands) {
		return ctx.sem
	}
	ctx.sem = buildSemanticModel(ctx.cands, ctx.parent)
	ctx.semN = len(ctx.cands)
	return ctx.sem
}

// buildSemanticModel runs the two reflective passes: nodes give words their
// vectors, then words give nodes theirs. After the second pass a node and a
// phrase describing it land near each other even with no word in common. The
// parent map carries the outline's shape into both passes — see "Structure is
// evidence" above.
func buildSemanticModel(cands []qCand, parent map[string]string) *semanticModel {
	kin := newKinship(parent)
	m := &semanticModel{df: map[string]int{}, kin: kin,
		termVec: map[string][]float64{}, docVec: map[string][]float64{}}

	for _, c := range cands {
		terms := semanticTerms(c.searchName + " " + c.searchNote)
		tf := map[string]float64{}
		for _, t := range terms {
			tf[t]++
		}
		if len(tf) == 0 {
			continue
		}
		var n float64
		for t, f := range tf {
			m.df[t]++
			n += f
		}
		m.docs = append(m.docs, semanticDoc{uuid: c.uuid, tf: tf, tri: trigrams(terms), len: n})
		m.avgLen += n
	}
	m.n = len(m.docs)
	if m.n == 0 {
		return m
	}
	m.avgLen /= float64(m.n)

	// pass 1 — a word IS the nodes it appears in, each node standing for its own
	// position in the tree as well as itself
	for _, d := range m.docs {
		idx := contextFingerprint(d.uuid, kin)
		for t, f := range d.tf {
			v := m.termVec[t]
			if v == nil {
				v = make([]float64, semanticDims)
				m.termVec[t] = v
			}
			w := m.weight(t, f)
			for _, e := range idx {
				v[e.dim] += w * e.weight
			}
		}
	}
	for t, v := range m.termVec {
		m.termVec[t] = unit(v)
	}

	// pass 2 — a node IS the words it uses, now expressed in word space
	own := make(map[string][]float64, len(m.docs))
	for _, d := range m.docs {
		v := make([]float64, semanticDims)
		for t, f := range d.tf {
			tv := m.termVec[t]
			w := m.weight(t, f)
			for i := range v {
				v[i] += w * tv[i]
			}
		}
		own[d.uuid] = unit(v)
	}
	m.blendKin(own, kin)
	return m
}

// blendKin folds a node's neighbourhood into its vector: a damped share of each
// ancestor's topic on the way down, and of its children's on the way up.
//
// Both sides read the UNBLENDED own-vectors, never each other. Blending from
// already-blended vectors would make the result depend on iteration order, and
// would let a topic seep arbitrarily far through the tree instead of stopping
// where the decay says it stops.
func (m *semanticModel) blendKin(own map[string][]float64, kin *kinship) {
	childSum := make(map[string][]float64, len(own))
	for uuid, v := range own {
		p := kin.parent[uuid]
		if p == "" || own[p] == nil {
			continue
		}
		acc := childSum[p]
		if acc == nil {
			acc = make([]float64, semanticDims)
			childSum[p] = acc
		}
		for i := range acc {
			acc[i] += v[i]
		}
	}

	for uuid, mine := range own {
		v := make([]float64, semanticDims)
		copy(v, mine)
		// what this node is filed under
		kin.walkAncestors(uuid, func(p string, weight float64) {
			av := own[p]
			if av == nil {
				return
			}
			for i := range v {
				v[i] += semanticAncestorPull * weight * av[i]
			}
		})
		// what is filed inside it — normalized first, so a container with fifty
		// children is described by their CENTROID rather than swamped by their bulk
		if kids := childSum[uuid]; kids != nil {
			ck := unit(append([]float64(nil), kids...))
			for i := range v {
				v[i] += semanticChildPull * ck[i]
			}
		}
		m.docVec[uuid] = unit(v)
	}
}

// queryVector places a phrase in the same space. Words the outline has never
// seen contribute nothing — there is no evidence about where they belong — which
// is why the lexical channel exists to catch them.
func (m *semanticModel) queryVector(terms []string) []float64 {
	v := make([]float64, semanticDims)
	seen := map[string]float64{}
	for _, t := range terms {
		seen[t]++
	}
	for t, f := range seen {
		tv := m.termVec[t]
		if tv == nil {
			continue
		}
		w := m.weight(t, f)
		for i := range v {
			v[i] += w * tv[i]
		}
	}
	return unit(v)
}

// weight is tf-idf with a sublinear term frequency: a word repeated five times
// is more important than one used once, but not five times more.
func (m *semanticModel) weight(term string, tf float64) float64 {
	return (1 + math.Log(tf)) * m.idf(term)
}

func (m *semanticModel) idf(term string) float64 {
	df := float64(m.df[term])
	if df <= 0 {
		return 0
	}
	return math.Log(1 + float64(m.n)/df)
}

// bm25 is the lexical half of the hybrid: classic Okapi scoring of the phrase's
// literal words against one node.
func (m *semanticModel) bm25(terms []string, d semanticDoc) float64 {
	var score float64
	for _, t := range terms {
		f := d.tf[t]
		if f == 0 {
			continue
		}
		norm := f * (bm25K1 + 1) /
			(f + bm25K1*(1-bm25B+bm25B*d.len/math.Max(m.avgLen, 1)))
		score += m.idf(t) * norm
	}
	return score
}

// trigrams is the set of 3-character shingles of terms, each term padded so its
// first and last letters are shingled too. Shared shingles are what let "auth"
// reach "authentication" without either side knowing the other exists.
func trigrams(terms []string) map[string]bool {
	out := map[string]bool{}
	for _, t := range terms {
		padded := []rune("  " + t + "  ")
		for i := 0; i+3 <= len(padded); i++ {
			out[string(padded[i:i+3])] = true
		}
	}
	return out
}

// diceCoefficient is 2|A∩B| / (|A|+|B|) — the overlap measure that, unlike
// Jaccard, does not punish a short query for being short.
func diceCoefficient(a, b map[string]bool) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	small, large := a, b
	if len(large) < len(small) {
		small, large = large, small
	}
	var shared float64
	for g := range small {
		if large[g] {
			shared++
		}
	}
	return 2 * shared / float64(len(a)+len(b))
}

// reciprocalRankFusion blends the rankings by POSITION rather than by score,
// so the cosine's tight [0,1] spread and BM25's open-ended one can be compared
// without inventing a normalization constant for either.
func reciprocalRankFusion(uuids []string, rankings ...map[string]float64) map[string]float64 {
	fused := make(map[string]float64, len(uuids))
	for _, scores := range rankings {
		order := append([]string(nil), uuids...)
		sort.SliceStable(order, func(i, j int) bool {
			if scores[order[i]] != scores[order[j]] {
				return scores[order[i]] > scores[order[j]]
			}
			return order[i] < order[j]
		})
		for rank, u := range order {
			fused[u] += 1 / (semanticRRFK + float64(rank+1))
		}
	}
	return fused
}

// --- vector plumbing ---------------------------------------------------------

type indexEntry struct {
	dim    int
	weight float64
}

// contextFingerprint is a node's own signature plus a decayed share of each
// ancestor's, which is how the tree enters the term vectors. Two terms filed
// under one parent come out sharing that parent's cells even when no single node
// contains both.
//
// The forest root is deliberately skipped: every node descends from it, so its
// cells would be a constant added to every vector — discriminating nothing while
// diluting everything.
func contextFingerprint(uuid string, kin *kinship) []indexEntry {
	out := randomIndexVector(uuid)
	kin.walkAncestors(uuid, func(p string, weight float64) {
		for _, e := range randomIndexVector(p) {
			out = append(out, indexEntry{dim: e.dim, weight: e.weight * weight})
		}
	})
	return out
}

// kinship is the outline's shape, plus the one number that keeps structure from
// swamping text: how many children each node has.
type kinship struct {
	parent map[string]string
	kids   map[string]int
}

func newKinship(parent map[string]string) *kinship {
	k := &kinship{parent: parent, kids: map[string]int{}}
	for _, p := range parent {
		if p != "" {
			k.kids[p]++
		}
	}
	return k
}

// broadcast is how much of an ancestor's identity each of its children inherits.
//
// A parent with three children is making a claim about all three; a bucket with
// fifty is barely making one about any of them. Dividing by sqrt(children) is
// what stops a large subtree from turning into one undifferentiated blob where
// every node is "related" to every other — which is exactly what happens at a
// flat weight, and it drowns the specific hit the user was looking for.
func (k *kinship) broadcast(uuid string) float64 {
	n := k.kids[uuid]
	if n <= 1 {
		return 1
	}
	return 1 / math.Sqrt(float64(n))
}

// walkAncestors visits up to semanticContextHops ancestors, handing each one the
// weight its topic carries down to uuid: decayed by distance and damped by how
// widely that ancestor broadcasts. The forest root and cyclic ancestry stop it.
func (k *kinship) walkAncestors(uuid string, visit func(ancestor string, weight float64)) {
	seen := map[string]bool{uuid: true}
	weight, cur := 1.0, uuid
	for hop := 0; hop < semanticContextHops; hop++ {
		p, ok := k.parent[cur]
		if !ok || p == "" || p == database.RootUUID || seen[p] {
			return
		}
		seen[p] = true
		weight *= semanticParentDecay
		visit(p, weight*k.broadcast(p))
		cur = p
	}
}

// randomIndexVector is a node's sparse ternary signature: semanticSeeds cells of
// ±1 in an otherwise empty vector. It is derived from the uuid rather than drawn
// at random, so two runs over the same outline rank identically — a query whose
// results reshuffled on every alt+r would be unusable.
func randomIndexVector(uuid string) []indexEntry {
	h := fnv.New64a()
	h.Write([]byte(uuid))
	state := h.Sum64() | 1
	next := func() uint64 { // xorshift64, seeded from the uuid hash
		state ^= state << 13
		state ^= state >> 7
		state ^= state << 17
		return state
	}
	out := make([]indexEntry, 0, semanticSeeds)
	used := map[int]bool{}
	for len(out) < semanticSeeds {
		r := next()
		dim := int(r % semanticDims)
		if used[dim] {
			continue
		}
		used[dim] = true
		sign := 1.0
		if r&(1<<40) != 0 {
			sign = -1
		}
		out = append(out, indexEntry{dim: dim, weight: sign})
	}
	return out
}

func unit(v []float64) []float64 {
	var n float64
	for _, x := range v {
		n += x * x
	}
	if n == 0 {
		return v
	}
	n = math.Sqrt(n)
	for i := range v {
		v[i] /= n
	}
	return v
}

// cosine assumes both vectors are already unit length (unit() runs on every
// vector the model stores), so the dot product IS the cosine.
func cosine(a, b []float64) float64 {
	if len(a) != len(b) {
		return 0
	}
	var dot float64
	for i := range a {
		dot += a[i] * b[i]
	}
	if dot < 0 {
		return 0 // an anti-correlated node is unrelated, not negatively related
	}
	return dot
}

// --- tokenization ------------------------------------------------------------

// semanticTerms lowercases, splits on non-alphanumerics, drops stopwords and
// folds a few English suffixes so "failing"/"failed"/"fails" share one term.
func semanticTerms(s string) []string {
	var out []string
	for _, w := range strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	}) {
		if len(w) < 2 || semanticStop[w] {
			continue
		}
		out = append(out, semanticStem(w))
	}
	return out
}

// semanticStem is a deliberately shallow suffix stripper. Porter-grade stemming
// would need a rules table this file has no business carrying, and the vector
// space already tolerates near-misses — folding the common inflections is enough
// to keep "deploys" and "deploying" from splitting into three unrelated words.
func semanticStem(w string) string {
	for _, suf := range []string{"ingly", "edly", "ing", "ies", "ied", "es", "ed", "ly", "s"} {
		if !strings.HasSuffix(w, suf) {
			continue
		}
		stem := strings.TrimSuffix(w, suf)
		// "ies" restores a "y" rather than deleting letters, so it needs far less
		// left over to stay a real word: "tries" → "try". Everything else keeps
		// four characters, below which stripping produces noise ("was" → "wa").
		minStem := 4
		if suf == "ies" || suf == "ied" {
			minStem = 2
		}
		if len(stem) < minStem {
			continue
		}
		if suf == "ies" || suf == "ied" {
			return stem + "y"
		}
		if suf == "ing" || suf == "ed" {
			// Porter's undoubling step: "shopping" strips to "shopp", which shares
			// nothing with "shop". Drop the repeat so the two agree.
			if n := len(stem); n >= 2 && stem[n-1] == stem[n-2] && !strings.ContainsRune("lsz", rune(stem[n-1])) {
				stem = stem[:n-1]
			}
		}
		return stem
	}
	return w
}
