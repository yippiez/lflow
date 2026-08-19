package editor

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/lflow/lflow/tui/database"
)

// The editor's review surface for suggestions (see tui/cli/suggest and
// database/suggestion.go). A pending suggestion is a proposal somebody — a
// teammate, an agent, another terminal — parked against the outline: it has
// changed nothing, and the tree reads exactly as it did before it arrived.
//
// A proposal is shown AS THE CHANGE IT PROPOSES, in the place that change would
// land, never as a band of prose under a node:
//
//	add     a yellow ghost row where the node would go
//	remove  the node itself, yellow with a red shine sliding through it
//	change  the node's own text, diffed in place: what goes is muted gray and
//	        struck through, what arrives is yellow
//
// Every one of them carries the same verdict prompt — · add ⌥y/⌥n — and ⌥y/⌥n
// on that row settles it. ⌥v walks to the next proposal anywhere in the outline.
//
// WARNING (invariant): a ghost row is a READING of the suggestions table, never
// a node. It is injected into m.rows AFTER the tree is flattened (see
// injectSuggestRows) and never enters the tree, so nothing saves, syncs, copies
// or exports it. Approving is the only path that writes to the outline, and it
// goes through database.ApplySuggestion.

// ghostPrefix marks the synthetic uuid of a ghost row's item, so a stray lookup
// against the real tree can never collide with it.
const ghostPrefix = "suggest:"

// loadSuggests refreshes the pending review queue, grouped by the node each
// suggestion is about. Cheap (the queue is short) and safe to call on any
// change; with no database the editor simply has no review surface.
func (m *Model) loadSuggests() {
	if m.db == nil {
		m.suggests, m.suggestOrder, m.suggestByID = nil, nil, nil
		return
	}
	list, err := database.ListSuggestions(m.db, database.SuggestionFilter{Status: database.SuggestPending})
	if err != nil {
		return // a failed read leaves the previous queue; the next event retries
	}
	byTarget := map[string][]database.Suggestion{}
	byID := map[string]database.Suggestion{}
	// The map answers "what is pending on this node"; the order answers "which
	// node next". ListSuggestions orders by (created_on, uuid), so the queue walks
	// oldest-first and the same way on every machine — a map alone loses that.
	var order []string
	for _, s := range list {
		if _, seen := byTarget[s.TargetUUID]; !seen {
			order = append(order, s.TargetUUID)
		}
		byTarget[s.TargetUUID] = append(byTarget[s.TargetUUID], s)
		byID[s.UUID] = s
	}
	m.suggests, m.suggestOrder, m.suggestByID = byTarget, order, byID
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

// rowSuggest is the proposal a row answers for, and how many more are queued
// behind it on the same row.
//
// A ghost row answers for its own add. A real node answers for the proposals
// about ITSELF — never for the adds filed against it, because those hang under
// it as ghost rows of their own and are answered there.
func (m *Model) rowSuggest(it *item) (database.Suggestion, int, bool) {
	if it == nil {
		return database.Suggestion{}, 0, false
	}
	if it.ghost != "" {
		s, ok := m.suggestByID[it.ghost]
		return s, 0, ok
	}
	var first database.Suggestion
	found, extra := false, 0
	for _, s := range m.suggestsFor(it.uuid) {
		if s.Kind == database.SuggestAdd {
			continue
		}
		if !found {
			first, found = s, true
			continue
		}
		extra++
	}
	return first, extra, found
}

// removeProposed reports whether the row is going with a proposed removal — the
// one state that animates (see suggestShine).
//
// A removal takes the subtree, so every row under the target shines too: a
// removal that lit only its own row would understate, every time, exactly how
// much ⌥y is about to take. Only the target itself carries the prompt.
func (m *Model) removeProposed(it *item) bool {
	for n := it; n != nil; n = n.parent {
		if s, _, ok := m.rowSuggest(n); ok && s.Kind == database.SuggestRemove {
			return true
		}
	}
	return false
}

// anyRemoveProposed keeps the animation tick alive while a removal is on
// screen: the shine IS the proposal's presence, so it has to keep moving.
func (m *Model) anyRemoveProposed() bool {
	for _, r := range m.rows {
		if m.removeProposed(r.it) {
			return true
		}
	}
	return false
}

// ── ghost rows ──────────────────────────────────────────────────────────────

// injectSuggestRows splices a yellow ghost row into the flattened outline for
// every pending add: the node as it would be, sitting where it would sit. It
// runs after tree.visibleRows, on the row slice alone, so the tree never learns
// about it.
//
// A proposal under a collapsed or off-screen parent gets no row — there is no
// place to put it — and stays reachable through ⌥v and the status-bar tally.
func (m *Model) injectSuggestRows(rows []row) []row {
	if len(m.suggests) == 0 {
		return rows
	}
	for _, target := range m.suggestOrder {
		for _, s := range m.suggests[target] {
			if s.Kind != database.SuggestAdd {
				continue
			}
			rows = m.spliceGhost(rows, s)
		}
	}
	return rows
}

// spliceGhost inserts one add proposal's row among the parent's children, at
// the end unless the proposal pinned itself to the top.
func (m *Model) spliceGhost(rows []row, s database.Suggestion) []row {
	parent := s.TargetUUID
	if parent == "" {
		parent = database.RootUUID
	}

	// depth-0: the proposal is about the view root itself, so its ghost is a
	// sibling of the top-level rows rather than a child of any of them
	at, depth, branch := len(rows), 0, []bool(nil)
	if root := m.viewRoot(); root == nil || root.uuid != parent {
		p := m.rowIndexOfUUID(parent)
		if p < 0 {
			return rows // the parent is not on screen: nothing to hang it under
		}
		at, depth = p+1, rows[p].depth+1
		branch = append(append([]bool(nil), rows[p].branch...), !rows[p].last)
		// walk past the parent's whole subtree unless the proposal lands on top
		if s.Position != database.PositionTop {
			for at < len(rows) && rows[at].depth >= depth {
				at++
			}
		}
	} else if s.Position != database.PositionTop {
		at = len(rows)
	} else {
		at = 0
	}

	g := row{it: m.ghostItem(s), depth: depth, branch: branch, last: true}
	// the ghost takes over as the last child, so the sibling it displaces — and
	// that sibling's whole subtree — has to grow the rail that now runs past it
	if at > 0 && at-1 < len(rows) && rows[at-1].depth >= depth {
		for i := at - 1; i >= 0 && rows[i].depth >= depth; i-- {
			if rows[i].depth == depth {
				rows[i].last = false
			} else if len(rows[i].branch) > depth {
				rows[i].branch[depth] = true
			}
		}
	}
	if at < len(rows) && rows[at].depth == depth {
		g.last = false // a real sibling still follows: only a top-pinned ghost
	}

	out := make([]row, 0, len(rows)+1)
	out = append(out, rows[:at]...)
	out = append(out, g)
	return append(out, rows[at:]...)
}

// ghostItem is the stand-in node a ghost row renders: real enough to lay out,
// marked so that every path that edits, moves or saves the outline refuses it.
func (m *Model) ghostItem(s database.Suggestion) *item {
	typ := s.Type
	if typ == "" {
		typ = database.TypeBullets
	}
	return &item{
		uuid:            ghostPrefix + s.UUID,
		name:            s.Name,
		note:            s.Note,
		typ:             typ,
		ghost:           s.UUID,
		readonly:        true,
		structureLocked: true,
	}
}

// rowIndexOfUUID finds the visible row for a uuid, or -1 when it is not on
// screen. A mirror can put the same node on several rows; the first is as good
// as any, since a proposal hangs off the node, not the placement.
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

// rowIndexOfSuggest finds the row that answers for a proposal: its ghost row
// for an add, the node's own row otherwise.
func (m *Model) rowIndexOfSuggest(s database.Suggestion) int {
	if s.Kind != database.SuggestAdd {
		return m.rowIndexOfUUID(s.TargetUUID)
	}
	for i, r := range m.rows {
		if r.it != nil && r.it.ghost == s.UUID {
			return i
		}
	}
	return -1
}

// ── rendering ───────────────────────────────────────────────────────────────

// ghostRowLines renders an add proposal's row: the node it would be, in yellow,
// with the verdict prompt after it. Under the cursor the glyph goes red like
// any other row — there is no caret, because there is nothing here to type in.
func (m *Model) ghostRowLines(r row, o rowOpts) (group, bands []string) {
	it := r.it
	s, _, ok := m.rowSuggest(it)
	if !ok {
		return nil, nil
	}
	glyphColor := cYellow
	if o.redGlyph {
		glyphColor = cRed
	}
	text := m.suggestText(it.name)
	line := " " + cDim + connector(r) + glyphColor + glyphSuggest + cReset + " " +
		cYellow + text + cReset + m.suggestPrompt(s, 0)
	if o.interactive && m.mode == modeFlash {
		line = cDim + stripSGR(line) + cReset
	}
	group = wrapLine(line, o.maxLine, continuationPrefix(r, false))
	if it.note != "" {
		rail := continuationPrefix(r, false)
		bands = append(bands, clip(rail+cReset+cYellow+m.suggestText(it.note)+cReset, o.maxLine))
	}
	return group, bands
}

// suggestPrompt is the verdict every pending row carries: what would happen,
// and the two keys that settle it. It is on EVERY proposal, not just the one
// under the cursor — a row that cannot be answered where it stands is a row you
// have to go looking for.
func (m *Model) suggestPrompt(s database.Suggestion, extra int) string {
	label, color := "", cYellow
	switch s.Kind {
	case database.SuggestAdd:
		label = "add"
	case database.SuggestRemove:
		label, color = "remove", cRed
	}
	out := cDim + " · "
	if label != "" {
		out += color + label + cDim + " "
	}
	out += "⌥y/⌥n"
	if extra > 0 {
		out += fmt.Sprintf(" · +%d", extra)
	}
	return out + cReset
}

// suggestSuffix is what a pending proposal adds to an ordinary node's row: the
// fields a change proposes that its text cannot show (note, type, done state),
// then the verdict prompt. The proposed TEXT is not here — it is diffed into
// the row's own body (see suggestDiffBody).
func (m *Model) suggestSuffix(it *item) string {
	s, extra, ok := m.rowSuggest(it)
	if !ok {
		return ""
	}
	return m.suggestFields(s) + m.suggestPrompt(s, extra)
}

// suggestFields renders the proposed fields that have no place on the row's own
// text: each as before → after, before muted and struck through, after yellow.
func (m *Model) suggestFields(s database.Suggestion) string {
	if s.Kind != database.SuggestChange {
		return ""
	}
	var cur database.Node
	if m.db != nil {
		cur, _ = database.GetNode(m.db, s.TargetUUID)
	}
	var out string
	pair := func(label, before, after string) {
		out += cDim + " · " + label + " "
		if strings.TrimSpace(before) != "" {
			out += cDim + cStrike + suggestBrief(before) + cReset + cDim + " "
		}
		out += cYellow + suggestBrief(after) + cReset
	}
	if s.Proposes(database.FieldNote) {
		pair("note", m.suggestText(cur.Note), m.suggestText(s.Note))
	}
	if s.Proposes(database.FieldType) {
		pair("type", cur.Type, s.Type)
	}
	if done, ok := s.ProposesDone(); ok {
		state := "reopen"
		if done {
			state = "complete"
		}
		out += cDim + " · " + cYellow + state + cReset
	}
	return out
}

// suggestBlockLines is the row prompt for the faces that have no row to hang it
// on — a code block, the nlpcompute code face, an empty node, a divider. The
// same proposal, as one line under the block.
func (m *Model) suggestBlockLines(r row, subtreeBelow bool, maxLine int) []string {
	s, extra, ok := m.rowSuggest(r.it)
	if !ok {
		return nil
	}
	rail := continuationPrefix(r, subtreeBelow)
	line := rail + cReset
	if s.Kind == database.SuggestChange && s.Proposes(database.FieldName) {
		line += m.suggestDiffBody(m.nodeText(r.it), s.Name, -1)
	}
	line += strings.TrimPrefix(m.suggestFields(s)+m.suggestPrompt(s, extra), cDim+" ")
	if s.Kind == database.SuggestRemove {
		line = rail + cReset + suggestShine(cDim+" remove"+cReset+m.suggestPrompt(s, extra))
	}
	return []string{clip(line, maxLine)}
}

// suggestGlyph turns a node carrying a proposal into a yellow circle: the mark
// belongs ON the row that needs answering. The cursor and multi-select red still
// win — those say where you are, not what needs answering.
func (m *Model) suggestGlyph(it *item, glyph, color string) (string, string) {
	if _, _, ok := m.rowSuggest(it); !ok {
		return glyph, color
	}
	return glyphSuggest, cYellow
}

// suggestBody paints the row's own text with whatever the proposal on it says:
// a removal shines yellow-through-red, a change diffs in place. Anything else
// (an add's ghost row handles itself) leaves the body alone.
func (m *Model) suggestBody(it *item, body string, caret int) string {
	if m.removeProposed(it) {
		return suggestShine(body)
	}
	s, _, ok := m.rowSuggest(it)
	if ok && s.Kind == database.SuggestChange && s.Proposes(database.FieldName) {
		return m.suggestDiffBody(m.nodeText(it), s.Name, caret)
	}
	return body
}

// suggestDiffBody renders a proposed text change in place, word by word: what
// stays is untouched, what goes is muted gray struck through, what arrives is
// yellow. Only the changed words carry any marking — a row where every word is
// painted says nothing about which word actually changed.
//
// The caret indexes the node's CURRENT text, which survives the diff whole (as
// the kept and removed words, in order), so the row still shows where the next
// keystroke lands: the proposal is on the row, not in charge of it.
func (m *Model) suggestDiffBody(before, after string, caret int) string {
	before, after = m.suggestText(before), m.suggestText(after)
	var b strings.Builder
	idx := 0    // runes of `before` emitted so far
	prev := " " // the last character written, so replaced words never fuse
	parts := wordDiff(before, after)
	for _, d := range parts {
		if d.text != "" && !strings.HasSuffix(prev, " ") && !strings.HasPrefix(d.text, " ") {
			b.WriteString(" ")
		}
		prev = d.text
		if d.op == diffAdd {
			b.WriteString(cYellow + d.text + cReset)
			continue
		}
		open, closing := cFG, cReset
		if d.op == diffDel {
			open = cDim + cStrike
		}
		runes := []rune(d.text)
		b.WriteString(open)
		for i, c := range runes {
			if idx+i == caret {
				b.WriteString(cInvert + string(c) + cReset + open)
				continue
			}
			b.WriteRune(c)
		}
		b.WriteString(closing)
		idx += len(runes)
	}
	if caret >= idx {
		b.WriteString(cInvert + " " + cReset) // the caret past the last character
	}
	return b.String()
}

// nodeText is a node's current text as the diff reads it, chips resolved.
func (m *Model) nodeText(it *item) string {
	if it == nil {
		return ""
	}
	return it.name
}

// suggestShine paints a row proposed for removal: it keeps the outline's yellow
// "answer me", with a red highlight sliding through it — the house shine (see
// ShineText), saying the row is on its way out without taking it away yet.
func suggestShine(s string) string {
	return shineOver(s, [3]int{255, 215, 95}, [3]int{244, 71, 71}, 0.40)
}

// suggestText renders stored text for display: chip anchors resolve, control
// bytes go, and the result is a single line.
func (m *Model) suggestText(text string) string {
	text = stripControlBytes(displayAnchors(text, m.chips))
	return strings.ReplaceAll(text, "\n", " ")
}

// ── verdicts ────────────────────────────────────────────────────────────────

// answerSuggest settles the proposal on the cursor row: ⌥y applies it to the
// outline, ⌥n turns it down and leaves everything as it was. Local edits are
// flushed first so an approval cannot be overwritten by a later flush of stale
// text, and the trees reload afterwards so the applied change is on screen.
func (m *Model) answerSuggest(approve bool) {
	s, _, ok := m.rowSuggest(m.cursorItem())
	if !ok || m.db == nil {
		return
	}

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
	m.clampCursor()
}

// ghostPasses lists the keys that still mean something on a ghost row: moving
// off it, scrolling, walking the queue and leaving. Everything else would be
// aimed at a node that is not there yet.
func ghostPasses(key string) bool {
	switch key {
	case "up", "down", "left", "right", "home", "end", "pgup", "pgdown",
		"ctrl+d", "ctrl+u", "ctrl+c", "ctrl+q", "esc", "alt+v", "alt+y", "alt+n":
		return true
	}
	return false
}

// handleSuggestKey answers the proposal on the cursor row. It runs before the
// ordinary outline keys, so ⌥y here settles instead of yanking — the row says
// which it is doing, and only a row carrying a proposal is diverted.
func (m *Model) handleSuggestKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	m.answerSuggest(k.String() == "alt+y")
	return m, nil
}

// gotoSuggestion (⌥v or /goto:suggestion) moves to the next proposal anywhere in
// the outline and puts the cursor on the row that answers it.
//
// The queue is GLOBAL, not what happens to be on screen: a proposal filed
// against a node in another part of the outline is still waiting for you, and
// "none in this view" was a dead end you could not act on from where you stood.
// So a target outside the loaded view reopens the outline at its parent — its
// parent, not the target itself, because answering needs the target to BE a row
// and a view root is not one.
//
// Repeated presses walk the queue from wherever the last one left you, wrapping,
// so the chord triages everything pending without ever going back to the finder.
// A proposal whose node was deleted meanwhile cannot be answered at all — the
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

	// resume after whatever the queue last put us on: the proposal under the
	// cursor, else the cursor's own node, else the head of the queue
	start := -1
	from := ""
	if cur := m.cursorItem(); cur != nil {
		from = cur.uuid
		if cur.ghost != "" {
			from = m.suggestByID[cur.ghost].TargetUUID
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
				m.flash = fmt.Sprintf("%s cleaned · %s", suggestNoun(cleaned), m.flash)
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
// longer exists makes its proposals unanswerable. Returns how many were settled.
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
		m.refreshRows()
	}
	return settled
}

// openSuggestOn puts the cursor on the row that answers the first proposal
// about target, reopening the outline around it when it is not on screen. It
// reports whether the row could be reached at all — a suggestion can outlive
// the node it is about, and that one is skipped rather than dead-ending the walk.
func (m *Model) openSuggestOn(target string) bool {
	list := m.suggestsFor(target)
	if len(list) == 0 {
		return false
	}
	s := list[0]
	if m.landOnSuggest(s) {
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
	if target == database.RootUUID {
		parent = database.RootUUID
	}
	m.reopenAt(parent, target)
	return m.landOnSuggest(s)
}

// landOnSuggest puts the cursor on the proposal's row when it is on screen.
func (m *Model) landOnSuggest(s database.Suggestion) bool {
	i := m.rowIndexOfSuggest(s)
	if i < 0 {
		return false
	}
	m.cursor = i
	m.caret = 0
	m.clampCaret()
	m.flash = suggestBy(s) + " · " + suggestGist(s)
	if s.Message != "" {
		m.flash += " · " + strings.TrimSpace(oneLine(s.Message))
	}
	return true
}

// suggestGist is the one-line summary of what a proposal would do.
func suggestGist(s database.Suggestion) string {
	switch {
	case s.Kind == database.SuggestAdd:
		return "add " + suggestBrief(s.Name)
	case s.Kind == database.SuggestRemove:
		return "remove " + suggestBrief(s.BaseName)
	case s.Proposes(database.FieldName):
		return suggestBrief(s.Name)
	case s.Proposes(database.FieldNote):
		return "note: " + suggestBrief(s.Note)
	case s.Proposes(database.FieldType):
		return "type: " + s.Type
	}
	if done, ok := s.ProposesDone(); ok {
		if done {
			return "mark complete"
		}
		return "reopen"
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
