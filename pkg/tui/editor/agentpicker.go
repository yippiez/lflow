package editor

import (
	"fmt"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lflow/lflow/pkg/tui/database"
)

// The two session pickers, both Group-A lists (see picker_list.go):
//
//   - /agent (agentStartSource) drops a session chip at the caret: a fresh
//     session, or one that already exists in a CLI's own store — the sessions you
//     began in a terminal before you thought to file them. /insert → agent is the
//     same picker.
//   - /agents (agentListSource) lists every session chip in the outline, live
//     ones first and done ones after — the index of everything you have going.

// openAgentPicker opens the start/attach picker.
func (m *Model) openAgentPicker() {
	m.mode = modeAgentPick
	m.agentStore = m.discoverAgentSessions()
	m.list.open(m, agentStartSource{}, true)
}

// discoverAgentSessions reads every variant's own store and returns the sessions
// found, newest first. Bounded by agentStoreFiles; a CLI that is not installed
// (or has no store) simply contributes nothing.
func (m *Model) discoverAgentSessions() []agentStoreSession {
	var out []agentStoreSession
	seen := map[string]bool{}
	for _, v := range agentVariants {
		roots := v.sessionDirs()
		if len(roots) == 0 {
			continue
		}
		for _, path := range agentStoreFiles(roots, v.exts, v.sessionPath) {
			s := agentReadMeta(v.id, path)
			if s.id == "" || seen[v.id+"/"+s.id] {
				continue
			}
			seen[v.id+"/"+s.id] = true
			out = append(out, s)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].updated.After(out[j].updated) })
	return out
}

// --- /agent: start or attach ------------------------------------------------

type agentStartSource struct{}

// items lists the sessions found across ALL THREE stores, newest first, filtered
// by the query. There are no "new session" rows: lflow attaches to conversations
// their CLI already made, so this list IS the picker.
func (agentStartSource) items(m *Model, q string) []pickerItem {
	ql := strings.ToLower(strings.TrimSpace(q))
	var out []pickerItem
	for _, s := range m.agentStore {
		s := s
		v, ok := agentVariantByID(s.variant)
		if !ok {
			continue
		}
		label := s.title
		if label == "" {
			label = "session " + agentShortID(s.id)
		}
		// searched by NAME first, then by which agent it is and where it ran
		if ql != "" && !fuzzyMatch(strings.ToLower(label), ql) && !fuzzyMatch(v.id, ql) &&
			!fuzzyMatch(strings.ToLower(tildePath(s.cwd)), ql) {
			continue
		}
		color := s.color
		if color == "" {
			color = v.colorSGR()
		}
		out = append(out, pickerItem{value: "use/" + v.id + "/" + s.id, render: func(bool) string {
			// a session reads as the pill it is about to become, then where it ran
			// and when it last moved
			meta := []string{}
			if s.cwd != "" {
				meta = append(meta, tildePath(s.cwd))
			}
			if !s.updated.IsZero() {
				meta = append(meta, relTime(s.updated.Unix()))
			}
			row := bgOf(color) + contrastInk(color) + " " + v.glyph + " " + clipStr(label, 40) + " " + cReset
			if len(meta) > 0 {
				row += cDim + " · " + strings.Join(meta, " · ") + cReset
			}
			return row
		}})
	}
	return out
}

func (agentStartSource) header(m *Model, p *listPicker) string {
	if p.query != "" {
		return " " + cDim + "agent: " + cReset + cFG + p.query + cReset
	}
	return " " + cDim + "search your coding sessions · enter files one here" + cReset
}

func (agentStartSource) initialSel(*Model) int { return 0 }

func (agentStartSource) onSelect(m *Model, it pickerItem) (tea.Model, tea.Cmd) {
	m.mode = modeOutline
	cur := m.cursorItem()
	if it.value == "" || cur == nil {
		return m, nil
	}
	parts := strings.SplitN(it.value, "/", 3)
	if len(parts) != 3 {
		return m, nil
	}
	v, ok := agentVariantByID(parts[1])
	if !ok {
		return m, nil
	}
	var attach agentStoreSession
	for _, s := range m.agentStore {
		if s.variant == v.id && s.id == parts[2] {
			attach = s
			break
		}
	}
	m.insertAgentChip(cur, v, attach)
	return m, nil
}

// --- /agents: every session in the outline ----------------------------------

// agentRow is one row of the /agents list: a session chip with what it takes to
// rank, draw and jump to it.
type agentRow struct {
	uuid    string // the node carrying the chip — where a pick jumps to
	id      string // the chip id, which is the session handle's storage key
	variant agentVariant
	name    string // the row the chip lives in
	sess    agentSession
	done    bool
}

// openAgentsList opens the /agents index. The list is read from the database, so
// pending edits are flushed first — a session started seconds ago must be in the
// index that claims to list them all.
func (m *Model) openAgentsList() {
	_ = m.flushSync()
	m.mode = modeAgents
	m.agentRows = m.collectAgentRows()
	m.list.open(m, agentListSource{}, true)
}

// collectAgentRows gathers every session chip in the outline, in one scan of the
// rows that carry any chip anchor. A chip inside a completed row counts as done:
// finished work sinks below the live sessions.
func (m *Model) collectAgentRows() []agentRow {
	if m.db == nil {
		return nil
	}
	hosts, err := database.GetNodesWhere(m.db, "name LIKE ?", "%"+string(chipSentinel)+"%")
	if err != nil {
		return nil
	}
	var rows []agentRow
	for _, n := range hosts {
		for _, sp := range anchorSpans([]rune(n.Name)) {
			c, ok := m.chips[sp.id]
			if !ok || c.Kind != chipKindAgent {
				continue
			}
			v, ok := agentVariantByID(c.Value)
			if !ok {
				continue
			}
			// the row's own words, with this chip cut out of them: the pill beside
			// them already says which session it is
			name := oneLine(displayAnchors(strings.ReplaceAll(n.Name, chipAnchor(c.ID), ""), m.chips))
			rows = append(rows, agentRow{
				uuid: n.UUID, id: c.ID, variant: v,
				name: strings.TrimSpace(name),
				sess: m.agentLoad(c.ID), done: n.CompletedAt > 0,
			})
		}
	}
	sort.SliceStable(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if a.done != b.done {
			return !a.done // live sessions first, done ones after
		}
		return a.sess.OpenedAt > b.sess.OpenedAt
	})
	return rows
}

type agentListSource struct{}

func (agentListSource) items(m *Model, q string) []pickerItem {
	ql := strings.ToLower(strings.TrimSpace(q))
	var out []pickerItem
	for _, r := range m.agentRows {
		r := r
		title := m.agentTitle(r.id, r.variant, r.sess)
		hay := strings.ToLower(r.name + " " + title + " " + r.variant.id + " " + r.sess.Cwd)
		if ql != "" && !strings.Contains(hay, ql) && !fuzzyMatch(hay, ql) {
			continue
		}
		out = append(out, pickerItem{value: r.uuid, render: func(bool) string { return m.agentListRow(r, title) }})
	}
	return out
}

// agentListRow draws one /agents row in the language the outline uses: the
// session's PILL — glyph and name on its agent's color — then muted gray info.
// A DONE session fills gray and sits below the live ones, so the list reads
// active-then-settled at a glance.
func (m *Model) agentListRow(r agentRow, title string) string {
	fill := m.agentColorFor(r.id, r.variant, r.sess)
	if r.done {
		fill = cDim
	}
	pill := bgOf(fill) + contrastInk(fill) + " " + r.variant.glyph + " " + clipStr(title, 32) + " " + cReset

	meta := []string{clipStr(r.name, 30)} // the row the chip is filed in
	if m.agentLive(r.variant, r.sess) {
		meta = append(meta, "live")
	}
	if r.sess.Cwd != "" {
		meta = append(meta, tildePath(r.sess.Cwd))
	}
	switch {
	case r.done:
		meta = append(meta, "done")
	case r.sess.OpenedAt > 0:
		meta = append(meta, relTime(r.sess.OpenedAt))
	default:
		meta = append(meta, "not opened")
	}
	return pill + cDim + " " + strings.Join(meta, " · ") + cReset
}

func (agentListSource) header(m *Model, p *listPicker) string {
	live, done := 0, 0
	for _, r := range m.agentRows {
		if r.done {
			done++
		} else {
			live++
		}
	}
	head := fmt.Sprintf("agents: %d live · %d done", live, done)
	if p.query != "" {
		return " " + cDim + head + " · " + cReset + cFG + p.query + cReset
	}
	return " " + cDim + head + " · enter opens · type to search" + cReset
}

func (agentListSource) initialSel(*Model) int { return 0 }

// onSelect jumps the editor to the row the picked session is filed in — the same
// jump /goto makes, so a session is one keystroke away from wherever you are.
func (agentListSource) onSelect(m *Model, it pickerItem) (tea.Model, tea.Cmd) {
	m.mode = modeOutline
	if it.value == "" {
		return m, nil
	}
	n, err := database.GetNode(m.db, it.value)
	if err != nil {
		m.flash = "agents: " + err.Error()
		return m, nil
	}
	if err := m.jumpTo(n); err != nil {
		m.err = err
		return m.quit()
	}
	return m, nil
}
