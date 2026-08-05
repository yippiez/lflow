package editor

import (
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/lflow/lflow/packages/database"
)

// A live-query node: its name is a search; alt+r searches the user's notes —
// both saved nodes (full live set) and unsaved ones currently in memory — and
// reconciles MIRROR children of the matches (first-order only). The mirrors are
// REAL persisted nodes, so they survive a relaunch; re-running reconciles them in
// place (add new matches, drop stale ones) to minimize churn.

const queryMaxHits = 50

func init() {
	registerType(nodeType{
		key:            database.TypeQuery,
		label:          "Query",
		inlineEditable: true,
		disableChips:   true,
		prefix:         queryPrefix,
		spanColor:      querySpanColor,
		run:            runQuery,
	})
}

// queryLoad streams database candidates to the Bubble Tea event loop. The
// worker owns only SQL iteration; all tree reconciliation remains on the UI
// goroutine.
type queryLoad struct {
	generation int
	uuid       string
	raw, scope string
	parsed     parsedQuery
	ctx        *qCtx
	ch         chan queryLoadMsg
	done       chan struct{}
}

type queryLoadMsg struct {
	generation int
	nodes      []database.Node
	done       bool
	err        error
}

// queryReadyMsg carries the finished match list back from the worker. Matching
// runs off the UI goroutine so the toolbar can keep shimmering while it works —
// on a large outline the evaluation is seconds, and doing it inline froze the
// editor with the "running" indicator never getting a frame to paint in.
type queryReadyMsg struct {
	generation int
	matches    []database.Node
}

// chipSnapshot copies the chip table for a query worker. Chips are created as
// the user types, so a worker reading the live map races the editor — in Go a
// concurrent map read/write is a hard crash, not a stale read.
func (m *Model) chipSnapshot() map[string]database.Chip {
	out := make(map[string]database.Chip, len(m.chips))
	for k, v := range m.chips {
		out[k] = v
	}
	return out
}

// queryPrefix is deliberately scope-neutral: query scope is expressed by the
// persisted :in: parameter, whose omitted value is the outline root.
func queryPrefix(*item) string { return cDim + "⌕" + cReset + " " }

// queryTextAndScope removes in: selectors from q's searchable expression and
// returns their selected subtree root. An omitted selector means the permanent
// outline root. The picker stores a selected node as a link chip, preserving its
// UUID even when the node is renamed; in:<uuid> and in:root also work in plain
// text for scripts and hand-written queries. The legacy :in: spelling still
// parses (see the compatibility invariant in querytime.go).
func (m *Model) queryTextAndScope(q *item) (string, string) {
	if q == nil {
		return "", database.RootUUID
	}
	runes := []rune(q.name)
	spans := database.AnchorSpans(runes)
	spanAt := make(map[int]database.AnchorSpan, len(spans))
	for _, sp := range spans {
		spanAt[sp.Start] = sp
	}

	scope := database.RootUUID
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
		if sp, ok := spanAt[j]; ok {
			if c, exists := m.chips[sp.ID]; exists && c.Kind == chipKindLink {
				if uuid, linked := nodeLinkUUID(c.Value); linked && uuid != "" {
					scope = uuid
				}
			}
			i = closeBracket(runes, sp.End, bracket)
			continue
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
	return strings.TrimSpace(database.ExpandAnchors(string(out), m.chips)), scope
}

// queryScopeMarker returns the rune length of the scope selector starting at
// runes and whether its value is bracketed, or 0 when there is no selector.
// "in(" is the current spelling; "in:" and the legacy ":in:" still resolve.
func queryScopeMarker(runes []rune) (n int, bracket bool) {
	head := strings.ToLower(string(runes[:min(len(runes), 4)]))
	switch {
	case strings.HasPrefix(head, "in("):
		return 3, true
	case strings.HasPrefix(head, ":in:"):
		return 4, false
	case strings.HasPrefix(head, "in:"):
		return 3, false
	}
	return 0, false
}

// closeBracket steps past the ")" that ends a bracketed selector, so the closing
// paren never survives into the searchable text as a stray grouping token.
func closeBracket(runes []rune, at int, bracket bool) int {
	if !bracket {
		return at
	}
	for at < len(runes) && unicode.IsSpace(runes[at]) {
		at++
	}
	if at < len(runes) && runes[at] == ')' {
		at++
	}
	return at
}

// querySpanColor tints the semantic-search delimiters. A "quoted" phrase is the
// query language's one operator that changes HOW matching works, so — as with
// the math node's yellow operators — the marks carry the color and the phrase
// inside stays ordinary text. An unterminated opening quote is tinted too: the
// atom is already live while the closing quote is still being typed.
func querySpanColor(_ *item, runes []rune) map[int]string {
	var quotes []int
	for i, r := range runes {
		if r == '"' {
			quotes = append(quotes, i)
		}
	}
	out := make(map[int]string, len(quotes))
	for _, i := range quotes {
		out[i] = cYellow
	}
	return out
}

func runQuery(m *Model, it *item) tea.Cmd {
	if it == nil {
		return nil
	}
	// The in-memory path stays synchronous: it is tiny, deterministic for tests,
	// and has no database scan to hide. A persisted outline streams below.
	if m.db == nil {
		m.finishQueryRun(it, m.queryMatches(it))
		return nil
	}
	return m.startQueryLoad(it)
}

// finishQueryRun records a completed query run and reconciles its generated
// mirrors. Keeping this on the model goroutine avoids concurrent tree mutation.
func (m *Model) finishQueryRun(it *item, matches []database.Node) {
	m.reconcileQueryMirrors(it, matches)
	d := m.nodeStore(it.uuid)
	d["queryRunAt"] = time.Now().Unix()
	// Results do not update until alt+r, so their highlights must describe the
	// last run too. Holding the run text prevents old result rows from repainting
	// under a partially edited query.
	raw, _ := m.queryTextAndScope(it)
	d["queryRunText"] = raw
	m.qCrumbs = nil // ancestor names may have changed — recompute breadcrumbs
	m.unsaved = true
	m.refreshRows()
}

// startQueryLoad starts a fresh, cancelable candidate scan. Batches are kept
// deliberately small: each one can repaint the outline before the next batch
// arrives, rather than holding the terminal hostage while SQLite is read.
func (m *Model) startQueryLoad(it *item) tea.Cmd {
	if m.queryLoad != nil {
		close(m.queryLoad.done)
	}
	m.queryGeneration++
	raw, scope := m.queryTextAndScope(it)
	load := &queryLoad{
		generation: m.queryGeneration,
		uuid:       it.uuid,
		raw:        raw,
		scope:      scope,
		parsed:     parseQuery(raw, time.Now()),
		ctx:        m.buildQueryCtxMemory(it, time.Now()),
		ch:         make(chan queryLoadMsg, 1),
		done:       make(chan struct{}),
	}
	// Batches land one at a time; a "quoted" atom holds until the last one is in
	// (see qSemantic.eval) and finishQueryRun clears this.
	load.ctx.partial = true
	m.queryLoad = load
	if load.parsed.empty() {
		m.finishQueryRun(it, nil)
		m.queryLoad = nil
		return nil
	}

	go func() {
		err := database.StreamLiveNodes(m.db, 24, func(nodes []database.Node) bool {
			cpy := append([]database.Node(nil), nodes...)
			select {
			case load.ch <- queryLoadMsg{generation: load.generation, nodes: cpy}:
				return true
			case <-load.done:
				return false
			}
		})
		select {
		case load.ch <- queryLoadMsg{generation: load.generation, done: true, err: err}:
		case <-load.done:
		}
	}()
	return m.waitQueryLoad(load)
}

// handleQueryLoad folds one streamed database batch into the live candidate
// context and incrementally reconciles the visible result mirrors.
func (m *Model) handleQueryLoad(msg queryLoadMsg) tea.Cmd {
	load := m.queryLoad
	if load == nil || msg.generation != load.generation {
		return nil // a newer alt+r superseded this scan
	}
	q := m.tree.byUUID[load.uuid]
	if q == nil || q.typ != database.TypeQuery {
		close(load.done)
		m.queryLoad = nil
		return nil
	}
	for _, n := range msg.nodes {
		// Mirrors are projections, never independent search targets. In
		// particular this keeps a persisted query's own old result rows from
		// feeding its next nested run.
		if n.MirrorOf != "" {
			continue
		}
		load.ctx.add(q, qCand{uuid: n.UUID, name: n.Name, note: n.Note, typ: n.Type,
			parent: n.ParentUUID, addedOn: n.AddedOn, completedAt: n.CompletedAt, starred: n.Starred})
	}
	if len(msg.nodes) > 0 {
		m.reconcileQueryMirrors(q, m.queryMatchesInCtx(q, load.parsed, load.scope, load.ctx))
		m.refreshRows()
	}
	if msg.done {
		if msg.err != nil {
			m.errorFlash("query: " + msg.err.Error())
		}
		load.ctx.partial = false // the corpus is complete — semantic atoms may run
		return m.evaluateQuery(q, load)
	}
	return m.waitQueryLoad(load)
}

// evaluateQuery runs the finished expression on a worker. The candidate set stops
// growing once the scan is done, and the context holds a chip snapshot, so the
// worker reads only frozen data — everything that touches the tree stays on the
// UI goroutine, in handleQueryReady.
func (m *Model) evaluateQuery(q *item, load *queryLoad) tea.Cmd {
	return func() tea.Msg {
		return queryReadyMsg{generation: load.generation,
			matches: evalMatches(q, load.parsed, load.scope, load.ctx)}
	}
}

// handleQueryReady lands a finished evaluation: reconcile the mirrors and drop
// the running indicator.
func (m *Model) handleQueryReady(msg queryReadyMsg) tea.Cmd {
	load := m.queryLoad
	if load == nil || msg.generation != load.generation {
		return nil // a newer alt+r superseded this run
	}
	q := m.tree.byUUID[load.uuid]
	m.queryLoad = nil
	if q == nil || q.typ != database.TypeQuery {
		return nil
	}
	if load.parsed.breadcrumb {
		m.sortByCrumb(msg.matches)
	}
	m.finishQueryRun(q, msg.matches)
	return m.scheduleSync()
}

// waitQueryLoad waits for the next batch from the current scan without blocking
// Bubble Tea's event loop.
func (m *Model) waitQueryLoad(load *queryLoad) tea.Cmd {
	return func() tea.Msg {
		select {
		case msg := <-load.ch:
			return msg
		case <-load.done:
			return nil
		}
	}
}

// queryUpdatedAt is the unix-seconds of a query node's last run (0 if never).
func (m *Model) queryUpdatedAt(uuid string) int64 {
	v, _ := m.nodeStore(uuid)["queryRunAt"].(int64)
	return v
}

// queryMatches finds nodes matching the query language in the node's name
// (see querytime.go): text / #tag / :type: / :after: / :before: combined with
// && || > and parens. In-memory tree and DB live nodes are merged; in-memory
// wins on conflict so the freshest name is used. Results are sorted by name
// (starred first); :breadcrumb: re-sorts by ancestor path.
func (m *Model) queryMatches(q *item) []database.Node {
	now := time.Now()
	// resolve the query node's own chips/dates to plain text before parsing, so a
	// ":before:2026-06-01" the editor chipified still reads as text here.
	raw, scope := m.queryTextAndScope(q)
	if raw == "" {
		return nil
	}
	pq := parseQuery(raw, now)
	if pq.empty() {
		return nil
	}

	ctx := m.buildQueryCtx(q, now)
	return m.queryMatchesInCtx(q, pq, scope, ctx)
}

// queryMatchesInCtx evaluates a context and applies every display ordering,
// including the breadcrumb sort that walks the tree. It therefore belongs to the
// UI goroutine; a worker calls evalMatches and lets handleQueryReady finish.
func (m *Model) queryMatchesInCtx(q *item, pq parsedQuery, scope string, ctx *qCtx) []database.Node {
	out := evalMatches(q, pq, scope, ctx)
	if pq.breadcrumb {
		m.sortByCrumb(out)
	}
	return out
}

// sortByCrumb groups hits by ancestor path so same-parent matches sit together
// and the render can show one breadcrumb per group. It reads the tree, so it
// never runs off the UI goroutine.
func (m *Model) sortByCrumb(out []database.Node) {
	m.qCrumbs = nil
	sort.SliceStable(out, func(i, j int) bool {
		return m.crumbOf(out[i].UUID) < m.crumbOf(out[j].UUID)
	})
}

// evalMatches is the pure half: it reads only the candidate context, which stops
// changing once the scan is done. That is what lets a large query evaluate on a
// worker while the editor keeps drawing.
func evalMatches(q *item, pq parsedQuery, scope string, ctx *qCtx) []database.Node {
	ctx = ctx.scoped(q, scope)
	if len(ctx.cands) == 0 {
		return nil
	}
	hitSet := pq.expr.eval(ctx)

	var out []database.Node
	for u := range hitSet {
		c := ctx.byUUID[u]
		if c == nil || c.uuid == q.uuid {
			continue
		}
		out = append(out, database.Node{
			UUID: c.uuid, Name: c.name, Note: c.note, Type: c.typ,
			ParentUUID: c.parent, AddedOn: c.addedOn, Starred: c.starred,
		})
	}

	// /star pins first. Then relevance, when the query has any: a "quoted" atom
	// ranks its hits, and name order would throw that away — the whole point of
	// scoring 50,000 candidates is that the best few come out on top, not the ones
	// that happen to start with "a". A purely lexical query has no score and falls
	// back to name order, since "matched" is all it ever knows.
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Starred != out[j].Starred {
			return out[i].Starred
		}
		if si, sj := ctx.score[out[i].UUID], ctx.score[out[j].UUID]; si != sj {
			return si > sj
		}
		if out[i].Name == out[j].Name {
			return out[i].UUID < out[j].UUID
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out
}

// buildQueryCtx gathers every searchable candidate (in-memory + DB) and the
// parent map used by the `>` operator.
func (m *Model) buildQueryCtx(q *item, now time.Time) *qCtx {
	ctx := m.buildQueryCtxMemory(q, now)
	if m.db == nil {
		return ctx
	}
	if err := database.StreamLiveNodes(m.db, 500, func(nodes []database.Node) bool {
		for _, n := range nodes {
			if n.MirrorOf == "" {
				ctx.add(q, qCand{uuid: n.UUID, name: n.Name, note: n.Note, typ: n.Type,
					parent: n.ParentUUID, addedOn: n.AddedOn, completedAt: n.CompletedAt, starred: n.Starred})
			}
		}
		return true
	}); err != nil {
		// In-memory candidates remain useful when the database scan fails.
	}
	return ctx
}

// buildQueryCtxMemory seeds a context from the open tree. The streaming path
// adds persisted candidates later, without ever reading or mutating the tree
// from its worker goroutine.
func (m *Model) buildQueryCtxMemory(q *item, now time.Time) *qCtx {
	ctx := &qCtx{m: m, now: now, chips: m.chipSnapshot(),
		parent: map[string]string{}, byUUID: map[string]*qCand{}, seen: map[string]bool{}}
	if m.tree == nil {
		return ctx
	}
	for uuid, it := range m.tree.byUUID {
		if it == q || it.mirrorOf != "" {
			continue
		}
		parent := ""
		if it.parent != nil {
			parent = it.parent.uuid
		}
		ctx.add(q, qCand{uuid: uuid, name: it.name, note: it.note, typ: it.typ,
			parent: parent, addedOn: it.addedOn, completedAt: it.completedAt, starred: it.starred})
	}
	return ctx
}

// add merges one candidate while preserving parent links for empty structural
// nodes. That parent map is what makes scoped nested (`>`) searches work.
func (ctx *qCtx) add(q *item, c qCand) {
	if c.uuid == "" || (q != nil && c.uuid == q.uuid) {
		return
	}
	// Ancestry is recorded from whichever source knows it, seen or not. The
	// loaded tree is the LOADED tree: open the outline at a subtree — which every
	// /goto and every zoom does — and that subtree's root has no parent item in
	// memory, so it arrived here parentless and, marked seen, hid the parent the
	// database was about to supply. The scope walk then dead-ended at it and
	// every node under it fell out of `in:root`, which is to say out of every
	// unscoped query: a whole branch of the outline silently stopped being
	// searchable from inside itself.
	if c.parent != "" && ctx.parent[c.uuid] == "" {
		ctx.parent[c.uuid] = c.parent
	}
	if ctx.seen[c.uuid] {
		return
	}
	// A source that cannot place the node has not really seen it — the next one
	// may know where it sits — so it is not marked, and the node gets another
	// chance to become a candidate with its ancestry intact.
	if c.parent == "" {
		return
	}
	ctx.seen[c.uuid] = true
	c.searchName = database.ExpandAnchors(c.name, ctx.chips)
	c.searchNote = database.ExpandAnchors(c.note, ctx.chips)
	// An empty structural node has no text to match. The forest root is chrome
	// the same way it is chrome in a breadcrumb path: it is every node's
	// ancestor, so returning it as a hit says nothing. Both still contributed
	// their parent link above, which is what `>` and in: scoping actually need.
	if c.name == "" || c.uuid == database.RootUUID {
		return
	}
	ctx.cands = append(ctx.cands, c)
	cp := c
	ctx.byUUID[c.uuid] = &cp
}

// scoped returns a non-mutating view of ctx narrowed to the requested subtree.
// A streamed scan must retain candidates outside the scope for a later batch:
// they may supply an ancestor link needed to prove a descendant is in scope.
func (ctx *qCtx) scoped(q *item, scope string) *qCtx {
	if q == nil {
		return ctx
	}
	if scope == "" {
		scope = database.RootUUID
	}
	out := &qCtx{m: ctx.m, now: ctx.now, chips: ctx.chips, parent: ctx.parent,
		byUUID: map[string]*qCand{}, seen: ctx.seen, partial: ctx.partial}
	qRoot := map[string]bool{q.uuid: true}
	scopeRoot := map[string]bool{scope: true}
	for _, c := range ctx.cands {
		if ctx.underAny(c.uuid, qRoot) || !ctx.atOrUnderAny(c.uuid, scopeRoot) {
			continue
		}
		out.cands = append(out.cands, c)
		out.byUUID[c.uuid] = ctx.byUUID[c.uuid]
	}
	return out
}

// crumbOf is a node's muted ancestor breadcrumb ("inbox › work › "), memoized
// in m.qCrumbs. In-memory ancestry wins; a hit outside the loaded subtree walks
// parent uuids through the DB (bounded, so a cycle cannot hang the render).
func (m *Model) crumbOf(uuid string) string {
	if c, ok := m.qCrumbs[uuid]; ok {
		return c
	}
	var parts []string
	if it, ok := m.tree.byUUID[uuid]; ok {
		for p := it.parent; p != nil && p.parent != nil; p = p.parent {
			if n := displayAnchors(m.tree.displayName(p), m.chips); n != "" {
				parts = append([]string{n}, parts...)
			}
		}
	} else if m.db != nil {
		cur, err := database.GetNode(m.db, uuid)
		for hops := 0; err == nil && cur.ParentUUID != "" && hops < 6; hops++ {
			p, perr := database.GetNode(m.db, cur.ParentUUID)
			if perr != nil || p.ParentUUID == "" { // stop before the forest root
				break
			}
			if n := displayAnchors(p.Name, m.chips); n != "" {
				parts = append([]string{n}, parts...)
			}
			cur = p
		}
	}
	if len(parts) > 3 {
		parts = parts[len(parts)-3:] // keep the nearest ancestors when deep
	}
	crumb := ""
	if len(parts) > 0 {
		crumb = strings.Join(parts, " › ") + " › "
	}
	if m.qCrumbs == nil {
		m.qCrumbs = map[string]string{}
	}
	m.qCrumbs[uuid] = crumb
	return crumb
}

// queryWant is one row in the materialized result view. Breadcrumb mode merges
// shared ancestors into this tree; hit distinguishes real results from the gray
// path-only scaffolding around them.
type queryWant struct {
	node     database.Node
	hit      bool
	children []*queryWant
	bySource map[string]*queryWant
}

// reconcileQueryMirrors materializes either a flat result list or a real
// breadcrumb tree. Every generated row is structurally locked. Path-only rows
// additionally carry the content lock and render gray; hits remain visually live
// but cannot be moved/indented out of the generated view.
func (m *Model) reconcileQueryMirrors(q *item, matches []database.Node) {
	raw, _ := m.queryTextAndScope(q)
	pq := parseQuery(raw, time.Now())
	root := &queryWant{bySource: map[string]*queryWant{}}
	seenHits := map[string]bool{}
	for _, mn := range matches {
		if mn.UUID == q.uuid || seenHits[mn.UUID] || len(seenHits) >= queryMaxHits {
			continue
		}
		seenHits[mn.UUID] = true
		path := []database.Node{mn}
		if pq.breadcrumb {
			path = append(m.queryAncestorPath(q, mn), mn)
		}
		at := root
		for i, pn := range path {
			w := at.bySource[pn.UUID]
			if w == nil {
				w = &queryWant{node: pn, bySource: map[string]*queryWant{}}
				at.bySource[pn.UUID] = w
				at.children = append(at.children, w)
			}
			if i == len(path)-1 {
				w.hit = true
			}
			at = w
		}
	}
	m.reconcileQueryLevel(q, root.children)
}

// queryAncestorPath returns root-to-parent ancestors for a hit, excluding the
// forest root and the selected :in: boundary.
func (m *Model) queryAncestorPath(q *item, hit database.Node) []database.Node {
	_, scope := m.queryTextAndScope(q)
	var rev []database.Node
	parent := hit.ParentUUID
	for hops := 0; parent != "" && hops < 64; hops++ {
		var n database.Node
		if src := m.tree.byUUID[parent]; src != nil {
			n = database.Node{UUID: src.uuid, ParentUUID: parentUUID(src), Name: src.name, Note: src.note,
				Type: src.typ, CompletedAt: src.completedAt, AddedOn: src.addedOn, Starred: src.starred}
		} else if m.db != nil {
			var err error
			n, err = database.GetNode(m.db, parent)
			if err != nil {
				break
			}
		} else {
			break
		}
		if n.UUID == q.uuid {
			return nil // a query never materializes its own descendants
		}
		if n.ParentUUID == "" { // forest root is chrome, not a breadcrumb
			break
		}
		if n.UUID == scope {
			break
		}
		rev = append(rev, n)
		parent = n.ParentUUID
	}
	for i, j := 0, len(rev)-1; i < j; i, j = i+1, j-1 {
		rev[i], rev[j] = rev[j], rev[i]
	}
	return rev
}

func parentUUID(it *item) string {
	if it == nil || it.parent == nil {
		return ""
	}
	return it.parent.uuid
}

// reconcileQueryLevel preserves generated item identities where possible and
// leaves user-created non-mirror children untouched after the generated view.
func (m *Model) reconcileQueryLevel(parent *item, wanted []*queryWant) {
	existing := map[string]*item{}
	var others []*item
	for _, c := range parent.children {
		if c.mirrorOf == "" {
			others = append(others, c)
			continue
		}
		src := m.tree.sourceUUID(c)
		if existing[src] != nil {
			m.tombstoneQueryItem(c)
			continue
		}
		existing[src] = c
	}

	var generated []*item
	for _, w := range wanted {
		c := existing[w.node.UUID]
		delete(existing, w.node.UUID)
		if c == nil {
			var err error
			c, err = m.tree.newItem()
			if err != nil {
				break
			}
		}
		c.parent = parent
		c.mirrorOf = w.node.UUID
		c.typ = w.node.Type
		if c.typ == "" {
			c.typ = database.TypeBullets
		}
		c.completedAt = w.node.CompletedAt
		c.structureLocked = true
		c.readonly = !w.hit
		c.collapsed = false
		if !m.tree.graftExternal(w.node.UUID) && w.node.Name != "" {
			m.tree.externalNames[w.node.UUID] = w.node.Name
		}
		m.reconcileQueryLevel(c, w.children)
		generated = append(generated, c)
	}
	for _, stale := range existing {
		m.tombstoneQueryItem(stale)
	}
	parent.children = append(generated, others...)
}

// tombstoneItem records one already-detached generated node for deletion. WF
// reconciliation also uses this primitive after walking its own subtree.
func (m *Model) tombstoneItem(it *item) {
	if !it.isNew {
		m.tree.deleted = append(m.tree.deleted, it.uuid)
	}
	delete(m.tree.byUUID, it.uuid)
}

func (m *Model) tombstoneQueryItem(it *item) {
	for _, c := range it.children {
		m.tombstoneQueryItem(c)
	}
	if !it.isNew {
		m.tree.deleted = append(m.tree.deleted, it.uuid)
	}
	delete(m.tree.byUUID, it.uuid)
}

type queryRange struct{ start, end int }

// queryRunText returns the expression that produced q's current materialized
// children. Existing results loaded from disk predate this ephemeral cache, so
// their current query text becomes the baseline on first render.
func (m *Model) queryRunText(q *item) string {
	d := m.nodeStore(q.uuid)
	if raw, ok := d["queryRunText"].(string); ok {
		return raw
	}
	raw, _ := m.queryTextAndScope(q)
	d["queryRunText"] = raw
	return raw
}

// highlightQueryHit paints the visible name fragments that explain why this
// generated result matched. Filters with no name fragment (note/type/date) paint
// the whole name, so every hit still has an explicit yellow-background reason.
func (m *Model) highlightQueryHit(it *item, name, body string) string {
	if it == nil || !it.queryGenerated() || it.readonly || name == "" {
		return body
	}
	var q *item
	for p := it.parent; p != nil; p = p.parent {
		if p.typ == database.TypeQuery {
			q = p
			break
		}
	}
	if q == nil {
		return body
	}
	pq := parseQuery(m.queryRunText(q), time.Now())
	var needles []string
	var collect func(qExpr)
	collect = func(e qExpr) {
		switch v := e.(type) {
		case *qText:
			if v.isTag {
				needles = append(needles, "#"+v.s)
			} else {
				needles = append(needles, v.s)
			}
		case *qAnd:
			for _, k := range v.kids {
				collect(k)
			}
		case *qOr:
			for _, k := range v.kids {
				collect(k)
			}
		case *qPipe:
			// Only the final stage describes the returned row; earlier stages
			// explain ancestry, not text on this hit.
			if len(v.stages) > 0 {
				collect(v.stages[len(v.stages)-1])
			}
		}
	}
	collect(pq.expr)

	// Search works on expanded chip values, while rows render compact chip
	// displays. Highlight the visible form; when the reason only exists in a
	// note/filter/hidden chip value, the whole visible name is the marker.
	visibleName := stripControlBytes(displayAnchors(name, m.chips))
	runes := []rune(visibleName)
	lower := []rune(strings.ToLower(visibleName))
	var ranges []queryRange
	for _, needle := range needles {
		nr := []rune(strings.ToLower(needle))
		if len(nr) == 0 || len(nr) > len(lower) {
			continue
		}
		for i := 0; i+len(nr) <= len(lower); i++ {
			if string(lower[i:i+len(nr)]) == string(nr) {
				ranges = append(ranges, queryRange{i, i + len(nr)})
				i += len(nr) - 1
			}
		}
	}
	if len(ranges) == 0 {
		ranges = []queryRange{{0, len(runes)}}
	} else {
		sort.Slice(ranges, func(i, j int) bool { return ranges[i].start < ranges[j].start })
		merged := ranges[:0]
		for _, r := range ranges {
			if len(merged) > 0 && r.start <= merged[len(merged)-1].end {
				if r.end > merged[len(merged)-1].end {
					merged[len(merged)-1].end = r.end
				}
				continue
			}
			merged = append(merged, r)
		}
		ranges = merged
	}
	plain := ansi.Strip(body)
	byteAt := strings.Index(plain, visibleName)
	if byteAt < 0 {
		// A type-specific renderer may show a transformed/truncated preview rather
		// than its literal name. It is still a hit, so mark the complete entry.
		return paintVisibleRanges(body, []queryRange{{0, utf8.RuneCountInString(plain)}})
	}
	offset := utf8.RuneCountInString(plain[:byteAt])
	for i := range ranges {
		ranges[i].start += offset
		ranges[i].end += offset
	}
	return paintVisibleRanges(body, ranges)
}

func paintVisibleRanges(s string, ranges []queryRange) string {
	starts, ends := map[int]bool{}, map[int]bool{}
	for _, r := range ranges {
		if r.end > r.start {
			starts[r.start], ends[r.end] = true, true
		}
	}
	restoreBG := "\x1b[49m"
	if bgPage != "" {
		restoreBG = bgPage
	}
	var b strings.Builder
	visible, active := 0, false
	for i := 0; i < len(s); {
		if starts[visible] && !active {
			b.WriteString(bgHit)
			active = true
		}
		if s[i] == '\x1b' {
			j := ansiEscapeEnd(s, i)
			seq := s[i:j]
			b.WriteString(seq)
			if active && seq == cReset {
				b.WriteString(bgHit)
			}
			i = j
			continue
		}
		_, n := utf8.DecodeRuneInString(s[i:])
		b.WriteString(s[i : i+n])
		i += n
		visible++
		if ends[visible] && active {
			b.WriteString(restoreBG)
			active = false
		}
	}
	if active {
		b.WriteString(restoreBG)
	}
	return b.String()
}

// Query rows may contain OSC 8 hyperlinks as well as SGR colors; neither
// occupies a visible rune in paintVisibleRanges' coordinate space. The scanner
// that skips both is the shared ansiEscapeEnd in text.go.

// queryHitCount counts only actual hits, recursively; gray breadcrumb rows do
// not inflate the suffix. Old flat mirrors (before lock bits) still count.
func queryHitCount(q *item) int {
	n := 0
	var walk func(*item)
	walk = func(it *item) {
		for _, c := range it.children {
			if c.mirrorOf != "" && (!c.structureLocked || !c.readonly) {
				n++
			}
			walk(c)
		}
	}
	walk(q)
	return n
}
