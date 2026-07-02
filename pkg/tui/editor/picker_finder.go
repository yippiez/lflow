package editor

// GROUP B — the shared full-body searchable finder. Unlike the Group-A list
// pickers it replaces the whole editor body: a query line at the top, a result
// list, and a hint/status footer. It backs the node finder today (/mirror,
// /move, /goto, /bring, "[[" link) via nodeFinderBackend (see editor.go).
//
// bodyFinder owns the query, selection, and result set; the backend owns where
// results come from, how they are filtered/decorated, and what a pick does.

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lflow/lflow/pkg/tui/database"
)

// bodyFinder is the shared full-body picker state.
type bodyFinder struct {
	query string
	sel   int
	hits  []finderRow
	act   finderAction // which action a pick performs (also selects the backend behavior)
}

// finderRow is one fully-counted result: the node plus its subtree count,
// precomputed by search() so view() needs no CountSubtree call.
type finderRow struct {
	node  database.Node
	count int
}

// finderBackend supplies results for a bodyFinder and commits a pick.
type finderBackend interface {
	// search returns the already-filtered, count-decorated rows for the query. An
	// empty query returns a sensible full list (recent nodes), not nothing.
	search(m *Model, query string) []finderRow
	// onSelect commits the highlighted row (mirror/move/goto/bring/link).
	onSelect(m *Model, row finderRow) (tea.Model, tea.Cmd)
	// interceptEnter runs before row selection; the link backend uses it to link a
	// URL query to a website. handled=false falls through to onSelect.
	interceptEnter(m *Model, query string) (handled bool, _ tea.Model, _ tea.Cmd)
	// queryAffordance is an optional extra line under the query (the "[[ link to
	// <url>" hint); "" for none.
	queryAffordance(m *Model, query string) string
	// label / hint drive the header and footer text.
	label(m *Model) string
	hint(m *Model) string
}

// open resets the finder and runs the initial (empty-query) search.
func (f *bodyFinder) open(m *Model, act finderAction, be finderBackend) {
	f.act = act
	f.query = ""
	f.sel = 0
	f.hits = nil
	f.refresh(m, be)
}

// refresh re-runs the backend search for the current query and re-clamps sel.
func (f *bodyFinder) refresh(m *Model, be finderBackend) {
	f.hits = be.search(m, f.query)
	if f.sel >= len(f.hits) {
		f.sel = 0
	}
}

// handleKey is the finder's esc/up/down/enter/backspace/runes loop. On Enter it
// gives the backend a chance to intercept (the URL-link case) before selecting
// the highlighted row.
func (f *bodyFinder) handleKey(m *Model, k tea.KeyMsg, be finderBackend) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "esc":
		m.mode = modeOutline
		return m, nil
	case "up":
		if f.sel > 0 {
			f.sel--
		}
		return m, nil
	case "down":
		if f.sel < len(f.hits)-1 {
			f.sel++
		}
		return m, nil
	case "backspace":
		if len(f.query) > 0 {
			f.query = f.query[:len(f.query)-1]
			f.refresh(m, be)
		}
		return m, nil
	case "enter":
		if handled, mm, cmd := be.interceptEnter(m, f.query); handled {
			return mm, cmd
		}
		if f.sel < len(f.hits) {
			return be.onSelect(m, f.hits[f.sel])
		}
		m.mode = modeOutline
		return m, nil
	}
	if k.Type == tea.KeySpace && !k.Alt {
		k.Type, k.Runes = tea.KeyRunes, []rune{' '}
	}
	if k.Type == tea.KeyRunes && !k.Alt {
		f.query += string(k.Runes)
		f.refresh(m, be)
	}
	return m, nil
}

// view renders the full-body finder: query line, optional URL-link affordance,
// the truncated result rows with precomputed counts and per-node styling, an
// overflow "… N more" line, then the hint + bottom bar. Counts are precomputed;
// only the shown rows' mirror names are resolved (a cheap per-row GetNode).
func (f *bodyFinder) view(m *Model, be finderBackend, maxLine int) []string {
	var lines []string

	query := cDim + " " + be.label(m) + " " + cFG + withCaret(f.query, len([]rune(f.query))) + cReset
	lines = append(lines, clip(query, maxLine))

	if aff := be.queryAffordance(m, f.query); aff != "" {
		lines = append(lines, clip(aff, maxLine))
	}

	maxResults := m.height - 4
	if maxResults < 3 {
		maxResults = 8
	}
	shown := f.hits
	overflow := 0
	if len(shown) > maxResults {
		overflow = len(shown) - maxResults
		shown = shown[:maxResults]
	}

	for i, r := range shown {
		mark := "   "
		if i == f.sel {
			mark = cAccent + " ▸ " + cReset
		}
		name := displayAnchors(finderRowName(r.node, func(uuid string) (database.Node, bool) {
			n, err := database.GetNode(m.db, uuid)
			return n, err == nil
		}), m.chips)
		// carry the node's own /color and /bold-/italic-/underline into the picker
		// so a styled node reads the same here as in the outline
		base := cFG
		if c := styleBaseColor(r.node.Style); c != "" {
			base = c
		}
		label := base + styleAttrs(r.node.Style) + fmt.Sprintf("%-28s", name) + cReset
		line := mark + label + cDim + fmt.Sprintf(" %d nodes", r.count) + cReset
		lines = append(lines, clip(line, maxLine))
	}
	if overflow > 0 {
		lines = append(lines, clip(cDim+fmt.Sprintf("   … %d more", overflow)+cReset, maxLine))
	}
	if len(shown) == 0 {
		lines = append(lines, cDim+"   no matches"+cReset)
	}

	lines = append(lines, "")
	lines = append(lines, clip(cDim+" "+be.hint(m)+" - esc back to outline"+cReset, maxLine))
	lines = append(lines, m.bottomBar(maxLine))

	return lines
}
