package editor

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/lflow/lflow/pkg/tui/database"
)

func (m *Model) handleKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := k.String()
	m.flash = "" // one-shot: whatever this key does sets the next status

	// page keys pin the viewport; every other key leaves pin mode. Cursor-follow
	// then prefers the last window (see viewWindow) so typing after a page does
	// not yank the view back.
	if key != "pgup" && key != "pgdown" {
		m.scrolling = false
	}

	// esc-esc quits from outline mode — but not while a focused inline view is up
	// (there esc defocuses; handled in the focused block below)
	if m.mode == modeOutline && key == "esc" && !m.focused {
		if m.selOn {
			m.clearSel() // first esc releases the multi-selection
			return m, nil
		}
		if m.escPending {
			return m.quit()
		}
		m.escPending = true
		return m, nil
	}
	if key != "esc" {
		m.escPending = false
	}

	switch m.mode {
	case modeSlash, modeType, modeStyle, modeTheme, modeComplete, modeTagColor:
		return m.handleListMode(k, m.listSource())
	case modeFinder:
		return m.finder.handleKey(m, k, nodeFinderBackend{})
	case modeLinkEdit:
		return m.handleLinkEditKey(k)
	case modeNote:
		return m.handleNoteKey(k)
	case modePaint:
		return m.handlePaintKey(k)
	case modeConfirm:
		return m.handleConfirmKey(k)
	case modeSettings:
		return m.handleSettingsKey(k)
	case modeFlash:
		return m.handleFlashKey(k)
	}

	// A focused inline node view captures input first (it stays inside the outline,
	// so we're still modeOutline). The view handles its own keys; esc or alt+e
	// defocuses (flushing edits); ctrl+c/ctrl+q fall through to quit; everything
	// else is swallowed so it can't leak into outline navigation.
	if m.focused && m.mode == modeOutline {
		cur := m.cursorItem()
		if v := m.activeView(cur); v != nil {
			if cmd, handled := v.Key(m, cur, k); handled {
				return m, cmd
			}
			switch key {
			case "esc", "alt+e":
				v.Leave(m, cur)
				m.focused = false
				return m, nil
			case "ctrl+c", "ctrl+q":
				// fall through to the quit handler below
			default:
				return m, nil
			}
		} else {
			m.focused = false
		}
	}

	// snapshot the tree before a mutating outline key so /undo can reverse it
	m.snapshotForKey(key, k)

	// multi-select lifecycle: shift+arrows grow the selection; any other plain
	// movement, typing or esc drops it (structural ops below act on it instead)
	if m.mode == modeOutline {
		switch key {
		case "shift+up":
			m.startOrExtendSel()
			if m.cursor > 0 {
				m.cursor--
			}
			return m, nil
		case "shift+down":
			m.startOrExtendSel()
			if m.cursor < len(m.rows)-1 {
				m.cursor++
			}
			return m, nil
		}
		if m.selOn {
			switch key {
			case "tab", "shift+tab", "ctrl+d", "alt+d", "ctrl+shift+backspace",
				"alt+shift+up", "ctrl+shift+up", "ctrl+alt+up",
				"alt+shift+down", "ctrl+shift+down", "ctrl+alt+down",
				"/", "alt+P": // the slash menu may apply /type //style //move to the selection
			case "esc":
				m.clearSel()
				return m, nil
			default:
				m.clearSel()
			}
		}
	}

	switch key {
	case "pgdown", "pgup":
		// scroll the body half a viewport without moving the cursor — for reading a
		// long note/subtree that runs past the footer. Half-page keeps enough context
		// that consecutive pages stay oriented.
		step := m.viewRows / 2
		if step < 1 {
			step = 1
		}
		if !m.scrolling {
			m.scrolling = true
			m.scrollTop = m.viewTop // start from what is currently on screen
		}
		if key == "pgdown" {
			m.scrollTop += step
		} else {
			m.scrollTop -= step
			if m.scrollTop < 0 {
				m.scrollTop = 0
			}
		}
		return m, nil
	case "ctrl+q", "ctrl+c":
		return m.quit()
	case "ctrl+s":
		written, err := m.saveAll()
		if err != nil {
			m.err = err
			return m.quit()
		}
		m.saved.written += written
		m.unsaved = false
		return m, nil
	case "ctrl+z", "alt+z":
		// undo the last action (alt+z is the fallback where ctrl+z suspends)
		m.undo()
		return m, nil
	case "enter":
		cur := m.cursorItem()
		// commit a #tag or date token under the caret into a chip before splitting
		if cur != nil {
			m.chipifyBeforeCaret(cur)
		}
		// an untagged commit inside an active thread ships for consideration
		// (agentCmd) while Enter carries on normally. A fresh @mention does
		// NOT send here — alt+r starts the session deliberately, so Enter at
		// the end of (or anywhere in) a mention just edits text.
		agentCmd, consumed := m.mentionSendOnEnter(cur)
		if consumed {
			return m, agentCmd
		}
		mc := m.mirrorContext()
		// caret at the very start of a node that has text: don't split — keep the
		// node and its whole subtree intact and push it down, opening an empty node
		// above it with the cursor there.
		if cur != nil && mc.editable && m.caret == 0 && cur.name != "" {
			it, err := m.tree.insertSiblingBefore(cur)
			if err != nil {
				m.err = err
				return m.quit()
			}
			if cur != nil && typeOf(cur.typ).continueOnEnter {
				it.typ = cur.typ // keep the todo list going
			}
			m.unsaved = true
			m.refreshRows()
			m.cursor = m.findRow(it, mc.ctx)
			m.caret = 0
			return m, agentCmd
		}
		var it *item
		var err error
		// On an expanded node that already has children, the new node belongs
		// inside it as the first child — not as a sibling after the whole subtree.
		expandedParent := cur != nil && cur.mirrorOf == "" && !cur.collapsed && len(m.tree.childItems(cur)) > 0
		switch {
		case cur == nil:
			it, err = m.tree.insertFirstChild(m.viewRoot())
		case expandedParent:
			it, err = m.tree.insertFirstChild(cur)
		default:
			it, err = m.tree.insertSiblingAfter(cur)
		}
		if err != nil {
			m.err = err
			return m.quit()
		}
		if it != nil {
			// pressing Enter from a todo continues the todo list — the fresh node
			// is a todo too (unchecked, since completedAt defaults to 0).
			if cur != nil && typeOf(cur.typ).continueOnEnter {
				it.typ = cur.typ
			}
			// split the node at the caret: text after the caret moves into the new
			// sibling, the part before — and the node's children — stays. A mirror
			// reference, or a non-inline-editable type (json), is not split — it just
			// opens an empty sibling.
			if cur != nil && mc.editable && typeOf(cur.typ).inlineEditable {
				runes := []rune(cur.name)
				at := m.caret
				if at < 0 {
					at = 0
				}
				if at > len(runes) {
					at = len(runes)
				}
				it.name = string(runes[at:])
				cur.name = string(runes[:at])
			}
			m.unsaved = true
			m.refreshRows()
			m.cursor = m.findRow(it, mc.ctx)
			m.caret = 0
		}
		return m, agentCmd
	case "tab":
		// path chips are inserted via the /file fuzzy picker, and "#" is for tags,
		// so Tab is free to just indent. The Temporary Domain edits exactly like the
		// main outline, so indenting works there too.
		if m.selOn {
			m.selIndent()
			return m, nil
		}
		if cur := m.cursorItem(); cur != nil {
			mc := m.mirrorContext()
			if m.tree.indent(cur) {
				m.unsaved = true
				m.refreshRows()
				// follow the cursor into the mirror we indented under, if any
				ctx := mc.ctx
				if mc.indentInto != nil {
					ctx = mc.indentInto
				}
				m.cursor = m.findRow(cur, ctx)
			}
		}
		return m, nil
	case "shift+tab":
		if m.selOn {
			m.selOutdent()
			return m, nil
		}
		if cur := m.cursorItem(); cur != nil {
			mc := m.mirrorContext()
			if m.tree.outdent(cur, mc.localRoot) {
				m.unsaved = true
				m.refreshRows()
				m.cursor = m.findRow(cur, mc.ctx)
			}
		}
		return m, nil
	case "ctrl+@", "ctrl+space":
		if cur := m.cursorItem(); cur != nil && len(m.tree.childItems(cur)) > 0 {
			cur.collapsed = !cur.collapsed
			m.persistCollapsed(cur)
			m.refreshRows()
		}
		return m, nil
	case "ctrl+d", "alt+d", "ctrl+shift+backspace":
		// delete the whole node/selection (subtrees confirm inline first)
		if m.selOn {
			if m.selHasChildren() {
				m.mode = modeConfirm
			} else {
				m.selDelete()
			}
			return m, nil
		}
		if cur := m.cursorItem(); cur != nil {
			if len(cur.children) > 0 {
				// children go with the node: confirm inline first
				m.mode = modeConfirm
			} else {
				m.deleteNode(cur)
			}
		}
		return m, nil
	// Delete the word to the left (or the whole chip just before the caret),
	// mirroring ctrl+left. ctrl+backspace arrives as ctrl+h in most terminals;
	// ctrl+w is the reliable readline alias (ctrl+shift+backspace is NOT a separable
	// key — the terminal sends the same byte as ctrl+backspace, so node-delete is
	// ctrl+d, not a backspace combo).
	case "ctrl+backspace", "ctrl+h", "ctrl+w":
		cur := m.cursorItem()
		if cur == nil || cur.mirrorOf != "" || !typeOf(cur.typ).inlineEditable || cur.readonly {
			return m, nil
		}
		runes := []rune(cur.name)
		if m.boundCaret(len(runes)) == 0 {
			return m, nil
		}
		spans := anchorSpans(runes)
		// caret right after a chip → delete just that chip
		if sp := spanEndingAt(spans, m.caret); sp != nil {
			m.deleteChipID(sp.id)
			cur.name = string(runes[:sp.start]) + string(runes[sp.end:])
			m.caret = sp.start
			m.markCmdDraft(cur)
			m.unsaved = true
			return m, nil
		}
		target := prevWordBoundary(runes, m.caret)
		if sp := spanContaining(spans, target); sp != nil {
			target = sp.start // don't cut into a chip — take the whole thing
		}
		for _, sp := range spans { // drop chip records the deletion removes
			if sp.start >= target && sp.end <= m.caret {
				m.deleteChipID(sp.id)
			}
		}
		cur.name = string(runes[:target]) + string(runes[m.caret:])
		shiftSpans(cur.uuid, target, target-m.caret)
		m.persistSpans(cur.uuid)
		m.caret = target
		m.markCmdDraft(cur)
		m.unsaved = true
		return m, nil
	case "ctrl+t":
		// convert a time phrase under the cursor to canonical date text (the renderer
		// then chips it)
		if cur := m.cursorItem(); cur != nil && m.mirrorContext().editable {
			if d := detectDate(cur.name, m.caret, time.Now()); d != nil && d.phrase != d.canonical() {
				runes := []rune(cur.name)
				date := d.canonical()
				cur.name = string(runes[:d.start]) + date + string(runes[d.end:])
				m.caret = d.start + len([]rune(date))
				m.unsaved = true
			}
		}
		return m, nil
	// every alt+arrow chord has a ctrl twin: terminals like windows
	// terminal grab alt+arrows for pane focus and never deliver them
	case "alt+shift+up", "ctrl+shift+up", "ctrl+alt+up":
		if m.selOn {
			m.selMove(-1)
			return m, nil
		}
		cur := m.cursorItem()
		if cur == nil {
			return m, nil
		}
		// at the top of the Temporary Domain, alt+shift+up moves the node out into
		// the notes — crossing the divider as if the two regions were one space.
		if m.tempActive && cur.parent == m.tempTree.root && indexOf(cur) == 0 {
			m.crossToNotes(cur)
			return m, nil
		}
		mc := m.mirrorContext()
		if m.tree.move(cur, -1, m.viewRoot()) {
			m.unsaved = true
			m.refreshRows()
			m.cursor = m.findRow(cur, mc.ctx)
		}
		return m, nil
	case "alt+shift+down", "ctrl+shift+down", "ctrl+alt+down":
		if m.selOn {
			m.selMove(1)
			return m, nil
		}
		if cur := m.cursorItem(); cur != nil {
			mc := m.mirrorContext()
			if m.tree.move(cur, 1, m.viewRoot()) {
				m.unsaved = true
				m.refreshRows()
				m.cursor = m.findRow(cur, mc.ctx)
			}
		}
		return m, nil
	case "ctrl+right":
		// jump forward one word; at the end of the text, cross to the next node
		cur := m.cursorItem()
		if cur == nil {
			return m, nil
		}
		runes := []rune(cur.name)
		if m.caret >= len(runes) {
			if m.cursor < len(m.rows)-1 {
				m.cursor++
				m.caret = 0
			}
			return m, nil
		}
		m.caret = nextWordBoundary(runes, m.caret)
		if sp := spanContaining(anchorSpans(runes), m.caret); sp != nil {
			m.caret = sp.end // a chip is atomic
		}
		return m, nil
	case "ctrl+left":
		// jump back one word; at the start, cross to the previous node's end
		cur := m.cursorItem()
		if cur == nil {
			return m, nil
		}
		if m.caret <= 0 {
			if m.cursor > 0 {
				m.cursor--
				if c := m.cursorItem(); c != nil {
					m.caret = len([]rune(c.name))
				}
			}
			return m, nil
		}
		runes := []rune(cur.name)
		m.caret = prevWordBoundary(runes, m.caret)
		if sp := spanContaining(anchorSpans(runes), m.caret); sp != nil {
			m.caret = sp.start // a chip is atomic
		}
		return m, nil
	case "alt+right":
		// zoom into the cursor node — leaves too: the view starts empty
		// and typing adds the first child
		if cur := m.cursorItem(); cur != nil {
			// a mirror carries no children in memory; zoom into its source so the
			// original's children render — see mirrorContext, "zoom"
			if cur.mirrorOf != "" {
				src, ok := m.tree.byUUID[m.tree.sourceUUID(cur)]
				if !ok {
					return m, nil
				}
				cur = src
			}
			m.viewStack = append(m.viewStack, cur)
			m.cursor = 0
			m.caret = 0
			m.refreshRows()
		}
		return m, nil
	case "alt+left", "alt+backspace":
		// zoom back out
		if len(m.viewStack) > 1 {
			zoomed := m.viewRoot()
			m.viewStack = m.viewStack[:len(m.viewStack)-1]
			m.refreshRows()
			m.cursor = m.rowIndexOf(zoomed)
			m.caret = 0
		} else if base := m.viewStack[0]; base.uuid != "" && base.uuid != database.RootUUID {
			// at the loaded root: walk up to its parent in the forest, reloading the
			// tree there and focusing the node we came from
			if n, err := database.GetNode(m.db, base.uuid); err == nil && n.ParentUUID != "" {
				m.reopenAt(n.ParentUUID, base.uuid)
			}
		}
		return m, nil
	case "alt+g":
		// on a link chip, alt+g follows it (a node jumps, a URL opens in the
		// browser); off a chip it opens the /goto finder to jump to any node
		if cur := m.cursorItem(); cur != nil {
			if c, ok := m.linkChipAtCaret(cur); ok {
				return m.followLink(c)
			}
		}
		if !m.tempActive {
			m.openFinder(actGoto)
		}
		return m, nil
	case "alt+e":
		// toggle a type's inline expanded view (json/bash): alt+e focuses it,
		// alt+e again collapses. Else fall back to an action-only expand (voice play).
		if cur := m.cursorItem(); cur != nil {
			if v := nodeViewOf(cur); v != nil {
				if m.focused {
					v.Leave(m, cur)
					m.focused = false
				} else if v.Enter(m, cur) {
					m.focused = true
					m.focusScroll = 0
				}
			} else if c, ok := m.cmdChipAtCaret(cur); ok {
				m.focusCmdChip(c) // ⌥e on a cmd chip: its run output as an inline band
				return m, nil
			} else if c, ok := m.linkChipAtCaret(cur); ok {
				m.openLinkEdit(c) // ⌥e on a link chip edits its name + target
				return m, nil
			} else if word, ok := m.tagWordAtCaret(cur); ok {
				m.openTagColor(word) // ⌥e on a tag picks its pill color
				return m, nil
			} else if e := typeOf(cur.typ).expand; e != nil {
				return m, e(m, cur)
			} else if cmd := m.openPathChipCmd(cur); cmd != nil {
				return m, cmd // ⌥e on a node with a path chip opens the file in $EDITOR
			}
		}
		return m, nil
	case "alt+r":
		// run / re-run a runnable node's own action (bash/query/voice). Never
		// auto-runs.
		if cur := m.cursorItem(); cur != nil {
			if c, ok := m.cmdChipAtCaret(cur); ok {
				return m, m.runCmdChip(c) // an inline cmd chip runs on its own
			}
			// a node mentioning an agent starts (or re-sends) its thread session
			if ag, ok := m.mentionedAgent(expandAnchors(cur.name, m.chips)); ok {
				return m, m.sendThread(cur, ag)
			}
			if run := typeOf(cur.typ).run; run != nil {
				return m, run(m, cur)
			}
			// any pulled Workflowy mirror refreshes its own branch — the
			// recursive-mirror model: every pulled node is a wf handle too
			if _, ok := m.wfMap[cur.uuid]; ok {
				return m, runWF(m, cur)
			}
		}
		return m, nil
	case "alt+enter":
		// same as /complete: toggle done on the cursor node
		if cur := m.cursorItem(); cur != nil {
			m.pushUndo("")
			m.toggleComplete(cur)
		}
		return m, nil
	case "alt+P":
		// open the command palette without typing "/" into the node text
		// (alt+shift+p — terminals deliver shift as uppercase P)
		cur := m.cursorItem()
		if cur == nil {
			it, err := m.tree.insertFirstChild(m.viewRoot())
			if err != nil {
				m.err = err
				return m.quit()
			}
			m.refreshRows()
			m.cursor = m.rowIndexOf(it)
			m.caret = 0
		}
		m.openSlashMenu(false)
		return m, nil
	case "alt+x":
		// stop a running command, keeping what was captured; when nothing is
		// running, clear the output band. A cmd chip under the caret takes the
		// key (its band is keyed by chip id); otherwise the node's own band.
		if cur := m.cursorItem(); cur != nil {
			id := cur.uuid
			if c, ok := m.cmdChipAtCaret(cur); ok {
				id = c.ID
			}
			r := m.run(id)
			if r != nil && r.cancel != nil {
				r.cancel()
				m.finishRun(id)
			} else if r != nil && len(r.out) > 0 {
				r.out = nil
				r.dropped = 0
				r.pwd = ""
				m.persistRunOut(id) // an empty band deletes the row
				m.setCmdPreview(id)
			}
		}
		return m, nil
	case "alt+s":
		// flash: label every visible row's actions (jump / run / expand / fold) and
		// hand off to modeFlash so the next keystrokes pick one — act on a node
		// elsewhere on screen without moving the cursor there. See flash.go.
		m.enterFlash()
		return m, nil
	case "alt+up", "ctrl+up":
		// collapse the cursor node
		if cur := m.cursorItem(); cur != nil && len(m.tree.childItems(cur)) > 0 && !cur.collapsed {
			ctx := m.mirrorContext().ctx
			cur.collapsed = true
			m.persistCollapsed(cur)
			m.refreshRows()
			m.cursor = m.findRow(cur, ctx)
		}
		return m, nil
	case "alt+down", "ctrl+down":
		// expand the cursor node
		if cur := m.cursorItem(); cur != nil && len(m.tree.childItems(cur)) > 0 && cur.collapsed {
			ctx := m.mirrorContext().ctx
			cur.collapsed = false
			m.persistCollapsed(cur)
			m.refreshRows()
			m.cursor = m.findRow(cur, ctx)
		}
		return m, nil
	case "up":
		starts := m.selectedVisualRows()
		line := caretVisualLine(starts, m.caret)
		if line > 0 {
			// walk up one visual line of the wrapped node first
			goal := m.caretColumn(starts, line)
			m.caret = m.caretAtColumn(starts, line-1, goal)
		} else if m.atTopOfTempList() {
			// at the top of the temp list: go back up into the main outline
			m.exitTemp()
		} else if m.cursor > 0 {
			// from the first visual line, cross to the previous node and land
			// on its last visual line, keeping the horizontal column
			goal := m.caretColumn(starts, 0)
			m.cursor--
			prev := m.selectedVisualRows()
			m.caret = m.caretAtColumn(prev, len(prev)-1, goal)
			m.clampCaret()
		}
		return m, nil
	case "down":
		starts := m.selectedVisualRows()
		line := caretVisualLine(starts, m.caret)
		if line < len(starts)-1 {
			// walk down one visual line of the wrapped node first
			goal := m.caretColumn(starts, line)
			m.caret = m.caretAtColumn(starts, line+1, goal)
		} else if m.cursor < len(m.rows)-1 {
			// from the last visual line, cross to the next node and land on its
			// first visual line, keeping the horizontal column
			goal := m.caretColumn(starts, line)
			m.cursor++
			m.caret = m.caretAtColumn(m.selectedVisualRows(), 0, goal)
			m.clampCaret()
		} else if !m.tempActive {
			// past the last node of the main outline: drop into the Temporary Domain
			m.enterTemp()
		}
		return m, nil
	case "left":
		if m.caret > 0 {
			m.caret--
			// a chip anchor is atomic: if the step landed inside one, jump to its start
			if cur := m.cursorItem(); cur != nil {
				if sp := spanContaining(anchorSpans([]rune(cur.name)), m.caret); sp != nil {
					m.caret = sp.start
				}
			}
		} else if m.cursor > 0 {
			// at the start of a node, cross to the previous node and land at its end
			m.cursor--
			if c := m.cursorItem(); c != nil {
				m.caret = len([]rune(c.name))
			}
		}
		return m, nil
	case "right":
		cur := m.cursorItem()
		if cur != nil && m.caret < len([]rune(cur.name)) {
			m.caret++
			// a chip anchor is atomic: if the step landed inside one, jump past it
			if sp := spanContaining(anchorSpans([]rune(cur.name)), m.caret); sp != nil {
				m.caret = sp.end
			}
		} else if cur != nil && m.cursor < len(m.rows)-1 {
			// at the end of a node, cross to the next node and land at its start
			m.cursor++
			m.caret = 0
		}
		return m, nil
	case "home":
		// move to the first position of the current visual line, not the start
		// of the whole node: a wrapped node has several visual lines
		starts := m.selectedVisualRows()
		line := caretVisualLine(starts, m.caret)
		m.caret = starts[line]
		return m, nil
	case "end":
		// move to the last position of the current visual line, not the end of
		// the whole node: a wrapped node has several visual lines. On the final
		// visual line this is the node end.
		cur := m.cursorItem()
		if cur == nil {
			return m, nil
		}
		runes := []rune(cur.name)
		starts := m.selectedVisualRows()
		line := caretVisualLine(starts, m.caret)
		if line+1 >= len(starts) {
			m.caret = len(runes)
			return m, nil
		}
		// stop before the next line's start; a space consumed by the wrap break
		// lands the caret just before it, mirroring the on-break-space render.
		end := starts[line+1]
		if end > 0 && end <= len(runes) && runes[end-1] == ' ' {
			end--
		}
		m.caret = end
		return m, nil
	case "backspace":
		cur := m.cursorItem()
		// a mirror reference is edited at its original — see mirrorContext
		if cur == nil || cur.mirrorOf != "" {
			return m, nil
		}
		if !typeOf(cur.typ).inlineEditable || cur.readonly {
			return m, nil // e.g. json — edited only in the alt+e editor; or a locked node
		}
		if runes := []rune(cur.name); m.boundCaret(len(runes)) > 0 {
			// backspace at a chip anchor's end deletes the whole chip (anchor + record)
			if sp := spanEndingAt(anchorSpans(runes), m.caret); sp != nil {
				m.deleteChipID(sp.id)
				cur.name = string(runes[:sp.start]) + string(runes[sp.end:])
				shiftSpans(cur.uuid, sp.start, sp.start-sp.end)
				m.persistSpans(cur.uuid)
				m.caret = sp.start
				m.markCmdDraft(cur)
				m.unsaved = true
				return m, nil
			}
			cur.name = string(runes[:m.caret-1]) + string(runes[m.caret:])
			shiftSpans(cur.uuid, m.caret-1, -1)
			m.persistSpans(cur.uuid)
			m.caret--
			m.markCmdDraft(cur)
			m.unsaved = true
			return m, nil
		}
		// backspace on an empty non-bullet node demotes its type to a plain bullet
		// first (e.g. Bash → bullet → delete), so a special node isn't blown away in
		// one keypress — the next backspace then merges/removes the bullet.
		if cur.name == "" && cur.mirrorOf == "" && typeOf(cur.typ).key != database.TypeBullets {
			cur.typ = database.TypeBullets
			m.unsaved = true
			return m, nil
		}
		// caret at the start: merge this node into the one above. Its text appends
		// to the previous node and its children move under that node.
		if m.cursor > 0 {
			prev := m.rows[m.cursor-1].it
			if prev.mirrorOf != "" {
				return m, nil // can't merge into a mirror reference
			}
			// merging up into a blank placeholder line: the absorbed node is really
			// the content, so carry its style/type/collapsed across — otherwise
			// backspacing a red, collapsed node into an empty line above it would
			// silently drop its colour and re-expand its children.
			if prev.name == "" && prev.style == "" && len(prev.children) == 0 {
				prev.style = cur.style
				prev.typ = cur.typ
				prev.completedAt = cur.completedAt
				prev.collapsed = cur.collapsed
				if m.tree.db != nil {
					_ = database.SetCollapsed(m.tree.db, prev.uuid, cur.collapsed)
				}
			}
			mergeAt := len([]rune(prev.name))
			prev.name += cur.name
			for _, c := range cur.children {
				c.parent = prev
			}
			prev.children = append(prev.children, cur.children...)
			cur.children = nil
			m.stopAgentsUnder(cur) // mention may still have a turn in flight
			m.tree.remove(cur)
			m.unsaved = true
			m.refreshRows()
			m.cursor = m.rowIndexOf(prev)
			m.clampCursor()
			m.caret = mergeAt
			return m, nil
		}
		// the first node and empty: just remove it
		if cur.name == "" && len(cur.children) == 0 {
			m.stopAgentsUnder(cur)
			m.tree.remove(cur)
			m.unsaved = true
			m.ensureViewNonEmpty()
			m.refreshRows()
		}
		return m, nil
	}

	// printable input (space arrives as KeySpace, not KeyRunes)
	if k.Type == tea.KeySpace && !k.Alt {
		k.Type = tea.KeyRunes
		k.Runes = []rune{' '}
	}
	if k.Type == tea.KeyRunes && len(k.Runes) > 0 && !k.Alt {
		cur := m.cursorItem()
		if cur == nil {
			// empty view: create the first node
			it, err := m.tree.insertFirstChild(m.viewRoot())
			if err != nil {
				m.err = err
				return m.quit()
			}
			m.refreshRows()
			m.cursor = m.rowIndexOf(it)
			m.caret = 0
			cur = it
		}

		// "/" opens the slash menu anywhere in the row. On editable rows it
		// is typed into the text and stripped when a command runs or the menu
		// is cancelled, so esc restores the name to what it was before.
		// alt+P (alt+shift+p) opens the same menu without inserting "/" (see openSlashMenu).
		if string(k.Runes) == "/" && !k.Paste {
			m.openSlashMenu(cur.mirrorOf == "" && !cur.readonly)
			return m, nil
		}

		// ">" opens the file picker to splice a path chip at the caret — the chip
		// renders as "›name", so ">" is its natural trigger. It fires in every
		// inline-editable type, including bash/code/query where ">" is real syntax:
		// the picker is cancelable, and dismissing it types a literal ">" instead
		// (see the fzfPickedMsg handler), so a redirect still works — you just quit
		// the picker. Only at a word start (start of text or after a space) so a
		// mid-word ">" and the "->" log gesture stay literal; when fzf is missing we
		// fall through to typing ">" literally.
		if string(k.Runes) == ">" && !k.Paste && cur.mirrorOf == "" && !cur.readonly &&
			pathChipTrigger(cur.typ) && atWordStart(cur, m.caret) {
			if cmd := m.openFilePicker(cur, ">"); cmd != nil {
				return m, cmd
			}
		}

		// "[[" opens the link picker: the second "[" drops the first and opens the
		// finder where you pick a node or type/paste a URL. Unlike the file picker
		// it has no cancel-to-literal path, so it stays off where "[" is real syntax
		// (bash test brackets, code, query, quote, json).
		if string(k.Runes) == "[" && !k.Paste && cur.mirrorOf == "" && !cur.readonly &&
			linkChipTrigger(cur.typ) && runeBeforeCaretIs(cur, m.caret, '[') {
			runes := []rune(cur.name)
			m.boundCaret(len(runes))
			cur.name = string(runes[:m.caret-1]) + string(runes[m.caret:])
			m.caret--
			m.unsaved = true
			m.openFinder(actLinkInsert)
			return m, nil
		}

		// "#" opens the tag completer at a word boundary; ":" opens the query-command
		// completer in a query node. Both stay literal mid-word so "C#"/"a:b" type
		// normally; tags skip bash/code where "#" is a comment.
		if string(k.Runes) == "#" && !k.Paste && cur.mirrorOf == "" && !cur.readonly &&
			tagPickerTrigger(cur.typ) && atWordStart(cur, m.caret) {
			return m.openCompleter(cur, complTag, "#")
		}
		if string(k.Runes) == ":" && !k.Paste && cur.mirrorOf == "" && !cur.readonly &&
			cur.typ == database.TypeQuery && atWordStart(cur, m.caret) {
			return m.openCompleter(cur, complQueryCmd, ":")
		}
		// "@" opens the agent picker at a word boundary — picking lands an
		// agent chip; alt+r on the node later starts the thread (see agent.go)
		if string(k.Runes) == "@" && !k.Paste && cur.mirrorOf == "" && !cur.readonly &&
			len(m.agents) > 0 && tagPickerTrigger(cur.typ) && atWordStart(cur, m.caret) {
			return m.openCompleter(cur, complAgent, "@")
		}

		if cur.mirrorOf != "" {
			return m, nil // a mirror reference is edited at its original — see mirrorContext
		}
		if !typeOf(cur.typ).inlineEditable || cur.readonly {
			return m, nil // e.g. json — edited only in the alt+e editor; or a locked node (slash above still works)
		}

		text := string(k.Runes)
		if k.Paste {
			if lines := pasteLines(text); len(lines) > 1 {
				return m.pasteFanOut(cur, lines)
			} else if len(lines) == 1 {
				text = lines[0]
			} else {
				text = ""
			}
		}

		// guard against a caret left stale by a cursor move (e.g. landing on a
		// shorter node) — slicing runes[:m.caret] would otherwise panic
		m.boundCaret(len([]rune(cur.name)))

		// typing a space commits a "$cmd" + double space into a cmd chip, or a
		// #tag / date token before it into a chip. A leading "$" never converts
		// the node's type — bash nodes are made via /type; "$" is chip territory.
		if text == " " && !k.Paste {
			if m.bashCmdBeforeCaret(cur) {
				return m, nil // a "$cmd" + double space committed a cmd chip
			}
			m.chipifyBeforeCaret(cur)
		}

		runes := []rune(cur.name)
		m.boundCaret(len(runes)) // chipify may have changed the name/caret
		ins := []rune(text)
		cur.name = string(runes[:m.caret]) + string(ins) + string(runes[m.caret:])
		if len(ins) > 0 {
			shiftSpans(cur.uuid, m.caret, len(ins)) // painted runs ride along
			m.persistSpans(cur.uuid)
			m.typedUUID = cur.uuid // blur-send candidate (see blurSendCheck)
		}
		m.caret += len(ins)
		m.markCmdDraft(cur)
		m.unsaved = true
		m.maybeLinkToMirror(cur)
		return m, nil
	}

	return m, nil
}
