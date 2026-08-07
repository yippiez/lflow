package editor

import (
	"strings"
	"time"
	"unicode"

	"github.com/lflow/lflow/tui/database"
)

// Query language (live-query node name). Every filter is a `key(value)` call —
// no `:sandwich:` — which reads as one token, nests visibly, and lets a value
// contain spaces:
//
//	words              deploy notes            substring on name/note (adjacent words glue)
//	"phrase"           "why the build broke"   SEMANTIC match — meaning, not letters
//	#tag               #urgent                 exact tag
//	type(todo)                                 node type
//	after(2026-06-01) · since(…)               dated/created on or after
//	before(2026-06-20 14:30) · until(…)        dated/created on or before
//	is(starred) · is(done) · is(open)          node state
//	has(note) · has(children)                  node shape
//	in(<node link>)                            limit results to a subtree (default: root)
//	as(tree) · as(list)                        display (default list)
//	-x                                         negate the atom that follows
//	boolean:           and(query1, query2) · or(query1, query2)
//	ops (legacy):       ||   &&   >   ( … )
//
// A "(" only opens a value when it is glued to a known qualifier key, so a
// standalone "(project || release)" still groups the boolean expression.
//
// "A > B" keeps nodes matching B that sit under a node matching A (strict
// descendants). A B-node whose own name also matches A still counts when
// another A-node sits above it ("work" > "work task" finds the task under
// work). Time bounds that share an AND combine into one window so a
// node needs any one of its dates inside [after, before].
//
// WARNING (invariant): the colon spellings (`type:todo`, `:type:todo`,
// `:breadcrumb:`) still tokenize. Query text is persisted node text, so queries
// written before the bracket syntax must keep matching what they always
// matched — the old forms are aliases, never errors.

// parsedQuery is a compiled query: the match expression plus display flags.
type parsedQuery struct {
	expr        qExpr
	breadcrumb  bool
	hasAnything bool // true when the input had any match atom/op (not only flags)
}

func (pq parsedQuery) empty() bool { return pq.expr == nil || !pq.hasAnything }

// hasSemantic reports whether the expression contains a "quoted" atom, whose
// evaluation needs the vector space built over the candidate set. The finder
// uses it to decide whether the (expensive) semantic model must be materialized
// for this keystroke's evaluation.
func (pq parsedQuery) hasSemantic() bool {
	var walk func(e qExpr) bool
	walk = func(e qExpr) bool {
		switch v := e.(type) {
		case *qSemantic:
			return true
		case *qOr:
			for _, k := range v.kids {
				if walk(k) {
					return true
				}
			}
		case *qAnd:
			for _, k := range v.kids {
				if walk(k) {
					return true
				}
			}
		case *qPipe:
			for _, s := range v.stages {
				if walk(s) {
					return true
				}
			}
		case *qNot:
			return walk(v.kid)
		}
		return false
	}
	return walk(pq.expr)
}

// --- expression AST ----------------------------------------------------------

type qExpr interface {
	// eval returns the set of candidate uuids matching this sub-expression.
	eval(ctx *qCtx) map[string]bool
}

type qOr struct{ kids []qExpr }
type qAnd struct{ kids []qExpr }
type qPipe struct{ stages []qExpr } // A > B > C
type qNot struct{ kid qExpr }       // -x
type qText struct {
	s     string
	isTag bool
}

// qSemantic is a "quoted" atom: it matches by MEANING rather than by letters,
// so a hit need not contain any of the phrase's words (see semantic.go).
type qSemantic struct{ phrase string }

type qType struct{ key string }
type qTime struct {
	after, before *time.Time
}

// qState is an is:/has: predicate — a boolean fact about the node itself rather
// than about its text.
type qState struct{ key string }

func (e *qOr) eval(ctx *qCtx) map[string]bool {
	out := map[string]bool{}
	for _, k := range e.kids {
		for u := range k.eval(ctx) {
			out[u] = true
		}
	}
	return out
}

func (e *qAnd) eval(ctx *qCtx) map[string]bool {
	if len(e.kids) == 0 {
		return map[string]bool{}
	}
	// merge time atoms so after+before form one window (any date in range)
	var times []qTime
	var rest []qExpr
	for _, k := range e.kids {
		if t, ok := k.(*qTime); ok {
			times = append(times, *t)
			continue
		}
		rest = append(rest, k)
	}
	var out map[string]bool
	first := true
	intersect := func(s map[string]bool) {
		if first {
			out = s
			first = false
			return
		}
		for u := range out {
			if !s[u] {
				delete(out, u)
			}
		}
	}
	if len(times) > 0 {
		merged := mergeTimes(times)
		intersect((&merged).eval(ctx))
	}
	for _, k := range rest {
		intersect(k.eval(ctx))
	}
	if first {
		return map[string]bool{}
	}
	return out
}

func mergeTimes(ts []qTime) qTime {
	var m qTime
	for _, t := range ts {
		if t.after != nil && (m.after == nil || t.after.After(*m.after)) {
			lo := *t.after
			m.after = &lo
		}
		if t.before != nil && (m.before == nil || t.before.Before(*m.before)) {
			hi := *t.before
			m.before = &hi
		}
	}
	return m
}

func (e *qPipe) eval(ctx *qCtx) map[string]bool {
	if len(e.stages) == 0 {
		return map[string]bool{}
	}
	cur := e.stages[0].eval(ctx)
	for _, st := range e.stages[1:] {
		under := st.eval(ctx)
		next := map[string]bool{}
		for u := range under {
			if ctx.underAny(u, cur) {
				next[u] = true
			}
		}
		cur = next
	}
	return cur
}

func (e *qText) eval(ctx *qCtx) map[string]bool {
	out := map[string]bool{}
	if e.isTag {
		for _, c := range ctx.cands {
			if nodeHasTag(c.searchName, e.s) || nodeHasTag(c.searchNote, e.s) {
				out[c.uuid] = true
			}
		}
		return out
	}
	lc := strings.ToLower(e.s)
	for _, c := range ctx.cands {
		if strings.Contains(strings.ToLower(c.searchName), lc) || strings.Contains(strings.ToLower(c.searchNote), lc) {
			out[c.uuid] = true
		}
	}
	return out
}

// eval on qNot complements the candidate universe. The universe is the SCOPED
// candidate set, so "-#done" inside an in:<node> query still means "everything
// in that subtree without the tag" rather than everything in the outline.
func (e *qNot) eval(ctx *qCtx) map[string]bool {
	if e.kid == nil {
		return map[string]bool{}
	}
	hit := e.kid.eval(ctx)
	out := map[string]bool{}
	for _, c := range ctx.cands {
		if !hit[c.uuid] {
			out[c.uuid] = true
		}
	}
	return out
}

// eval on qSemantic defers while a streaming scan is still delivering
// candidates. Unlike every other atom, a semantic match is a property of the
// WHOLE corpus — idf, co-occurrence and the vector space all shift as nodes
// arrive — so a partial answer is not an early version of the final one, it is
// a different and wrong one. Worse, it is expensive: evaluating per batch
// rebuilt the space once per batch, which is what made a 50k-node query take
// twenty seconds instead of one.
func (e *qSemantic) eval(ctx *qCtx) map[string]bool {
	if ctx.partial {
		return map[string]bool{}
	}
	return semanticHits(ctx, e.phrase)
}

func (e *qState) eval(ctx *qCtx) map[string]bool {
	out := map[string]bool{}
	for _, c := range ctx.cands {
		if ctx.stateHolds(e.key, c) {
			out[c.uuid] = true
		}
	}
	return out
}

// stateHolds answers one is:/has: predicate for a candidate. Unknown keys match
// nothing rather than everything: a typo must not silently widen the result set.
func (ctx *qCtx) stateHolds(key string, c qCand) bool {
	switch key {
	case "starred":
		return c.starred
	case "unstarred":
		return !c.starred
	case "done", "complete", "completed":
		return c.completedAt > 0
	case "open", "todo", "incomplete":
		return c.completedAt == 0
	case "note":
		return strings.TrimSpace(c.searchNote) != ""
	case "tag", "tagged":
		return len(tagsIn(c.searchName)) > 0 || len(tagsIn(c.searchNote)) > 0
	case "children", "child":
		// Derived from the parent map rather than a stored count, so it stays
		// true for the candidates actually loaded — the same universe every other
		// atom sees.
		for _, k := range ctx.cands {
			if ctx.parent[k.uuid] == c.uuid {
				return true
			}
		}
		return false
	}
	return false
}

func (e *qType) eval(ctx *qCtx) map[string]bool {
	out := map[string]bool{}
	key := strings.ToLower(e.key)
	for _, c := range ctx.cands {
		if strings.ToLower(c.typ) == key {
			out[c.uuid] = true
		}
	}
	return out
}

func (e *qTime) eval(ctx *qCtx) map[string]bool {
	out := map[string]bool{}
	for _, c := range ctx.cands {
		if matchDateWindow(nodeDates(c.name, c.addedOn, ctx.now, ctx.chips), e.after, e.before) {
			out[c.uuid] = true
		}
	}
	return out
}

// matchDateWindow reports whether any of dates lands inside [after, before]
// (either bound may be open).
func matchDateWindow(dates []time.Time, after, before *time.Time) bool {
	for _, d := range dates {
		if after != nil && d.Before(*after) {
			continue
		}
		if before != nil && d.After(*before) {
			continue
		}
		return true
	}
	return false
}

// --- candidate context -------------------------------------------------------

// qCand is one searchable node (in-memory or from the DB).
type qCand struct {
	uuid, name, note, typ, parent string
	// searchName/searchNote are anchor-expanded. Keep name/note raw so a
	// materialized mirror still renders the source's real chips.
	searchName, searchNote string
	addedOn                int64
	completedAt            int64
	starred                bool
	// style/editedOn carry a candidate's picker appearance. The finder's
	// hierarchical search reads them back so a styled or recently edited node
	// renders the same in the picker as it does under a plain search; the query
	// view leaves them zero (its mirrors render via the source node).
	style    string
	editedOn int64
}

// qCtx holds the candidate universe for one query run.
type qCtx struct {
	m      *Model
	now    time.Time
	cands  []qCand
	byUUID map[string]*qCand
	parent map[string]string // uuid → parent uuid
	seen   map[string]bool   // merged candidate UUIDs, including empty structural nodes
	// semantic vector space for "quoted" atoms, memoized across the atoms of one
	// run and rebuilt only when a streamed batch grows the candidate set.
	sem  *semanticModel
	semN int
	// partial marks an evaluation over a candidate set the streaming scan has not
	// finished delivering. See qSemantic.eval.
	partial bool
	// chips is a SNAPSHOT of the editor's chip table, taken when the run starts.
	// Evaluation happens on a worker goroutine while the user keeps typing, and
	// typing creates chips — reading the live map would be a concurrent map
	// read/write, which is a hard crash in Go rather than a stale result.
	chips map[string]database.Chip
	// score is each hit's semantic relevance, written by the quoted atoms as they
	// evaluate and read back to order the results. Empty for a purely lexical
	// query, which has no notion of "better" — only "matched".
	score map[string]float64
}

// recordScore keeps the STRONGEST claim any quoted atom made about a node. Two
// phrases in one query are both describing the wanted thing, so a node that one
// of them is sure about should not be dragged down by the other's indifference.
func (ctx *qCtx) recordScore(uuid string, s float64) {
	if ctx.score == nil {
		ctx.score = map[string]float64{}
	}
	if s > ctx.score[uuid] {
		ctx.score[uuid] = s
	}
}

// recordParent records a parent edge without making uuid a candidate. Mirrors
// are projections, never search targets, but a `>` chain may have to pass
// through one to reach the real nodes below it — the edge is all the walk
// needs. First source wins, like add's ancestry guard.
func (ctx *qCtx) recordParent(uuid, parent string) {
	if uuid == "" || parent == "" {
		return
	}
	if ctx.parent[uuid] == "" {
		ctx.parent[uuid] = parent
	}
}

// underAny reports whether uuid has a proper ancestor in roots — the "under"
// test that gives `A > B` its meaning. A node that ALSO matches the ancestor
// stage (an A-name inside a B-node's own title, "work" > "work task") is still
// under the set when another A-node sits above it; only its own membership is
// never counted as ancestry, so a lone match is not "under itself".
// Bad legacy mirror/parent data must not turn a nested query into a long
// loop. The seen guard makes this O(depth), even when a cycle appears.
func (ctx *qCtx) underAny(uuid string, roots map[string]bool) bool {
	seen := map[string]bool{}
	for hops, cur := 0, uuid; hops < 64; hops++ {
		if seen[cur] {
			return false
		}
		seen[cur] = true
		p, ok := ctx.parent[cur]
		if !ok || p == "" {
			return false
		}
		if roots[p] {
			return true
		}
		cur = p
	}
	return false
}

// atOrUnderAny is the inclusive subtree test used by :in:. Unlike `>` the
// selected node itself belongs to its query scope.
func (ctx *qCtx) atOrUnderAny(uuid string, roots map[string]bool) bool {
	return roots[uuid] || ctx.underAny(uuid, roots)
}

// --- tokenizer / parser ------------------------------------------------------

type qTokKind int

const (
	tokWord qTokKind = iota
	tokAnd
	tokOr
	tokPipe
	tokNot
	tokLParen
	tokRParen
	tokComma
	tokAndCall
	tokOrCall
	tokType
	tokAfter
	tokBefore
	tokTag
	tokState    // is:/has: predicate
	tokSemantic // "quoted phrase"
)

type qTok struct {
	kind qTokKind
	text string // word / type key / tag / date operand / phrase
}

// qField is one lexical field of a query. key is set when the field was written
// as a `key(value)` qualifier; quoted marks a "double quoted" phrase. Both are
// flags rather than literal characters, because both are gone by the time the
// parser sees them.
type qField struct {
	text   string
	key    string
	quoted bool
}

// splitQueryFields scans raw into fields. A `key(value)` qualifier and a quoted
// run each stay whole — spaces and all — grouping parens and a leading "-"
// become their own fields, and everything else splits on whitespace.
func splitQueryFields(raw string) []qField {
	var out []qField
	runes := []rune(raw)
	for i := 0; i < len(runes); {
		switch {
		case unicode.IsSpace(runes[i]):
			i++
		case runes[i] == '(' || runes[i] == ')' || runes[i] == ',':
			out = append(out, qField{text: string(runes[i])})
			i++
		case runes[i] == '-' && i+1 < len(runes) && !unicode.IsSpace(runes[i+1]) &&
			(i == 0 || unicode.IsSpace(runes[i-1]) || runes[i-1] == '('):
			// only a word-leading "-" negates; an interior one is just a hyphen,
			// which keeps "2026-06-01" and "re-run" intact.
			out = append(out, qField{text: "-"})
			i++
		case runes[i] == '"':
			j := i + 1
			for j < len(runes) && runes[j] != '"' {
				j++
			}
			// An unterminated quote still searches: the phrase runs to end of text,
			// so the atom is live while the user is still typing the closing quote.
			if phrase := strings.TrimSpace(string(runes[i+1 : j])); phrase != "" {
				out = append(out, qField{text: phrase, quoted: true})
			}
			if j < len(runes) {
				j++ // consume the closing quote
			}
			i = j
		default:
			j := i
			for j < len(runes) && !unicode.IsSpace(runes[j]) &&
				runes[j] != '(' && runes[j] != ')' && runes[j] != ',' && runes[j] != '"' {
				j++
			}
			word := string(runes[i:j])
			// "type(todo)": a "(" glued to a known qualifier key opens its VALUE,
			// not a grouping group. The value may then contain spaces — which is
			// the point of the bracket form, since "after(jun 20)" says something
			// "after:jun 20" could not say at all.
			if j < len(runes) && runes[j] == '(' {
				if isQualifierKey(word) {
					value, end := scanBracketValue(runes, j)
					out = append(out, qField{text: value, key: strings.ToLower(word)})
					i = end
					continue
				}
				// Boolean functions keep their contents as ordinary query syntax;
				// only the glued function head is special.
				switch strings.ToLower(word) {
				case "and", "or":
					out = append(out, qField{text: strings.ToLower(word) + "("})
					i = j + 1
					continue
				}
			}
			out = append(out, qField{text: word})
			i = j
		}
	}
	return out
}

// scanBracketValue reads the value of a `key(value)` qualifier, given the index
// of its opening paren. Nesting is balanced so an in: chip label containing a
// paren survives; an unterminated value runs to the end of the text, which keeps
// a half-typed "type(tod" from breaking the rest of the parse.
func scanBracketValue(runes []rune, open int) (value string, end int) {
	depth := 0
	for k := open; k < len(runes); k++ {
		switch runes[k] {
		case '(':
			depth++
		case ')':
			if depth--; depth == 0 {
				return string(runes[open+1 : k]), k + 1
			}
		}
	}
	return string(runes[open+1:]), len(runes)
}

// isQualifierKey reports whether a word names a qualifier, and so whether a "("
// glued to it opens a value rather than a grouping group.
func isQualifierKey(word string) bool {
	w := strings.ToLower(word)
	if _, ok := queryFilterKeys[w]; ok {
		return true
	}
	return w == "in" || w == "as"
}

// queryFilterKeys maps a qualifier key to the token it produces. All three
// spellings — `type(todo)`, `type:todo` and the legacy `:type:todo` — resolve
// through it.
var queryFilterKeys = map[string]qTokKind{
	"type":   tokType,
	"after":  tokAfter,
	"since":  tokAfter,
	"before": tokBefore,
	"until":  tokBefore,
	"is":     tokState,
	"has":    tokState,
}

// queryDisplayFlags maps the legacy bare display flags to their breadcrumb
// setting. The current spelling is the as() qualifier; these are kept alive by
// the compatibility invariant.
var queryDisplayFlags = map[string]bool{
	":breadcrumb:": true, ":breadcrumb": true,
	":list:": false, ":list": false,
}

// displaySetting resolves an as() qualifier, which chooses how hits are laid out
// and never affects which nodes match.
func displaySetting(key, value string) (breadcrumb, ok bool) {
	if strings.ToLower(key) != "as" {
		return false, false
	}
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "tree", "breadcrumb":
		return true, true
	case "list", "flat":
		return false, true
	}
	return false, false
}

// splitQualifier peels "key:value" off a field, tolerating the legacy
// ":key:value" wrapper. ok is false when the field carries no known key, in
// which case it is ordinary search text — "ratio:1.5" stays a word.
func splitQualifier(field string) (key, value string, ok bool) {
	f := strings.TrimPrefix(field, ":") // legacy ":type:todo" → "type:todo"
	i := strings.Index(f, ":")
	if i <= 0 {
		return "", "", false
	}
	key = strings.ToLower(f[:i])
	if !isQualifierKey(key) {
		return "", "", false
	}
	return key, f[i+1:], true
}

// tokenizeQuery splits raw into operator/filter/word tokens. Display-only
// fields are stripped and reported through breadcrumb. Legacy :tree: is ignored
// so old text does not break the parse.
func tokenizeQuery(raw string, now time.Time) (toks []qTok, breadcrumb bool, ok bool) {
	ok = true
	for _, f := range splitQueryFields(raw) {
		if f.quoted {
			toks = append(toks, qTok{kind: tokSemantic, text: f.text})
			continue
		}
		if f.key != "" { // a key(value) qualifier
			if v, isDisplay := displaySetting(f.key, f.text); isDisplay {
				breadcrumb = v
				continue
			}
			toks = append(toks, filterToken(f.key, f.text, now)...)
			continue
		}
		lf := strings.ToLower(f.text)
		if v, isFlag := queryDisplayFlags[lf]; isFlag {
			breadcrumb = v
			continue
		}
		switch lf {
		case "&&", "and":
			toks = append(toks, qTok{kind: tokAnd})
			continue
		case "||", "or":
			toks = append(toks, qTok{kind: tokOr})
			continue
		case ">":
			toks = append(toks, qTok{kind: tokPipe})
			continue
		case "-", "not":
			toks = append(toks, qTok{kind: tokNot})
			continue
		case "(":
			toks = append(toks, qTok{kind: tokLParen})
			continue
		case ")":
			toks = append(toks, qTok{kind: tokRParen})
			continue
		case ",":
			toks = append(toks, qTok{kind: tokComma})
			continue
		case "and(":
			toks = append(toks, qTok{kind: tokAndCall})
			continue
		case "or(":
			toks = append(toks, qTok{kind: tokOrCall})
			continue
		case ":tree:", ":tree":
			continue // removed display flag — ignore rather than search for it
		}
		if key, value, isFilter := splitQualifier(f.text); isFilter {
			if v, isDisplay := displaySetting(key, value); isDisplay {
				breadcrumb = v
				continue
			}
			toks = append(toks, filterToken(key, value, now)...)
			continue
		}
		// a bare "#tag" is a tag atom; otherwise a word (case is kept for display,
		// matching lowers both sides).
		if tag, is := tagQuery(f.text); is {
			toks = append(toks, qTok{kind: tokTag, text: tag})
			continue
		}
		toks = append(toks, qTok{kind: tokWord, text: f.text})
	}
	return toks, breadcrumb, ok
}

// filterToken compiles one resolved qualifier. A qualifier with an unusable
// value (bare "type:", an unparseable date) yields no token at all, so a
// half-typed filter simply does not constrain the query yet.
func filterToken(key, value string, now time.Time) []qTok {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	switch queryFilterKeys[key] {
	case tokType:
		return []qTok{{kind: tokType, text: strings.ToLower(value)}}
	case tokState:
		return []qTok{{kind: tokState, text: strings.ToLower(value)}}
	case tokAfter:
		if t, _, pok := parseQueryDate(strings.ToLower(value), now); pok {
			return []qTok{{kind: tokAfter, text: t.Format(time.RFC3339Nano)}}
		}
	case tokBefore:
		if t, hasTime, pok := parseQueryDate(strings.ToLower(value), now); pok {
			hi := t
			if !hasTime {
				hi = time.Date(t.Year(), t.Month(), t.Day(), 23, 59, 59, 0, t.Location())
			}
			return []qTok{{kind: tokBefore, text: hi.Format(time.RFC3339Nano)}}
		}
	}
	// "in:" is consumed by queryTextAndScope before parsing; drop any residue so
	// a scope selector never leaks into text matching.
	return nil
}

// parseQuery compiles raw into an expression + display flags.
func parseQuery(raw string, now time.Time) parsedQuery {
	toks, breadcrumb, _ := tokenizeQuery(raw, now)
	if len(toks) == 0 {
		return parsedQuery{breadcrumb: breadcrumb}
	}
	p := &qParser{toks: toks, now: now}
	expr := p.parseOr()
	return parsedQuery{expr: expr, breadcrumb: breadcrumb, hasAnything: expr != nil}
}

// qParser is a tiny recursive-descent parser over qTok.
//
//	or   = and ( '||' and )*
//	and  = pipe ( '&&' pipe )*
//	pipe = implicit ( '>' implicit )*
//	implicit = primary+          // adjacent primaries AND together; words glue
//	primary  = '-'* ( '(' or ')' | and-call | or-call | atom )
type qParser struct {
	toks []qTok
	pos  int
	now  time.Time
}

func (p *qParser) peek() (qTok, bool) {
	if p.pos >= len(p.toks) {
		return qTok{}, false
	}
	return p.toks[p.pos], true
}

func (p *qParser) take() (qTok, bool) {
	t, ok := p.peek()
	if ok {
		p.pos++
	}
	return t, ok
}

func (p *qParser) parseOr() qExpr {
	left := p.parseAnd()
	var kids []qExpr
	if left != nil {
		kids = append(kids, left)
	}
	for {
		t, ok := p.peek()
		if !ok || t.kind != tokOr {
			break
		}
		p.take()
		right := p.parseAnd()
		if right != nil {
			kids = append(kids, right)
		}
	}
	if len(kids) == 0 {
		return nil
	}
	if len(kids) == 1 {
		return kids[0]
	}
	return &qOr{kids: kids}
}

func (p *qParser) parseAnd() qExpr {
	left := p.parsePipe()
	var kids []qExpr
	if left != nil {
		kids = append(kids, left)
	}
	for {
		t, ok := p.peek()
		if !ok || t.kind != tokAnd {
			break
		}
		p.take()
		right := p.parsePipe()
		if right != nil {
			kids = append(kids, right)
		}
	}
	if len(kids) == 0 {
		return nil
	}
	if len(kids) == 1 {
		return kids[0]
	}
	return &qAnd{kids: kids}
}

func (p *qParser) parsePipe() qExpr {
	left := p.parseImplicitAnd()
	var stages []qExpr
	if left != nil {
		stages = append(stages, left)
	}
	for {
		t, ok := p.peek()
		if !ok || t.kind != tokPipe {
			break
		}
		p.take()
		right := p.parseImplicitAnd()
		if right != nil {
			stages = append(stages, right)
		}
	}
	if len(stages) == 0 {
		return nil
	}
	if len(stages) == 1 {
		return stages[0]
	}
	return &qPipe{stages: stages}
}

// parseImplicitAnd reads one or more primaries. Adjacent bare words merge into
// one phrase ("deploy notes"); other atoms AND with their neighbours.
func (p *qParser) parseImplicitAnd() qExpr {
	var kids []qExpr
	var words []string
	flushWords := func() {
		if len(words) == 0 {
			return
		}
		kids = append(kids, &qText{s: strings.Join(words, " ")})
		words = nil
	}
	for {
		t, ok := p.peek()
		if !ok {
			break
		}
		switch t.kind {
		case tokOr, tokAnd, tokPipe, tokRParen, tokComma:
			flushWords()
			goto done
		case tokWord:
			p.take()
			words = append(words, t.text)
		default:
			flushWords()
			if atom := p.parsePrimary(); atom != nil {
				kids = append(kids, atom)
			}
		}
	}
done:
	flushWords()
	if len(kids) == 0 {
		return nil
	}
	if len(kids) == 1 {
		return kids[0]
	}
	return &qAnd{kids: kids}
}

// parsePrimary reads one non-word atom, including any "-" negations in front of
// it. A trailing "-" with nothing after it is dropped rather than negating the
// whole rest of the query — the user is still typing.
func (p *qParser) parsePrimary() qExpr {
	t, ok := p.take()
	if !ok {
		return nil
	}
	switch t.kind {
	case tokNot:
		inner := p.parsePrimary()
		if inner == nil {
			return nil
		}
		return &qNot{kid: inner}
	case tokLParen:
		inner := p.parseOr()
		if t2, ok2 := p.peek(); ok2 && t2.kind == tokRParen {
			p.take()
		}
		return inner
	case tokAndCall, tokOrCall:
		var args []qExpr
		for {
			if next, exists := p.peek(); !exists || next.kind == tokRParen {
				if exists {
					p.take()
				}
				break
			}
			if arg := p.parseOr(); arg != nil {
				args = append(args, arg)
			}
			next, exists := p.peek()
			if !exists {
				break
			}
			if next.kind == tokComma {
				p.take()
				continue
			}
			if next.kind == tokRParen {
				p.take()
				break
			}
			break
		}
		if t.kind == tokAndCall {
			return &qAnd{kids: args}
		}
		return &qOr{kids: args}
	case tokTag:
		return &qText{s: t.text, isTag: true}
	case tokSemantic:
		return &qSemantic{phrase: t.text}
	case tokType:
		return &qType{key: t.text}
	case tokState:
		return &qState{key: t.text}
	case tokAfter:
		if tm, err := time.Parse(time.RFC3339Nano, t.text); err == nil {
			lo := tm
			return &qTime{after: &lo}
		}
	case tokBefore:
		if tm, err := time.Parse(time.RFC3339Nano, t.text); err == nil {
			hi := tm
			return &qTime{before: &hi}
		}
	}
	return nil
}

// --- legacy helpers kept for tests / shared date scanning --------------------

// parseQueryDate resolves a time operand to its time, whether it carried an
// explicit clock time, and ok. The leftmost recognised date wins.
func parseQueryDate(operand string, now time.Time) (time.Time, bool, bool) {
	ms := detectAllDates(operand, now)
	if len(ms) == 0 {
		return time.Time{}, false, false
	}
	best := ms[0]
	for _, mm := range ms[1:] {
		if mm.start < best.start {
			best = mm
		}
	}
	return best.t, best.hasTime, true
}

// nodeDates is the set of times a node matches against: its creation time plus
// every date chip / inline date in its (anchor-expanded) name. It takes the chip
// table rather than the Model because it runs on the query worker — see
// qCtx.chips.
func nodeDates(name string, addedOn int64, now time.Time, chips map[string]database.Chip) []time.Time {
	var out []time.Time
	if addedOn > 0 {
		out = append(out, time.Unix(0, addedOn))
	}
	plain := database.ExpandAnchors(name, chips)
	for _, dm := range detectAllDates(plain, now) {
		out = append(out, dm.t)
	}
	return out
}
