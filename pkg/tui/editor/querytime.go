package editor

import (
	"strings"
	"time"

	"github.com/lflow/lflow/pkg/tui/database"
)

// Query language (live-query node name):
//
//	atoms:  bare words (substring on name/note), #tag (exact tag),
//	        :type:<key>, :after:/:since:<date>, :before:/:until:<date>
//	ops:    ||  or   ·  &&  and (also implicit between adjacent atoms)  ·  >  under
//	parens: ( … ) for grouping
//	flags:  :breadcrumb: nest hits in a locked gray ancestor tree (default :list:)
//	scope:  :in: followed by a picked node link limits results to its subtree
//
// "A > B" keeps nodes matching B that sit under a node matching A (strict
// descendants). Time bounds that share an AND combine into one window so a
// node needs any one of its dates inside [after, before].

// parsedQuery is a compiled query: the match expression plus display flags.
type parsedQuery struct {
	expr        qExpr
	breadcrumb  bool
	hasAnything bool // true when the input had any match atom/op (not only flags)
}

func (pq parsedQuery) empty() bool { return pq.expr == nil || !pq.hasAnything }

// --- expression AST ----------------------------------------------------------

type qExpr interface {
	// eval returns the set of candidate uuids matching this sub-expression.
	eval(ctx *qCtx) map[string]bool
}

type qOr struct{ kids []qExpr }
type qAnd struct{ kids []qExpr }
type qPipe struct{ stages []qExpr } // A > B > C
type qText struct {
	s     string
	isTag bool
}
type qType struct{ key string }
type qTime struct {
	after, before *time.Time
}

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
		if matchDateWindow(ctx.m.nodeDates(c.name, c.addedOn, ctx.now), e.after, e.before) {
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
	starred                bool
}

// qCtx holds the candidate universe for one query run.
type qCtx struct {
	m      *Model
	now    time.Time
	cands  []qCand
	byUUID map[string]*qCand
	parent map[string]string // uuid → parent uuid
}

// underAny reports whether uuid is a strict descendant of any node in roots.
func (ctx *qCtx) underAny(uuid string, roots map[string]bool) bool {
	if roots[uuid] {
		return false // strict: self is not under self
	}
	for hops, cur := 0, uuid; hops < 64; hops++ {
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
	tokLParen
	tokRParen
	tokType
	tokAfter
	tokBefore
	tokTag
)

type qTok struct {
	kind qTokKind
	text string // word / type key / tag / date operand
}

// splitQueryFields splits on whitespace and peels leading/trailing parens into
// their own fields so "(project || release)" tokenizes cleanly.
func splitQueryFields(raw string) []string {
	var out []string
	for _, f := range strings.Fields(raw) {
		// peel leading (
		for strings.HasPrefix(f, "(") {
			out = append(out, "(")
			f = f[1:]
		}
		// peel trailing ) (may be several)
		var trail []string
		for strings.HasSuffix(f, ")") && f != "" {
			trail = append(trail, ")")
			f = f[:len(f)-1]
		}
		if f != "" {
			out = append(out, f)
		}
		out = append(out, trail...)
	}
	return out
}

// tokenizeQuery splits raw into operator/filter/word tokens. Display flags
// (:breadcrumb: / :list:) are stripped and reported separately. Legacy :tree:
// is ignored so old text does not break the parse.
func tokenizeQuery(raw string, now time.Time) (toks []qTok, breadcrumb bool, ok bool) {
	ok = true
	fields := splitQueryFields(raw)
	for i := 0; i < len(fields); i++ {
		f := fields[i]
		lf := strings.ToLower(f)
		switch {
		case f == "&&":
			toks = append(toks, qTok{kind: tokAnd})
		case f == "||":
			toks = append(toks, qTok{kind: tokOr})
		case f == ">":
			toks = append(toks, qTok{kind: tokPipe})
		case f == "(":
			toks = append(toks, qTok{kind: tokLParen})
		case f == ")":
			toks = append(toks, qTok{kind: tokRParen})
		case lf == ":breadcrumb:" || lf == ":breadcrumb":
			breadcrumb = true
		case lf == ":list:" || lf == ":list":
			breadcrumb = false
		case lf == ":tree:" || lf == ":tree":
			// removed — ignore so old text does not break the parse
			continue
		case strings.HasPrefix(lf, ":type:"):
			key := lf[len(":type:"):]
			if key != "" {
				toks = append(toks, qTok{kind: tokType, text: key})
			}
		case hasAnyPrefix(lf, ":after:", ":since:"):
			rest := cutPref(lf, ":after:", ":since:")
			if t, _, pok := parseQueryDate(rest, now); pok {
				toks = append(toks, qTok{kind: tokAfter, text: t.Format(time.RFC3339Nano)})
			}
		case hasAnyPrefix(lf, ":before:", ":until:"):
			rest := cutPref(lf, ":before:", ":until:")
			if t, hasTime, pok := parseQueryDate(rest, now); pok {
				hi := t
				if !hasTime {
					hi = time.Date(t.Year(), t.Month(), t.Day(), 23, 59, 59, 0, t.Location())
				}
				toks = append(toks, qTok{kind: tokBefore, text: hi.Format(time.RFC3339Nano)})
			}
		default:
			// a bare "#tag" is a tag atom; otherwise words (keep original case for
			// display-less matching — we lower at match time)
			if tag, is := tagQuery(f); is {
				toks = append(toks, qTok{kind: tokTag, text: tag})
			} else {
				toks = append(toks, qTok{kind: tokWord, text: f})
			}
		}
	}
	return toks, breadcrumb, ok
}

func hasAnyPrefix(s string, prefixes ...string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}

func cutPref(s string, prefixes ...string) string {
	for _, p := range prefixes {
		if strings.HasPrefix(s, p) {
			return s[len(p):]
		}
	}
	return s
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
//	primary  = '(' or ')' | atom
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
		case tokOr, tokAnd, tokPipe, tokRParen:
			flushWords()
			goto done
		case tokLParen:
			flushWords()
			p.take()
			inner := p.parseOr()
			if t2, ok2 := p.peek(); ok2 && t2.kind == tokRParen {
				p.take()
			}
			if inner != nil {
				kids = append(kids, inner)
			}
		case tokWord:
			p.take()
			words = append(words, t.text)
		case tokTag:
			flushWords()
			p.take()
			kids = append(kids, &qText{s: t.text, isTag: true})
		case tokType:
			flushWords()
			p.take()
			kids = append(kids, &qType{key: t.text})
		case tokAfter:
			flushWords()
			p.take()
			if tm, err := time.Parse(time.RFC3339Nano, t.text); err == nil {
				lo := tm
				kids = append(kids, &qTime{after: &lo})
			}
		case tokBefore:
			flushWords()
			p.take()
			if tm, err := time.Parse(time.RFC3339Nano, t.text); err == nil {
				hi := tm
				kids = append(kids, &qTime{before: &hi})
			}
		default:
			flushWords()
			p.take()
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

// --- legacy helpers kept for tests / shared date scanning --------------------

// cutAnyPrefix returns s with the first matching prefix removed, or false.
func cutAnyPrefix(s string, prefixes ...string) (string, bool) {
	for _, p := range prefixes {
		if strings.HasPrefix(s, p) {
			return s[len(p):], true
		}
	}
	return "", false
}

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
// every date chip / inline date in its (anchor-expanded) name.
func (m *Model) nodeDates(name string, addedOn int64, now time.Time) []time.Time {
	var out []time.Time
	if addedOn > 0 {
		out = append(out, time.Unix(0, addedOn))
	}
	plain := database.ExpandAnchors(name, m.chips)
	for _, dm := range detectAllDates(plain, now) {
		out = append(out, dm.t)
	}
	return out
}

// parseTimeQuery is a thin wrapper used by older tests: residual text + time
// bounds + types + breadcrumb flag, without boolean operators.
func parseTimeQuery(raw string, now time.Time) timeQuery {
	pq := parseQuery(raw, now)
	tq := timeQuery{tree: pq.breadcrumb}
	var words []string
	var walk func(qExpr)
	walk = func(e qExpr) {
		if e == nil {
			return
		}
		switch v := e.(type) {
		case *qAnd:
			for _, k := range v.kids {
				walk(k)
			}
		case *qOr:
			for _, k := range v.kids {
				walk(k)
			}
		case *qPipe:
			for _, k := range v.stages {
				walk(k)
			}
		case *qText:
			if !v.isTag {
				words = append(words, v.s)
			} else {
				words = append(words, "#"+v.s)
			}
		case *qType:
			tq.types = append(tq.types, v.key)
		case *qTime:
			if v.after != nil {
				tq.after = v.after
			}
			if v.before != nil {
				tq.before = v.before
			}
		}
	}
	walk(pq.expr)
	tq.text = strings.Join(words, " ")
	return tq
}

// timeQuery is the legacy flat view of a query (tests + simple matchers). Prefer
// parseQuery for real evaluation.
type timeQuery struct {
	after, before *time.Time
	types         []string
	tree          bool // breadcrumb display
	text          string
}

func (tq timeQuery) hasFilter() bool {
	return tq.hasTimeFilter() || len(tq.types) > 0
}

func (tq timeQuery) hasTimeFilter() bool { return tq.after != nil || tq.before != nil }

func (tq timeQuery) matchType(typ string) bool {
	if len(tq.types) == 0 {
		return !typeOf(typ).searchHidden
	}
	typ = strings.ToLower(typ)
	for _, t := range tq.types {
		if t == typ {
			return true
		}
	}
	return false
}

func (tq timeQuery) matchDates(dates []time.Time) bool {
	return matchDateWindow(dates, tq.after, tq.before)
}
