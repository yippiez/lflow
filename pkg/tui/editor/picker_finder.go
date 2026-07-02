package editor

// GROUP B — full-body searchable finder.
//
// SCAFFOLD ONLY: signatures + data structures + TODOs for review. Real logic
// panics so the package still builds.
//
// Unlike the Group A list pickers this one replaces the whole editor body: a
// query line at the top, a scrolling result list that fills the height, and a
// hint/status footer (see viewFinder). It is backed by a live data source
// (SQLite today) re-queried on every keystroke.
//
// One component already serves five actions through finderAct: /mirror, /move,
// /goto, /bring, and "[[" link insertion. The goal is to keep that single
// component but hide the data plumbing behind finderBackend.
//
// Design decisions locked in review:
//   - search stays synchronous for now (local SQLite; revisit if it janks)
//   - the backend OWNS filtering: it returns the already-filtered + merged rows
//     (drop-cursor-node, /goto empty-node drop, /bring Agent-Domain merge all
//     live in the node backend, not here)
//   - the backend OWNS the "[[" URL Enter override via interceptEnter
//   - finderRow is fully decorated by search() (count precomputed); view() does
//     no DB calls and no per-frame recompute

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/lflow/lflow/pkg/tui/database"
)

// bodyFinder is the shared full-body picker. It owns the query, the selection,
// and the current result set; the backend owns where results come from, how they
// are filtered/decorated, and what a pick does.
type bodyFinder struct {
	query string
	sel   int
	hits  []finderRow
	act   finderAction // which action a pick performs (also selects the backend)
}

// finderRow is one fully-decorated result: the node plus everything view() needs
// to draw it without touching the DB. search() fills these in.
type finderRow struct {
	node  database.Node
	count int // subtree node count shown dim on the right; precomputed in search()
	// TODO: if per-node style ends up expensive to recompute in view(), precompute
	// the styled name string here too. Start with node.Style in view() and only
	// cache if profiling says so.
}

// finderBackend supplies results for a bodyFinder and commits a pick. The node
// backend wraps RecentNodes/SearchNodes + the caller-specific filtering and the
// /bring Agent-Domain merge; other backends could front a filesystem or an
// in-memory list.
type finderBackend interface {
	// search returns the already-filtered, already-decorated rows for the query
	// (finderRow.count populated). An empty query returns a sensible full list
	// (RecentNodes today), not nothing. Synchronous by decision.
	search(m *Model, query string) []finderRow

	// onSelect commits the highlighted row (mirror/move/goto/bring/link).
	onSelect(m *Model, row finderRow) (tea.Model, tea.Cmd)

	// interceptEnter runs before row selection. The link backend uses it to link
	// to a website when the query is a URL. handled=false falls through to normal
	// onSelect on the highlighted row. Backends without an override return false.
	interceptEnter(m *Model, query string) (handled bool, _ tea.Model, _ tea.Cmd)

	// label / hint drive the header and footer text ("/mirror", "Enter mirror at
	// cursor", …).
	label() string
	hint() string
}

// open resets the finder and runs the initial (empty-query) search.
func (f *bodyFinder) open(m *Model, act finderAction, be finderBackend) {
	panic("TODO: implement bodyFinder.open")
}

// refresh re-runs the backend search for the current query and re-clamps sel.
// Thin now: the backend returns finished rows, so this is essentially
// `f.hits = be.search(m, f.query)` plus the sel clamp.
func (f *bodyFinder) refresh(m *Model, be finderBackend) {
	panic("TODO: implement bodyFinder.refresh")
}

// handleKey is the finder's esc/up/down/enter/backspace/runes loop. On Enter it
// calls be.interceptEnter first, then falls through to be.onSelect on the
// highlighted row.
func (f *bodyFinder) handleKey(m *Model, k tea.KeyMsg, be finderBackend) (tea.Model, tea.Cmd) {
	panic("TODO: implement bodyFinder.handleKey")
}

// view renders the full-body finder: query line, optional URL-link affordance,
// the windowed result rows with precomputed counts and per-node styling, an
// overflow "… N more" line, then the hint + bottom bar. No DB calls here.
func (f *bodyFinder) view(m *Model, be finderBackend, maxLine int) []string {
	panic("TODO: implement bodyFinder.view")
}
