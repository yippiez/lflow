package editor

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/lflow/lflow/packages/database"
)

// The editor's review surface for edit suggestions (see packages/cli/suggest and
// database/suggestion.go). A pending suggestion is a proposal somebody — a
// teammate, an agent, another terminal — parked against a node: it has changed
// nothing, and the outline reads exactly as it did before it arrived.
//
// It shows up as a band under the node it is about, the same surface a note or
// a run output hangs from, so a suggestion arriving over the live feed appears
// in place while you work. alt+v on that node opens review: the band expands to
// the proposal, y approves it (applying it to the outline) and n rejects it.
//
// WARNING (invariant): the band is a READING of the suggestions table, never a
// node. Approving is the only path that writes to the tree, and it goes through
// database.ApplySuggestion — nothing here edits a node directly.

// loadSuggests refreshes the pending review queue, grouped by the node each
// suggestion is about. Cheap (the queue is short) and safe to call on any
// change; with no database the editor simply has no review surface.
func (m *Model) loadSuggests() {
	if m.db == nil {
		m.suggests = nil
		return
	}
	list, err := database.ListSuggestions(m.db, database.SuggestionFilter{Status: database.SuggestPending})
	if err != nil {
		return // a failed read leaves the previous queue; the next event retries
	}
	byTarget := map[string][]database.Suggestion{}
	// The map answers "what is pending on this node"; the order answers "which
	// node next". ListSuggestions orders by (created_on, uuid), so the queue walks
	// oldest-first and the same way on every machine — a map alone loses that.
	var order []string
	for _, s := range list {
		if _, seen := byTarget[s.TargetUUID]; !seen {
			order = append(order, s.TargetUUID)
		}
		byTarget[s.TargetUUID] = append(byTarget[s.TargetUUID], s)
	}
	m.suggests, m.suggestOrder = byTarget, order
	m.clampSuggestSel()
}

// suggestsFor returns the pending suggestions about one node.
func (m *Model) suggestsFor(uuid string) []database.Suggestion {
	if uuid == "" {
		return nil
	}
	return m.suggests[uuid]
}

// pendingSuggestCount is the whole queue's size, for the status bar.
func (m *Model) pendingSuggestCount() int {
	n := 0
	for _, list := range m.suggests {
		n += len(list)
	}
	return n
}

// clampSuggestSel keeps the review cursor inside the reviewed node's list —
// the queue can shrink underneath it when another client settles a suggestion.
func (m *Model) clampSuggestSel() {
	if m.mode != modeSuggest {
		m.suggestSel = 0
		return
	}
	list := m.suggestsFor(m.suggestUUID)
	if len(list) == 0 {
		m.mode = modeOutline
		m.suggestUUID = ""
		m.suggestSel = 0
		return
	}
	if m.suggestSel >= len(list) {
		m.suggestSel = len(list) - 1
	}
	if m.suggestSel < 0 {
		m.suggestSel = 0
	}
}

// enterSuggestReview opens review on the cursor node. A node with nothing
// pending says so, and points at the rest of the queue if there is any.
func (m *Model) enterSuggestReview() {
	cur := m.cursorItem()
	if cur == nil {
		return
	}
	if len(m.suggestsFor(cur.uuid)) == 0 {
		if n := m.pendingSuggestCount(); n > 0 {
			m.flash = fmt.Sprintf("no suggestions here · %s elsewhere · /goto:suggestion", suggestNoun(n))
		} else {
			m.flash = "no suggestions"
		}
		return
	}
	m.mode = modeSuggest
	m.suggestUUID = cur.uuid
	m.suggestSel = 0
}

// gotoSuggestion (/goto:suggestion or alt+g s) moves to the next node carrying a
// proposal and opens review on it — the way to reach one that arrived while you
// were reading somewhere else.
//
// The queue is GLOBAL, not what happens to be on screen: a proposal filed
// against a node in another part of the outline is still waiting for you, and
// "none in this view" was a dead end you could not act on from where you stood.
// So a target outside the loaded view reopens the outline at its parent — its
// parent, not the target itself, because review needs the target to BE a row and
// a view root is not one.
//
// Repeated presses walk the queue from wherever the last one left you, wrapping,
// so the chord triages everything pending without ever going back to the finder.
// A proposal whose node was deleted meanwhile cannot be reviewed at all — the
// walk settles it (cleanStaleSuggest) and moves on, so the queue does not keep
// tripping on the same ghost.
func (m *Model) gotoSuggestion() {
	total := m.pendingSuggestCount()
	if total == 0 {
		m.flash = "no suggestions"
		return
	}
	order := m.suggestOrder
	if len(order) == 0 {
		m.flash = "no suggestions"
		return
	}

	// resume after whatever the queue last put us on: the node under review, else
	// the cursor's node if it happens to carry one, else the head of the queue
	start := -1
	from := m.suggestUUID
	if from == "" {
		if cur := m.cursorItem(); cur != nil {
			from = cur.uuid
		}
	}
	for i, uuid := range order {
		if uuid == from {
			start = i
			break
		}
	}

	cleaned := 0
	for off := 1; off <= len(order); off++ {
		target := order[(start+off+len(order))%len(order)]
		if len(m.suggestsFor(target)) == 0 {
			continue // settled underneath us between load and now
		}
		if m.openSuggestOn(target) {
			if cleaned > 0 {
				m.flash = fmt.Sprintf("%s cleaned · reviewing", suggestNoun(cleaned))
			}
			return
		}
		cleaned += m.cleanStaleSuggest(target)
	}
	if cleaned > 0 {
		m.flash = fmt.Sprintf("%s cleaned · no reviewable suggestions", suggestNoun(cleaned))
		return
	}
	m.flash = fmt.Sprintf("%s · target missing", suggestNoun(total))
}

// cleanStaleSuggest settles the pending proposals about a target whose node is
// gone — deleted or expunged — so the walk never dead-ends on the same ghost
// twice. A target that is merely off-screen is left alone: only a node that no
// longer exists makes its proposals unreviewable. Returns how many were settled.
func (m *Model) cleanStaleSuggest(target string) int {
	if m.db == nil {
		return 0
	}
	n, err := database.GetNode(m.db, target)
	if err == nil && !n.Deleted {
		return 0 // the node is alive; openSuggestOn failed for another reason
	}
	settled := 0
	for _, s := range m.suggestsFor(target) {
		if database.RejectSuggestion(m.db, s) == nil {
			settled++
		}
	}
	if settled > 0 {
		m.loadSuggests()
	}
	return settled
}

// openSuggestOn puts the cursor on target and starts review, reopening the
// outline around it when it is not a row in the current view. It reports whether
// the target could be reached at all — a suggestion can outlive the node it is
// about, and that one is skipped rather than dead-ending the walk.
func (m *Model) openSuggestOn(target string) bool {
	if i := m.rowIndexOfUUID(target); i >= 0 {
		m.cursor = i
		m.caret = 0
		m.enterSuggestOn(target)
		return true
	}
	if m.db == nil {
		return false
	}
	n, err := database.GetNode(m.db, target)
	if err != nil {
		return false
	}
	// a top-level node has no parent to open under, so the forest root is the
	// view that makes it a row
	parent := n.ParentUUID
	if parent == "" {
		parent = database.RootUUID
	}
	m.reopenAt(parent, target)
	if m.rowIndexOfUUID(target) < 0 {
		return false
	}
	m.enterSuggestOn(target)
	return true
}

// enterSuggestOn opens review on a node the cursor already sits on.
func (m *Model) enterSuggestOn(target string) {
	m.mode = modeSuggest
	m.suggestUUID = target
	m.suggestSel = 0
}

// rowIndexOfUUID finds the visible row for a uuid, or -1 when it is not on
// screen. A mirror can put the same node on several rows; the first is as good
// as any, since review hangs off the node, not the placement.
func (m *Model) rowIndexOfUUID(uuid string) int {
	if uuid == "" {
		return -1
	}
	for i, r := range m.rows {
		if r.it != nil && r.it.uuid == uuid {
			return i
		}
	}
	return -1
}

// handleSuggestKey drives review mode: y/enter approves, n/x rejects, up/down
// walks a node's queue, and esc leaves the proposal pending.
func (m *Model) handleSuggestKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "y", "enter":
		m.reviewVerdict(true)
	case "n", "x":
		m.reviewVerdict(false)
	case "up", "shift+tab":
		m.suggestSel--
		m.clampSuggestSel()
	case "down", "tab":
		m.suggestSel++
		m.clampSuggestSel()
	case "alt+g":
		// review mode owns the keyboard, so the goto leader has to be armed from
		// inside it too — otherwise walking the queue means esc before every hop
		m.gotoPending = true
		m.flash = "goto · g node · s suggestion · esc cancel"
	case "esc", "alt+v", "q":
		m.mode = modeOutline
		m.suggestUUID = ""
		m.suggestSel = 0
		m.flash = "left pending"
	}
	return m, nil
}

// reviewVerdict settles the suggestion under the review cursor. Local edits are
// flushed first so an approval cannot be overwritten by a later flush of stale
// text, and the trees reload afterwards so the applied change is on screen.
func (m *Model) reviewVerdict(approve bool) {
	list := m.suggestsFor(m.suggestUUID)
	if m.suggestSel >= len(list) || m.db == nil {
		return
	}
	s := list[m.suggestSel]

	if _, err := m.saveAll(); err != nil {
		m.errorFlash("sync: " + err.Error())
		return
	}
	m.unsaved = false

	if approve {
		if drifted, err := database.TargetDrifted(m.db, s); err == nil && drifted {
			// the node moved on since the proposal was written: applying now
			// would silently drop somebody's newer text
			m.errorFlash("node changed since suggested · lflow suggest approve --force")
			return
		}
		if _, err := database.ApplySuggestion(m.db, s); err != nil {
			m.errorFlash("suggest: " + err.Error())
			return
		}
		m.flash = "approved · " + suggestGist(s)
	} else {
		if err := database.RejectSuggestion(m.db, s); err != nil {
			m.errorFlash("suggest: " + err.Error())
			return
		}
		m.flash = "rejected · " + suggestGist(s)
	}

	m.loadSuggests()
	m.resync() // an approval changed the tree; a rejection leaves it identical
	m.clampSuggestSel()
}

// suggestInline is how a pending proposal reads on the node itself: a yellow
// arrow and the text it proposes, hanging off the node's own row. The node
// still shows its CURRENT text — the arrow is what somebody wants it to become,
// not what it is.
//
// Every row-shaped node type gets this for free (bullets, todo, headings,
// quote, log, json, math, query, table, wf, image, the nlpcompute prose face):
// it is a suffix on the rendered line, like the mirror/locked/child-count one.
// Types whose row is REPLACED by a block — the Code node and the nlpcompute
// code face — have no line to hang it on, so they take the same text as one
// band line instead (see suggestBlockLines).
func (m *Model) suggestInline(it *item) string {
	list := m.suggestsFor(it.uuid)
	if len(list) == 0 {
		return ""
	}
	// while review is open the expanded band carries the proposal in full;
	// repeating it on the row would just be noise
	if m.mode == modeSuggest && m.suggestUUID == it.uuid {
		return " " + cYellow + "→ reviewing" + cReset
	}
	out := " " + cYellow + "→ " + suggestGist(list[0]) + cReset
	if n := len(list) - 1; n > 0 {
		out += cDim + fmt.Sprintf(" · +%d", n) + cReset
	}
	return out
}

// suggestGlyph turns a node carrying a proposal into a yellow outline circle:
// the marker belongs ON the node that needs review, not on a line under it, and
// it survives a row whose arrow text is clipped. The cursor and multi-select red
// still win — those say where you are, not what needs answering.
func (m *Model) suggestGlyph(it *item, glyph, color string) (string, string) {
	if len(m.suggestsFor(it.uuid)) == 0 {
		return glyph, color
	}
	return glyphSuggest, cYellow
}

// suggestBandLines is the review surface under a node: nothing while the
// proposal only shows inline, the expanded before/after once alt+v opens
// review on this node.
func (m *Model) suggestBandLines(r row, subtreeBelow bool, maxLine int) []string {
	if m.mode != modeSuggest || m.suggestUUID != r.it.uuid {
		return nil
	}
	list := m.suggestsFor(r.it.uuid)
	if m.suggestSel >= len(list) {
		return nil
	}
	rail := continuationPrefix(r, subtreeBelow)
	return m.suggestReviewLines(rail, list[m.suggestSel], m.suggestSel, len(list), maxLine)
}

// suggestBlockLines is suggestInline for the types whose row is replaced by a
// block (Code, the nlpcompute code face): the same yellow arrow, as a line
// hanging under the block, plus the review band when review is open on it.
func (m *Model) suggestBlockLines(r row, subtreeBelow bool, maxLine int) []string {
	if len(m.suggestsFor(r.it.uuid)) == 0 {
		return nil
	}
	rail := continuationPrefix(r, subtreeBelow)
	var lines []string
	if inline := m.suggestInline(r.it); inline != "" {
		lines = append(lines, clip(rail+cReset+strings.TrimPrefix(inline, " "), maxLine))
	}
	return append(lines, m.suggestBandLines(r, subtreeBelow, maxLine)...)
}

// suggestReviewLines is the expanded proposal: who and why, the before/after,
// and the keys that settle it. It sits at the node's own column — the node's
// yellow circle already says which node this belongs to, so indenting the
// proposal under it would only push the before/after away from the text it is
// comparing against.
func (m *Model) suggestReviewLines(rail string, s database.Suggestion, idx, total, maxLine int) []string {
	head := cDim + suggestBy(s)
	if total > 1 {
		head += fmt.Sprintf(" · %d/%d", idx+1, total)
	}
	// A completion proposal has no replacement text to announce. Name the state
	// action before the optional comment so a long rationale cannot clip away the
	// one fact the reviewer needs to decide what y will do.
	if s.Kind == database.SuggestComplete || s.Kind == database.SuggestUncomplete {
		head += " · " + cYellow + suggestGist(s) + cReset + cDim
	}
	if s.Message != "" {
		head += " · " + cItalic + strings.TrimSpace(s.Message) + cReset + cDim
	}
	lines := []string{clip(rail+cReset+head+cReset, maxLine)}

	for _, d := range m.suggestDiff(s) {
		mark, color := "+", cGreen
		if d.remove {
			mark, color = "-", cRed
		}
		lines = append(lines, clip(rail+cReset+color+mark+" "+cReset+cDim+d.text+cReset, maxLine))
	}

	approve := "approve"
	if s.Kind == database.SuggestComplete {
		approve = "approve (marks complete)"
	} else if s.Kind == database.SuggestUncomplete {
		approve = "approve (reopens node)"
	}
	keys := cGreen + "y" + cDim + " " + approve + " · " + cRed + "n" + cDim + " reject"
	if total > 1 {
		keys += " · ↑↓ next"
	}
	keys += " · esc later"
	return append(lines, clip(rail+cReset+cDim+keys+cReset, maxLine))
}

// suggestLine is one rendered side of a proposal.
type suggestLine struct {
	text   string
	remove bool
}

// suggestDiff turns a proposal into the lines review shows: an add is the node
// it would create, an edit is each proposed field before and after.
func (m *Model) suggestDiff(s database.Suggestion) []suggestLine {
	if s.Kind == database.SuggestComplete {
		return []suggestLine{{text: "state: open", remove: true}, {text: "state: complete"}}
	}
	if s.Kind == database.SuggestUncomplete {
		return []suggestLine{{text: "state: complete", remove: true}, {text: "state: open"}}
	}
	if s.Kind == database.SuggestAdd {
		out := []suggestLine{{text: m.suggestText(s.Name)}}
		if s.Note != "" {
			out = append(out, suggestLine{text: "note: " + s.Note})
		}
		return out
	}

	var cur database.Node
	if m.db != nil {
		cur, _ = database.GetNode(m.db, s.TargetUUID)
	}
	var out []suggestLine
	pair := func(before, after string) {
		out = append(out,
			suggestLine{text: m.suggestText(before), remove: true},
			suggestLine{text: m.suggestText(after)})
	}
	if s.Proposes(database.FieldName) {
		pair(cur.Name, s.Name)
	}
	if s.Proposes(database.FieldNote) {
		pair("note: "+cur.Note, "note: "+s.Note)
	}
	if s.Proposes(database.FieldType) {
		pair("type: "+cur.Type, "type: "+s.Type)
	}
	return out
}

// suggestText renders stored text for display: chip anchors resolve, control
// bytes go, and the result is a single line.
func (m *Model) suggestText(text string) string {
	text = stripControlBytes(displayAnchors(text, m.chips))
	text = strings.ReplaceAll(text, "\n", " ")
	if strings.TrimSpace(text) == "" {
		return "(empty)"
	}
	return strings.TrimSpace(text)
}

// suggestGist is the one-line summary of what a proposal would do.
func suggestGist(s database.Suggestion) string {
	switch {
	case s.Kind == database.SuggestAdd:
		return "+ " + suggestBrief(s.Name)
	case s.Kind == database.SuggestComplete:
		return "mark complete"
	case s.Kind == database.SuggestUncomplete:
		return "reopen"
	case s.Proposes(database.FieldName):
		return suggestBrief(s.Name)
	case s.Proposes(database.FieldNote):
		return "note: " + suggestBrief(s.Note)
	case s.Proposes(database.FieldType):
		return "type: " + s.Type
	}
	return "a change"
}

// suggestBy names the author, or the proposal itself when nobody signed it.
func suggestBy(s database.Suggestion) string {
	if s.Author == "" {
		return "suggested"
	}
	return "suggested by " + s.Author
}

func suggestNoun(n int) string {
	if n == 1 {
		return "1 suggestion"
	}
	return fmt.Sprintf("%d suggestions", n)
}

// suggestBrief flattens proposed text to a short single line for a status line
// or a summary.
func suggestBrief(s string) string {
	return clipStr(strings.TrimSpace(oneLine(s)), 48)
}
