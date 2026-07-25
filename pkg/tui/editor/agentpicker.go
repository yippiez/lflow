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
//   - /agent (agentStartSource) starts a session here, or ATTACHES one that
//     already exists in a CLI's own store — the sessions you began in a terminal
//     before you thought to file them. It is also the /insert "agent" chip flow;
//     the target (node or chip) is m.agentPickChip.
//   - /agents (agentListSource) lists every session in the outline, live ones
//     first and done ones after, muted gray — the index of everything you have
//     going.

// agentPickTarget says what the /agent picker is choosing FOR.
type agentPickTarget int

const (
	agentPickNode agentPickTarget = iota // retype the cursor node as a session
	agentPickChip                        // splice a session chip at the caret
)

// openAgentPicker opens the start/attach picker for a target.
func (m *Model) openAgentPicker(target agentPickTarget) {
	m.mode = modeAgentPick
	m.agentPickTarget = target
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

func (agentStartSource) items(m *Model, q string) []pickerItem {
	ql := strings.ToLower(strings.TrimSpace(q))
	var out []pickerItem

	// the "new session" rows: one per variant, in registry order
	for _, v := range agentVariants {
		v := v
		if ql != "" && !fuzzyMatch(strings.ToLower(v.label), ql) && !fuzzyMatch(v.id, ql) && !fuzzyMatch("new", ql) {
			continue
		}
		bin, missing := m.typeDepMissing(v.key)
		out = append(out, pickerItem{value: "new/" + v.id, render: func(bool) string {
			if missing {
				return cDim + fmt.Sprintf("%-2s new %-9s", v.glyph, v.id) + " · missing " + bin + cReset
			}
			return v.colorSGR() + fmt.Sprintf("%-2s ", v.glyph) + cReset +
				cFG + fmt.Sprintf("new %-9s", v.id) + cReset + cDim + " start a fresh session here" + cReset
		}})
	}

	// then the sessions already in the CLIs' stores — attach one to this node
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
		if ql != "" && !fuzzyMatch(strings.ToLower(label), ql) && !fuzzyMatch(v.id, ql) &&
			!fuzzyMatch(strings.ToLower(tildePath(s.cwd)), ql) {
			continue
		}
		color := s.color
		if color == "" {
			color = v.colorSGR()
		}
		out = append(out, pickerItem{value: "use/" + v.id + "/" + s.id, render: func(bool) string {
			// an existing session is drawn as the pill it will become
			meta := []string{}
			if s.cwd != "" {
				meta = append(meta, tildePath(s.cwd))
			}
			if !s.updated.IsZero() {
				meta = append(meta, relTime(s.updated.Unix()))
			}
			row := bgOf(color) + contrastInk(color) + " " + v.glyph + " " + clipStr(label, 40) + " " + cReset
			if len(meta) > 0 {
				row += cDim + " " + strings.Join(meta, " · ") + cReset
			}
			return row
		}})
	}
	return out
}

func (agentStartSource) header(m *Model, p *listPicker) string {
	what := "session on this node"
	if m.agentPickTarget == agentPickChip {
		what = "session chip"
	}
	if p.query != "" {
		return " " + cDim + "agent: " + cReset + cFG + p.query + cReset
	}
	return " " + cDim + "start or attach a " + what + " · type to search" + cReset
}

func (agentStartSource) initialSel(*Model) int { return 0 }

func (agentStartSource) onSelect(m *Model, it pickerItem) (tea.Model, tea.Cmd) {
	m.mode = modeOutline
	cur := m.cursorItem()
	if it.value == "" || cur == nil {
		return m, nil
	}
	parts := strings.SplitN(it.value, "/", 3)
	v, ok := agentVariantByID(parts[1])
	if !ok {
		return m, nil
	}
	if bin, missing := m.typeDepMissing(v.key); missing && m.agentPickTarget == agentPickNode {
		m.flash = "Missing dependency: " + bin
		return m, nil
	}

	var attach agentStoreSession
	if parts[0] == "use" && len(parts) == 3 {
		for _, s := range m.agentStore {
			if s.variant == v.id && s.id == parts[2] {
				attach = s
				break
			}
		}
	}

	if m.agentPickTarget == agentPickChip {
		m.insertAgentChip(cur, v, attach)
		return m, nil
	}

	// the node itself becomes the session: retype it, pin its directory, and give
	// it the session's own name when it is still unnamed
	if cur.readonly || cur.mirrorOf != "" {
		m.flash = "node is not editable"
		return m, nil
	}
	m.pushUndo("")
	cur.typ = v.key
	m.unsaved = true
	if attach.id != "" {
		m.agentAttach(cur.uuid, attach)
		if strings.TrimSpace(cur.name) == "" && attach.title != "" {
			cur.name = attach.title
			m.caret = len([]rune(cur.name))
		}
		m.flash = v.label + " · attached " + agentShortID(attach.id)
	} else {
		s := m.agentLoad(cur.uuid)
		s.Variant, s.Cwd = v.id, m.agentNodeCwd(cur)
		m.agentSave(cur.uuid, s)
		m.flash = v.label + " · ⌥r opens it"
	}
	m.publishAgentLook(cur.uuid, v)
	return m, nil
}

// --- /agents: every session in the outline ----------------------------------

// agentRow is one row of the /agents list: a session handle (node or chip) with
// what it takes to rank, draw and jump to it.
type agentRow struct {
	uuid    string // the node to jump to (a chip row jumps to its host node)
	id      string // the handle's storage id (node uuid, or chip id)
	variant agentVariant
	name    string // the outline's name for it
	sess    agentSession
	done    bool
	chip    bool
}

// openAgentsList opens the /agents index. The list is read from the database, so
// pending edits are flushed first — a session started seconds ago (or, with no
// daemon, a whole session's worth of edits) must be in the index that claims to
// list them all.
func (m *Model) openAgentsList() {
	_ = m.flushSync()
	m.mode = modeAgents
	m.agentRows = m.collectAgentRows()
	m.list.open(m, agentListSource{}, true)
}

// collectAgentRows gathers every session in the outline: the session NODES, and
// the session CHIPS living inside ordinary rows. Done ones sink below live ones;
// within each group the most recently opened comes first.
func (m *Model) collectAgentRows() []agentRow {
	if m.db == nil {
		return nil
	}
	var rows []agentRow

	keys := make([]string, 0, len(agentVariants))
	for _, v := range agentVariants {
		keys = append(keys, "'"+v.key+"'")
	}
	nodes, err := database.GetNodesWhere(m.db, "type IN ("+strings.Join(keys, ",")+")")
	if err == nil {
		for _, n := range nodes {
			v, ok := agentVariantOf(n.Type)
			if !ok {
				continue
			}
			rows = append(rows, agentRow{
				uuid: n.UUID, id: n.UUID, variant: v,
				name: oneLine(displayAnchors(n.Name, m.chips)),
				sess: m.agentLoad(n.UUID), done: n.CompletedAt > 0,
			})
		}
	}

	// the chips: one scan of the rows that carry any chip anchor, matched against
	// the loaded chip store — a session chip's host node is what a pick jumps to
	if hosts, err := database.GetNodesWhere(m.db, "name LIKE ?", "%"+string(chipSentinel)+"%"); err == nil {
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
				rows = append(rows, agentRow{
					uuid: n.UUID, id: c.ID, variant: v,
					name: oneLine(displayAnchors(n.Name, m.chips)),
					sess: m.agentLoad(c.ID), done: n.CompletedAt > 0, chip: true,
				})
			}
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

// agentListRow draws one /agents row in exactly the language the outline uses:
// the session's PILL — glyph and name on its agent's color — then muted gray
// info to its right. A DONE session fills gray instead and sits below the live
// ones, so the list reads active-then-settled at a glance.
func (m *Model) agentListRow(r agentRow, title string) string {
	fill := m.agentColorFor(r.id, r.variant, r.sess)
	if r.done {
		fill = cDim
	}
	name := title
	if strings.TrimSpace(name) == "" {
		name = r.name
	}
	pill := bgOf(fill) + contrastInk(fill) + " " + r.variant.glyph + " " + clipStr(name, 32) + " " + cReset

	var meta []string
	if r.name != "" && r.name != name {
		meta = append(meta, clipStr(r.name, 30)) // the node the session is filed under
	}
	if r.chip {
		meta = append(meta, "chip")
	}
	if m.agentCloud(r.variant, r.sess) {
		meta = append(meta, glyphCloud+" hosted")
	}
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

// onSelect jumps the editor to the picked session's node — the same jump /goto
// makes, so a session is one keystroke away from wherever you are.
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
