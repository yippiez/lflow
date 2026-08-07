package editor

import (
	"strings"
	"time"
	"unicode"

	"github.com/lflow/lflow/tui/database"
)

// Hierarchical finder search: a query line containing the query language's `>`
// pipe operator turns /goto and the other node finders into the same evaluator
// the query views use. "A > B" then means the nodes matching B that sit strictly
// under a node matching A (see qPipe.eval), chainable, with every atom — #tag,
// type(...), "phrase", -x, is()/has(), after()/before() — available in any
// stage. Queries without `>` are untouched: they still run through the plain
// lexical SearchNodes.

// finderHierMaxHits caps the hierarchical result list. The picker windows its
// display anyway; the cap only bounds the row slice's memory on a huge outline.
const finderHierMaxHits = 200

// hasPipeOperator reports whether raw contains the query language's `>` token,
// which switches a finder query to hierarchical evaluation. The tokenizer is
// the single source of truth for what counts as an operator: `>` must be
// whitespace-delimited, so a literal "a>b" stays a plain search.
func hasPipeOperator(raw string) bool {
	toks, _, _ := tokenizeQuery(raw, time.Now())
	for _, t := range toks {
		if t.kind == tokPipe {
			return true
		}
	}
	return false
}

// queryScopeFromText parses the in(...)/in:/:in: scope selector out of a
// PLAIN-TEXT query — the finder's query line carries typed text, never node-link
// chips, so it cannot reuse queryTextAndScope's chip table. It deliberately
// mirrors that loop (same queryScopeMarker/closeBracket helpers, same "last
// selector wins" scan) so both surfaces agree on what a selector is.
func queryScopeFromText(raw string) (text, scope string) {
	runes := []rune(raw)
	scope = database.RootUUID
	var out []rune
	for i := 0; i < len(runes); {
		marker, bracket := queryScopeMarker(runes[i:])
		if marker == 0 || (i > 0 && !unicode.IsSpace(runes[i-1]) && runes[i-1] != '(') {
			out = append(out, runes[i])
			i++
			continue
		}
		j := i + marker
		for j < len(runes) && unicode.IsSpace(runes[j]) {
			j++
		}
		end := j
		if bracket {
			for end < len(runes) && runes[end] != ')' {
				end++
			}
		} else {
			for end < len(runes) && !unicode.IsSpace(runes[end]) {
				end++
			}
		}
		value := strings.TrimSpace(string(runes[j:end]))
		switch strings.ToLower(value) {
		case "", "root":
			scope = database.RootUUID
		default:
			scope = value
		}
		i = closeBracket(runes, end, bracket)
	}
	return strings.TrimSpace(string(out)), scope
}

// resolveFinderScope turns a plain-text in() value into a scope uuid: an exact
// name match wins (typed selectors are names, not uuids), otherwise the value
// is used as-is — a raw uuid. An unknown name is a dead scope, resolving to
// nothing rather than silently searching the whole outline; a query node's
// unresolvable selector behaves the same way.
func (m *Model) resolveFinderScope(value string) string {
	if value == "" || value == database.RootUUID || m.db == nil {
		return value
	}
	if n, err := database.NodeByName(m.db, value); err == nil {
		return n.UUID
	}
	return value
}

// finderQueryCtx builds the candidate context for hierarchical finder queries —
// every live DB node outside the Temporary Domain, mirrors excluded as
// candidates but keeping their parent edges, empty structural nodes included
// for the same reason (a `>` chain may pass through them). It is cached for the
// whole finder session: the picker is modal, so the outline cannot change while
// it is open, and one full scan per session beats one per keystroke.
func (m *Model) finderQueryCtx() *qCtx {
	if m.finder.hier != nil {
		return m.finder.hier
	}
	ctx := &qCtx{now: time.Now(), chips: m.chipSnapshot(),
		parent: map[string]string{}, byUUID: map[string]*qCand{}, seen: map[string]bool{}}
	if m.db != nil {
		database.StreamLiveNodesIncl(m.db, 500, func(batch []database.Node) bool {
			for _, n := range batch {
				if n.MirrorOf != "" {
					ctx.recordParent(n.UUID, n.ParentUUID)
					continue
				}
				ctx.add(nil, qCand{uuid: n.UUID, name: n.Name, note: n.Note, typ: n.Type,
					parent: n.ParentUUID, addedOn: n.AddedOn, completedAt: n.CompletedAt,
					starred: n.Starred, style: n.Style, editedOn: n.EditedOn})
			}
			return true
		})
	}
	m.finder.hier = ctx
	return ctx
}

// finderHierSearch runs a query-language expression over the session's
// candidate context, activated only when the typed query carries a `>` pipe.
// The bool reports whether the query was hierarchical: an A > B search that
// happens to match nothing is still hierarchical (the caller must not fall back
// to a lexical search for the literal "A > B" text). Results cap at
// finderHierMaxHits; the cursor node is never a target, but that exclusion
// lives in nodeFinderBackend.search where every other finder result is
// filtered the same way.
func (m *Model) finderHierSearch(raw string) ([]database.Node, bool) {
	now := time.Now()
	if !hasPipeOperator(raw) {
		return nil, false
	}
	text, scopeRaw := queryScopeFromText(raw)
	scope := m.resolveFinderScope(scopeRaw)
	pq := parseQuery(text, now)
	if pq.empty() {
		return nil, true
	}
	sc := m.finderQueryCtx().scoped("", scope)
	if pq.hasSemantic() {
		// Inject the memoized vector space so a "quoted" stage does not rebuild
		// it on every keystroke. The model depends only on the scoped candidate
		// set, which is stable while the finder is open, so one build per in()
		// scope serves the whole session.
		if m.finder.semByScope == nil {
			m.finder.semByScope = map[string]*semanticModel{}
		}
		mod := m.finder.semByScope[scope]
		if mod == nil {
			mod = buildSemanticModel(sc.cands, sc.parent)
			m.finder.semByScope[scope] = mod
		}
		sc.sem = mod
		sc.semN = len(sc.cands)
	}
	matches := evalMatches("", pq, "", sc)
	if len(matches) > finderHierMaxHits {
		matches = matches[:finderHierMaxHits]
	}
	return matches, true
}
