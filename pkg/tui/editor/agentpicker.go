package editor

import (
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// The session picker (a Group-A list, see picker_list.go): /insert → agent
// searches every session the three CLIs already have and files the one you pick
// as a chip at the caret. It is the only agent surface — there is no /agent and
// no /agents, because a chip belongs to the row it sits in and the outline is
// already the index of them.

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
		color := v.colorSGR()
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
