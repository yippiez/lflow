package editor

// GROUP B — full-body searchable finder.
//
// SCAFFOLD ONLY: signatures + data structures + TODOs for review. Bodies panic so
// the package still builds.
//
// Unlike the Group A list pickers this one replaces the whole editor body: a
// query line at the top, a scrolling result list that fills the height, and a
// hint/status footer (see viewFinder). It is backed by a live data source
// (SQLite today) that is re-queried on every keystroke, not a static slice.
//
// One component already serves five actions through finderAct: /mirror, /move,
// /goto, /bring, and "[[" link insertion. The goal here is to keep that single
// component but hide the DB/query plumbing behind finderBackend so a non-node
// finder (files, agents, arbitrary lists) could reuse it.

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/lflow/lflow/pkg/tui/database"
)

// bodyFinder is the shared full-body picker. It owns the query, the selection,
// and the current result set; the backend owns where results come from and what
// a pick does.
type bodyFinder struct {
	query string
	sel   int
	hits  []finderRow
	act   finderAction // which action a pick performs (drives label/hint)
}

// finderRow is one result: the node plus any precomputed decoration the row
// needs. Kept as a struct (not a bare database.Node) so synthesized rows — the
// Agent Domain nodes /bring surfaces — sit in the same list without a DB row.
type finderRow struct {
	node  database.Node
	count int // subtree node count shown dim on the right; 0 = unknown/synthetic
	// TODO: some rows carry style from the node (styleBaseColor/styleAttrs). Decide
	// whether to precompute a rendered label here or keep styling in the renderer.
}

// finderBackend supplies results for a bodyFinder and commits a pick. The node
// finder's implementation wraps RecentNodes/SearchNodes + the /bring Agent Domain
// merge; other backends could front a filesystem or an in-memory list.
type finderBackend interface {
	// search returns results for the query. An empty query should return a sensible
	// full list (RecentNodes today), not nothing.
	// TODO: this is synchronous but hits SQLite on every keystroke. Decide if it
	// stays inline or moves behind a tea.Cmd for large outlines.
	search(m *Model, query string) []finderRow

	// onSelect commits the highlighted row (mirror/move/goto/bring/link).
	onSelect(m *Model, row finderRow) (tea.Model, tea.Cmd)

	// label / hint drive the header and footer text, keyed off the action today.
	label() string
	hint() string
}

// open resets the finder and runs the initial (empty-query) search.
func (f *bodyFinder) open(m *Model, act finderAction, be finderBackend) {
	panic("TODO: implement bodyFinder.open")
}

// refresh re-runs the backend search for the current query and re-clamps sel.
// TODO: fold in the caller-specific filtering the current refreshFinder does —
// dropping the cursor node, dropping empty /goto targets — via the backend, not
// hardcoded here.
func (f *bodyFinder) refresh(m *Model, be finderBackend) {
	panic("TODO: implement bodyFinder.refresh")
}

// handleKey is the finder's esc/up/down/enter/backspace/runes loop.
//   - enter: onSelect the highlighted row; but for "[[" a URL query links to the
//     site instead of a node — TODO: keep that special-case out of the shared
//     component (a backend hook that can intercept enter before selection?).
func (f *bodyFinder) handleKey(m *Model, k tea.KeyMsg, be finderBackend) (tea.Model, tea.Cmd) {
	panic("TODO: implement bodyFinder.handleKey")
}

// view renders the full-body finder: query line, optional URL-link affordance,
// the windowed result rows with subtree counts and per-node styling, an overflow
// "… N more" line, then the hint + bottom bar.
// TODO: the current viewFinder computes CountSubtree per visible row via a DB
// call — keep that here or precompute counts into finderRow during refresh.
func (f *bodyFinder) view(m *Model, be finderBackend, maxLine int) []string {
	panic("TODO: implement bodyFinder.view")
}
