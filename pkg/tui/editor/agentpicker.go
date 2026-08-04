package editor

import (
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// The session picker (a Group-A list, see picker_list.go): /insert → agent
// files a session as a chip at the caret, while /type → Agent binds the whole
// node. Both surfaces point to the CLI's local store; neither copies a transcript
// into the outline.

// openAgentPicker opens the picker for an inline chip.
func (m *Model) openAgentPicker() tea.Cmd {
	return m.openAgentPickerForNode("")
}

// openAgentPickerForNode opens the start/attach picker. It opens EMPTY and fills
// as the stores are read. nodeUUID is empty for a chip or the Agent node to bind.
func (m *Model) openAgentPickerForNode(nodeUUID string) tea.Cmd {
	m.mode = modeAgentPick
	m.agentPickNode = nodeUUID
	m.agentStore = nil
	m.agentSeen = map[string]bool{}
	m.agentFill.begin()

	// buffered past the most batches a bounded scan can produce, so the walking
	// goroutine always runs to completion even if the picker is closed on its
	// first frame — nothing is left blocked on a receive that will not come
	ch := make(chan tea.Msg, 64)
	m.agentScanCh = ch
	go scanAgentSessions(ch)

	m.list.open(m, agentStartSource{}, true)
	return waitFill(ch)
}

// agentScanBatch is how many sessions are read before a batch is sent. Small
// enough that the first rows land almost at once, big enough that the update
// loop is not woken once per file.
const agentScanBatch = 24

// agentScanMsg is one batch of sessions from the store walk, or the end of it.
// ch identifies the scan that sent it, so a picker reopened mid-walk ignores
// what the previous walk is still delivering.
type agentScanMsg struct {
	ch    chan tea.Msg
	batch []agentStoreSession
	done  bool
}

// scanAgentSessions reads every variant's own store and sends the sessions it
// finds in batches, newest first. Runs OFF the update goroutine and touches no
// Model state — everything it learns travels back as a message. Bounded by
// agentStoreFiles; a CLI that is not installed (or has no store) contributes
// nothing.
func scanAgentSessions(ch chan tea.Msg) {
	batch := make([]agentStoreSession, 0, agentScanBatch)
	flush := func() {
		if len(batch) == 0 {
			return
		}
		ch <- agentScanMsg{ch: ch, batch: batch}
		batch = make([]agentStoreSession, 0, agentScanBatch)
	}
	for _, v := range agentVariants {
		roots := v.sessionDirs()
		if len(roots) == 0 {
			continue
		}
		// the CLI's own index of what its sessions are called, read ONCE per
		// store rather than per session: for Claude Code the name and color live
		// beside the transcript, not in it
		var idents map[string]agentIdent
		if v.idents != nil {
			idents = v.idents()
		}
		for _, path := range agentStoreFiles(roots, v.exts, v.sessionPath) {
			s := agentReadMeta(v.id, path)
			if s.id == "" {
				continue
			}
			s.applyIdent(idents[s.id])
			if batch = append(batch, s); len(batch) == agentScanBatch {
				flush()
			}
		}
		flush() // a store finishes as a batch of its own, however short
	}
	ch <- agentScanMsg{ch: ch, done: true}
}

// handleAgentScan merges a batch into the open picker and asks for the next
// one. Duplicates are dropped here rather than in the walk, because the walk
// has no memory across stores; the merged list is re-sorted every time, since
// a later store's sessions interleave with an earlier one's by age.
func (m *Model) handleAgentScan(msg agentScanMsg) tea.Cmd {
	if msg.ch != m.agentScanCh {
		return nil // a scan from a picker that has since been reopened
	}
	if msg.done {
		m.agentFill.done = true
		return nil
	}
	for _, s := range msg.batch {
		key := s.variant + "/" + s.id
		if m.agentSeen[key] {
			continue
		}
		m.agentSeen[key] = true
		m.agentStore = append(m.agentStore, s)
	}
	sort.SliceStable(m.agentStore, func(i, j int) bool {
		return m.agentStore[i].updated.After(m.agentStore[j].updated)
	})
	m.agentFill.n = len(m.agentStore)
	return waitFill(msg.ch)
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
		// a session the CLI gave a color of its own wears it here too — the row is
		// a preview of the chip it becomes, so it must not show a color the chip
		// will not have
		color := v.colorSGR()
		if c := agentColorSGR(s.color); c != "" {
			color = c
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
	// the tail carries the spinner and the running count while the stores are
	// still being read, so a list that is still growing says so
	mark := fillMark(&m.agentFill, "sessions")
	if p.query != "" {
		return " " + cDim + "agent: " + cReset + cFG + p.query + cReset + mark
	}
	return " " + cDim + "search your coding sessions · enter files one here" + cReset + mark
}

func (agentStartSource) initialSel(*Model) int { return 0 }

func (agentStartSource) onSelect(m *Model, it pickerItem) (tea.Model, tea.Cmd) {
	m.mode = modeOutline
	target := m.agentPickNode
	m.agentPickNode = ""
	cur := m.cursorItem()
	if it.value == "" || (cur == nil && target == "") {
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
	if target != "" {
		m.bindAgentNode(target, v, attach)
		return m, nil
	}
	m.insertAgentChip(cur, v, attach)
	return m, nil
}
