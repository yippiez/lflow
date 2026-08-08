package editor

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/pkg/errors"

	"github.com/lflow/lflow/tui/database"
	"github.com/lflow/lflow/tui/utils/browser"
)

// lockedFlash explains a refused splice in the terms of whatever owns the node:
// a mirrored Zotero entry says so by name, anything else is a plain lock.
func lockedFlash(it *item) string {
	if zoteroMirrored(it) {
		return "zotero · a mirrored entry is read-only · alt+r refreshes it"
	}
	return "node structure is locked"
}

func (m *Model) handleKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := k.String()
	m.flash = "" // one-shot: whatever this key does sets the next status
	m.flashErr = false

	// establish auto-focus before this key is dispatched, so a key arriving while
	// the cursor already rests on a code block (e.g. the first keystroke at open)
	// routes into the block editor rather than the outline. Update re-runs it
	// after the key to track cursor moves for the next render.
	m.reconcileAutoFocus()

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
			m.clearSel() // first esc releases the row selection
			return m, nil
		}
		if m.textSelOn {
			m.clearTextSel() // …and the horizontal one
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
	case modeSlash, modeType, modeStyle, modeTheme, modeComplete, modeTagColor, modeInsert,
		modeAgentPick, modeAgentColor, modeCharacterPick, modeCharacterColor, modeCite:
		return m.handleListMode(k, m.listSource())
	case modeFinder:
		return m.finder.handleKey(m, k, nodeFinderBackend{})
	case modeLinkEdit:
		return m.handleLinkEditKey(k)
	case modeCmdEdit:
		return m.handleCmdEditKey(k)
	case modeAgentEdit:
		return m.handleAgentEditKey(k)
	case modeNote:
		return m.handleNoteKey(k)
	case modeConfirm:
		return m.handleConfirmKey(k)
	case modeSuggest:
		return m.handleSuggestKey(k)
	case modeSettings:
		return m.handleSettingsKey(k)
	case modeFlash:
		return m.handleFlashKey(k)
	case modeShortcuts:
		return m.handleShortcutsKey(k)
	}

	// alt+e on a block-faced node (nlpcompute) flips its code ⇄ prose face rather
	// than entering an editor — the code face auto-focuses for editing on its own.
	// Handled before the focused-view capture below so it fires even while the code
	// face is auto-focused.
	if key == "alt+e" && m.mode == modeOutline {
		if cur := m.cursorItem(); cur != nil && typeOf(cur.typ).blockFaces {
			m.toggleBlockFace(cur)
			return m, nil
		}
	}

	// A focused inline node view captures input first (it stays inside the outline,
	// so we're still modeOutline). The view handles its own keys; esc or alt+e
	// defocuses (flushing edits); ctrl+c/ctrl+q fall through to quit; everything
	// else is swallowed so it can't leak into outline navigation.
	if m.focused && m.mode == modeOutline {
		cur := m.cursorItem()
		if v := m.activeView(cur); v != nil {
			if cmd, handled := v.key(m, cur, k); handled {
				return m, cmd
			}
			// auto-focus (Code): the view declined this key, so it is meant for the
			// outline — release focus and let it act on the block node rather than
			// swallowing it. esc/alt+e park the block unfocused (the hold stops an
			// immediate re-grab); up/down at the block's top/bottom line cross to
			// the neighbouring row; everything else falls through to outline keys.
			auto := m.autoFocused != nil && m.autoFocused == cur
			switch key {
			case "esc", "alt+e":
				v.leave(m, cur)
				m.focused = false
				if typeOf(cur.typ).blockFaces {
					// esc off a two-faced code editor collapses to the prose face —
					// no held code-block state where typing would edit the hidden
					// instruction; the prose face is plainly inline-editable.
					m.nodeStore(cur.uuid)["blockFace"] = "nlp"
					m.autoFocused = nil
				} else if auto {
					m.autoFocused = nil
					m.autoFocusHold = cur
				}
				return m, nil
			case "ctrl+c", "ctrl+q":
				// fall through to the quit handler below
			default:
				if !auto {
					return m, nil // manual focus swallows everything else
				}
				v.leave(m, cur)
				m.focused = false
				m.autoFocused = nil
				switch key {
				case "up":
					if m.cursor > 0 {
						m.cursor--
						if c := m.cursorItem(); c != nil {
							m.caret = len([]rune(c.name))
						}
						m.clampCaret()
					}
					return m, nil
				case "down":
					if m.cursor < len(m.rows)-1 {
						m.cursor++
						m.caret = 0
					}
					return m, nil
				}
				// any other key: fall through to normal outline handling below
			}
		} else {
			m.focused = false
		}
	}

	// snapshot the tree before a mutating outline key so /undo can reverse it
	m.snapshotForKey(key, k)

	// selection lifecycle: shift+arrows grow a selection — ↑/↓ by row
	// (multisel.go), ←/→ inside the node's own text (textsel.go). Any other plain
	// movement, typing or esc drops it, except y/x: once a selection is live those
	// are commands (copy/cut), not text.
	if m.mode == modeOutline {
		switch key {
		case "shift+up":
			m.clearTextSel()
			m.startOrExtendSel()
			if m.cursor > 0 {
				m.cursor--
			}
			return m, nil
		case "shift+down":
			m.clearTextSel()
			m.startOrExtendSel()
			if m.cursor < len(m.rows)-1 {
				m.cursor++
			}
			return m, nil
		case "shift+left":
			m.extendTextSel(-1, false)
			return m, nil
		case "shift+right":
			m.extendTextSel(1, false)
			return m, nil
		case "ctrl+shift+left", "alt+shift+left":
			m.extendTextSel(-1, true)
			return m, nil
		case "ctrl+shift+right", "alt+shift+right":
			m.extendTextSel(1, true)
			return m, nil
		}
		if m.textSelOn {
			switch key {
			// the style picker (and the menus that reach it) style the run;
			// yank/cut take it to the clipboard (see clipboard.go)
			case "/", "alt+P", "alt+a", "alt+c", "y", "x", "alt+y", "alt+x", "ctrl+x":
			case "backspace":
				// a selection is content: backspace removes ALL of it, not the
				// one character behind the caret
				if m.deleteTextSelection() {
					return m, nil
				}
				m.clearTextSel()
			default:
				// typing over a selection REPLACES it — the run goes, and the key
				// that removed it lands where it was. Anything that is not a typed
				// character just releases the selection as before.
				if isTypedRune(k) {
					// a paste that IS a link names the run after it: the selected
					// text becomes the link chip, the pasted link its target
					if m.pasteLinkOverSelection(k) {
						return m, nil
					}
					m.deleteTextSelection()
					break
				}
				m.clearTextSel()
			}
		}
		if m.selOn {
			switch key {
			case "tab", "shift+tab", "ctrl+d", "alt+d", "ctrl+shift+backspace",
				"backspace",
				"alt+shift+up", "ctrl+shift+up", "ctrl+alt+up",
				"alt+shift+down", "ctrl+shift+down", "ctrl+alt+down",
				"/", "alt+P", "alt+a", // the slash menu may apply /type //style //move to the selection
				"alt+t", "alt+c", // the type/style pickers retype/re-style the whole selection
				"y", "x", "alt+y", "alt+x", "ctrl+x": // yank/cut take the whole selection
			case "esc":
				m.clearSel()
				return m, nil
			default:
				m.clearSel()
			}
		}
	}

	// Vim-shaped single keys are commands only after a selection exists; with no
	// selection they remain ordinary editable text.
	if (m.textSelOn || m.selOn) && (key == "y" || key == "x") {
		m.copyCut(key == "x")
		return m, nil
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
		if key == "pgup" {
			step = -step
		}
		m.scrollBody(step)
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
	case "ctrl+y", "ctrl+shift+z":
		// redo the last undo (ctrl+shift+z is the accepted chord where the
		// terminal delivers it; ctrl+y works everywhere)
		m.redo()
		return m, nil
	case "enter":
		cur := m.cursorItem()
		if cur != nil && cur.queryGenerated() {
			m.errorFlash("query view is structurally locked")
			return m, nil
		}
		// commit a #tag or date token under the caret into a chip before splitting
		if cur != nil {
			m.chipifyBeforeCaret(cur)
			// a committed line under a session-bound mention is a child change —
			// arm the debounced think (Enter itself does not ship)
		}
		mc := m.mirrorContext()
		// caret at the very start of a node that has text: don't split — keep the
		// node and its whole subtree intact and push it down, opening an empty node
		// above it with the cursor there.
		if cur != nil && mc.editable && m.caret == 0 && cur.name != "" {
			it, err := m.tree.insertSiblingBefore(cur)
			if errors.Is(err, errStructureLocked) {
				m.flash = lockedFlash(cur)
				return m, nil
			}
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
			return m, nil
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
		if errors.Is(err, errStructureLocked) {
			m.flash = lockedFlash(cur)
			return m, nil
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
			// reference, a non-inline-editable type (json), or a locked node is not
			// split — it just opens an empty sibling (newline without rewriting text).
			if cur != nil && mc.editable && typeOf(cur.typ).inlineEditable && !cur.readonly {
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
		return m, nil
	case "tab":
		// no chip kind claims Tab as a trigger, so it is free to just indent. The
		// Temporary Domain edits exactly like the main outline, so indenting works
		// there too.
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
			// escape=ctx so a through-child can leave the mirrored source and
			// land after the mirror; after that findRow falls back to the real row
			if m.tree.outdent(cur, mc.localRoot, mc.ctx) {
				m.unsaved = true
				m.refreshRows()
				m.cursor = m.findRow(cur, mc.ctx)
			}
		}
		return m, nil
	case "ctrl+@", "ctrl+space":
		if cur := m.cursorItem(); cur != nil && len(m.tree.childItems(cur)) > 0 && m.cursor < len(m.rows) {
			if cur.collapsed || m.rows[m.cursor].cycled {
				m.expandStep()
			} else {
				m.collapseStep()
			}
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
			if cur.structureLocked {
				// refuse here rather than at the end of a confirmation the answer
				// to which cannot matter
				m.flash = lockedFlash(cur)
			} else if len(cur.children) > 0 {
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
		tgt := m.editTarget()
		if tgt == nil {
			return m, nil
		}
		runes := []rune(tgt.name)
		if m.boundCaret(len(runes)) == 0 {
			return m, nil
		}
		spans := anchorSpans(runes)
		// caret right after a chip → delete just that chip
		if sp := spanEndingAt(spans, m.caret); sp != nil {
			m.deleteChipID(sp.id)
			tgt.name = string(runes[:sp.start]) + string(runes[sp.end:])
			m.caret = sp.start
			m.unsaved = true
			return m, nil
		}
		target := deleteWordBoundary(runes, m.caret)
		if sp := spanContaining(spans, target); sp != nil {
			target = sp.start // don't cut into a chip — take the whole thing
		}
		for _, sp := range spans { // drop chip records the deletion removes
			if sp.start >= target && sp.end <= m.caret {
				m.deleteChipID(sp.id)
			}
		}
		tgt.name = string(runes[:target]) + string(runes[m.caret:])
		shiftSpans(tgt.uuid, target, target-m.caret)
		m.persistSpans(tgt.uuid)
		m.caret = target
		m.unsaved = true
		return m, nil
	case "ctrl+t":
		// convert a time phrase under the cursor to canonical date text (the renderer
		// then chips it); with no date phrase there, convert a bare URL — or a
		// pasted lflow://node/<uuid> link — under the cursor straight into a link
		// chip instead — neither ever happens just from typing, only this explicit
		// key (see the status-bar hint in view.go).
		if tgt := m.editTarget(); tgt != nil {
			if d := detectDate(tgt.name, m.caret, time.Now()); d != nil && d.phrase != d.canonical() {
				runes := []rune(tgt.name)
				date := d.canonical()
				tgt.name = string(runes[:d.start]) + date + string(runes[d.end:])
				m.caret = d.start + len([]rune(date))
				m.unsaved = true
			} else if u := detectURLNear(tgt.name, m.caret); u != nil && chipsEnabled(tgt) {
				value, label := u.raw, ""
				if uuid, isNode := nodeLinkUUID(u.raw); isNode {
					// a pasted node link names itself after its target node; the
					// URI is already canonical, never browser-normalized
					label = m.nodeLinkLabel(uuid)
				} else {
					value = browser.Normalize(u.raw)
					label = urlChipLabel(value)
				}
				anchor := m.createLabeledChip(chipKindLink, value, label)
				if anchor != "" {
					runes := []rune(tgt.name)
					tgt.name = string(runes[:u.start]) + anchor + string(runes[u.end:])
					m.caret = u.start + len([]rune(anchor))
					m.unsaved = true
				}
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
		runes := []rune(m.caretText(cur))
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
					m.caret = len([]rune(m.caretText(c)))
				}
			}
			return m, nil
		}
		runes := []rune(m.caretText(cur))
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
		// browser); on a citation chip it goes to the paper, local or cloud per
		// the zotero.open setting; off a chip it opens the /goto finder
		if cur := m.cursorItem(); cur != nil {
			if c, ok := m.linkChipAtCaret(cur); ok {
				return m.followLink(c)
			}
			if c, ok := m.zoteroChipAtCaret(cur); ok {
				return m.openZoteroChip(c, zoteroTargetLocal())
			}
			if zoteroMirrored(cur) {
				return m.zoteroOpenNode(cur, false)
			}
		}
		if !m.tempActive {
			m.openFinder(actGoto)
		}
		return m, nil
	case "alt+i":
		// edit the command inside a cmd chip at the caret — the one edit a $ chip
		// allows (see modeCmdEdit). alt+e on a cmd chip is its run output, so the
		// command itself gets this key instead.
		if cur := m.cursorItem(); cur != nil {
			if c, ok := m.cmdChipAtCaret(cur); ok {
				m.openCmdEdit(c)
				return m, nil
			}
			// on a markup row, ⌥i edits the RESERVED element — the same gesture,
			// the same meaning: change the structured thing this row is built on
			if c, ok := m.markupElementChip(cur); ok {
				m.openCmdEdit(c)
				return m, nil
			}
		}
		return m, nil
	case "alt+e":
		// toggle a type's inline expanded view (json/bash): alt+e focuses it,
		// alt+e again collapses. Else fall back to an action-only expand (voice play).
		if cur := m.cursorItem(); cur != nil {
			if v := nodeViewOf(cur); v != nil {
				if m.focused {
					v.leave(m, cur)
					m.focused = false
				} else if v.enter(m, cur) {
					m.focused = true
					m.focusScroll = 0
				}
			} else if c, ok := m.cmdChipAtCaret(cur); ok {
				m.focusCmdChip(c) // ⌥e on a cmd chip: its run output as an inline band
				return m, nil
			} else if c, ok := m.agentChipForKeys(cur); ok {
				m.openAgentEdit(c) // ⌥e on a session chip: the name+color page
				return m, nil
			} else if c, ok := m.linkChipAtCaret(cur); ok {
				m.openLinkEdit(c) // ⌥e on a link chip edits its name + target
				return m, nil
			} else if word, ok := m.tagWordAtCaret(cur); ok {
				m.openTagColor(word) // ⌥e on a tag picks its pill color
				return m, nil
			} else if e := typeOf(cur.typ).expand; e != nil {
				return m, e(m, cur)
			}
		}
		return m, nil
	case "alt+r":
		// run / re-run a runnable node's own action (bash/query/voice). Never
		// auto-runs.
		if cur := m.cursorItem(); cur != nil {
			// inside a Zotero mirror alt+r always means "re-read the entry",
			// whatever the node's own type would otherwise run — a mirrored crop
			// is a real image node, and image's alt+r pastes the clipboard
			if zoteroMirrored(cur) {
				return m, runZoteroPull(m, cur)
			}
			if c, ok := m.cmdChipAtCaret(cur); ok {
				return m, m.runCmdChip(c) // an inline cmd chip runs on its own
			}
			// running a link chip IS opening it — the browser for a URL (a Google
			// Sheets/Docs chip lands in the host browser), a jump for a node link.
			// Same action as alt+g, reached from the key every other inline chip
			// is run with.
			if c, ok := m.linkChipAtCaret(cur); ok {
				return m.followLink(c)
			}
			if run := typeOf(cur.typ).run; run != nil {
				if bin, missing := m.typeDepMissing(cur.typ); missing {
					m.errorFlash("Missing dependency: " + bin)
					return m, nil
				}
				return m, run(m, cur)
			}
			// any pulled Workflowy mirror refreshes its own branch — the
			// recursive-mirror model: every pulled node is a wf handle too
			if _, ok := m.wfMap[cur.uuid]; ok {
				return m, runWF(m, cur)
			}
		}
		return m, nil
	case "alt+v":
		// review the proposals pending on this node: y approves (applying it),
		// n rejects. Until then the suggestion has changed nothing.
		m.enterSuggestReview()
		return m, nil
	case "alt+o":
		// open the cursor node in the HOST's own app — outside the terminal
		// (image → the desktop image viewer). On a citation chip it opens the
		// destination alt+g did NOT take: the Zotero app when the setting points
		// at the cloud, the web library when it points at this desktop — so a
		// machine without Zotero installed can still reach the paper.
		if cur := m.cursorItem(); cur != nil {
			if c, ok := m.zoteroChipAtCaret(cur); ok {
				return m.openZoteroChip(c, !zoteroTargetLocal())
			}
			// a type that knows how to hand ITSELF to the desktop does that — so
			// a mirrored crop opens in the image viewer, which is the only place
			// its real pixels can be seen; everything else in a mirror offers the
			// destination alt+g did not take
			if open := typeOf(cur.typ).openHost; open != nil {
				return m, open(m, cur)
			}
			if zoteroMirrored(cur) {
				return m.zoteroOpenNode(cur, true)
			}
			// a session chip's "open in host" is its native resume COMMAND on
			// the clipboard — paste it in whatever terminal you like; lflow
			// never runs the agent itself
			if c, ok := m.agentChipForKeys(cur); ok {
				m.copyAgentCommand(c)
				return m, nil
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
	case "alt+a":
		// open the command palette without typing "/" into the node text —
		// mnemonic twin of alt+P, on the home row
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
	case "alt+t":
		// open the type picker directly (same as /type)
		return m.runSlash("/type")
	case "alt+c":
		// color: a session chip under the caret takes the key for its own color,
		// the way ⌥k goes to a cmd chip's band before the node's. Everywhere else
		// this opens the style picker (same as /style) — c for colors; alt+y is
		// the yank key, so the picker moved off it.
		if cur := m.cursorItem(); cur != nil {
			if c, ok := m.agentChipForKeys(cur); ok {
				m.openAgentColor(c)
				return m, nil
			}
		}
		return m.runSlash("/style")
	case "alt+k":
		// kill: stop a running command, keeping what was captured; when nothing is
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
			} else if r != nil && len(r.lines()) > 0 {
				r.out = nil
				r.scr = nil // drop the terminal screen with the band
				r.dropped = 0
				r.pwd = ""
				m.persistRunOut(id) // an empty band deletes the row
				m.setCmdPreview(id)
			}
		}
		return m, nil
	case "alt+y", "alt+x", "ctrl+x":
		// yank / cut, acting on the horizontal selection, else the row selection,
		// else the cursor node's subtree (see clipboard.go). alt+y yanks and
		// alt+x cuts; ctrl+x is kept as the cut twin for terminals that swallow
		// the alt chord.
		m.copyCut(key != "alt+y")
		return m, nil
	case "alt+s":
		// flash: label every visible row's actions (jump / run / expand / fold) and
		// hand off to modeFlash so the next keystrokes pick one — act on a node
		// elsewhere on screen without moving the cursor there. See flash.go.
		m.enterFlash()
		return m, nil
	case "alt+up", "ctrl+up":
		m.collapseStep() // fold the cursor node, one cycle level at a time
		return m, nil
	case "alt+down", "ctrl+down":
		m.expandStep() // open the cursor node, one cycle level at a time
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
		} else if cur := m.cursorItem(); cur != nil && m.caret < len([]rune(m.caretText(cur))) {
			// on the last visual line: snap the caret to the end of this node's
			// text first — the next down press crosses to the next node
			m.caret = len([]rune(m.caretText(cur)))
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
				if sp := spanContaining(anchorSpans([]rune(m.caretText(cur))), m.caret); sp != nil {
					m.caret = sp.start
				}
			}
		} else if m.cursor > 0 {
			// at the start of a node, cross to the previous node and land at its end
			m.cursor--
			if c := m.cursorItem(); c != nil {
				m.caret = len([]rune(m.caretText(c)))
			}
		}
		return m, nil
	case "right":
		cur := m.cursorItem()
		if cur != nil && m.caret < len([]rune(m.caretText(cur))) {
			m.caret++
			// a chip anchor is atomic: if the step landed inside one, jump past it
			if sp := spanContaining(anchorSpans([]rune(m.caretText(cur))), m.caret); sp != nil {
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
		runes := []rune(m.caretText(cur))
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
		// backspace deletes the whole shift-selection, like alt+d — a selected
		// range is content, not a cursor to back over.
		if m.selOn {
			if m.selHasChildren() {
				m.mode = modeConfirm
			} else {
				m.selDelete()
			}
			return m, nil
		}
		cur := m.cursorItem()
		if cur == nil {
			return m, nil
		}
		// text belongs to the node the row SHOWS — the source, when the row is a
		// mirror of one; a fixed row (query result, Zotero, locked, json) has no
		// edit target at all and swallows the key. See editTarget.
		tgt := m.editTargetOf(cur)
		if tgt == nil {
			return m, nil
		}
		if runes := []rune(tgt.name); m.boundCaret(len(runes)) > 0 {
			// backspace at a chip anchor's end deletes the whole chip (anchor + record)
			if sp := spanEndingAt(anchorSpans(runes), m.caret); sp != nil {
				m.deleteChipID(sp.id)
				tgt.name = string(runes[:sp.start]) + string(runes[sp.end:])
				shiftSpans(tgt.uuid, sp.start, sp.start-sp.end)
				m.persistSpans(tgt.uuid)
				m.caret = sp.start
				m.unsaved = true
				return m, nil
			}
			tgt.name = string(runes[:m.caret-1]) + string(runes[m.caret:])
			shiftSpans(tgt.uuid, m.caret-1, -1)
			m.persistSpans(tgt.uuid)
			m.caret--
			m.unsaved = true
			return m, nil
		}
		// a divider is a full-width rule, not content: backspace on it (or into
		// it) is a no-op — never demote it, never merge its row with a neighbor.
		if cur.typ == database.TypeDivider {
			return m, nil
		}
		// backspace on an empty non-default node demotes its type to the tree's
		// plain type first (bullets, or a file session's statement type), so a
		// special node isn't blown away in one keypress — the next backspace
		// then merges/removes it.
		plainType := m.tree.defaultType
		if plainType == "" {
			plainType = database.TypeBullets
		}
		if cur.name == "" && cur.mirrorOf == "" && typeOf(cur.typ).key != plainType {
			m.setNodeType(cur, plainType)
			return m, nil
		}
		// caret at the start: merge this node into the one above. Its text appends
		// to the previous node and its children move under that node.
		if m.cursor > 0 {
			prev := m.rows[m.cursor-1].it
			if prev.mirrorOf != "" {
				return m, nil // can't merge into a mirror reference
			}
			if prev.typ == database.TypeDivider || prev.typ == database.TypeEmpty {
				return m, nil // never merge text into a divider rule or an empty spacer
			}
			// merging up into a blank placeholder line: the absorbed node is really
			// the content, so carry its style/type/collapsed across — otherwise
			// backspacing a red, collapsed node into an empty line above it would
			// silently drop its colour and re-expand its children.
			// Only when the absorbed node has something to preserve (text or
			// children). An empty leaf is just deleted — same as alt+d — so a
			// blank todo above an empty bullet must stay a todo, not flip to ○.
			if prev.name == "" && prev.style == "" && len(prev.children) == 0 &&
				(cur.name != "" || len(cur.children) > 0) {
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
			m.removeNodeStateUnder(cur)
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
			m.removeNodeStateUnder(cur)
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
			m.openSlashMenu(m.editTargetOf(cur) != nil)
			return m, nil
		}

		// "[[" opens the link picker: the second "[" drops the first and opens the
		// finder where you pick a node or type/paste a URL. It has no
		// cancel-to-literal path, so it stays off where "[" is real syntax (bash
		// test brackets, code, query, quote, json).
		if tgt := m.editTargetOf(cur); tgt != nil && string(k.Runes) == "[" && !k.Paste &&
			linkChipTrigger(tgt.typ) && runeBeforeCaretIs(tgt, m.caret, '[') {
			runes := []rune(tgt.name)
			m.boundCaret(len(runes))
			tgt.name = string(runes[:m.caret-1]) + string(runes[m.caret:])
			m.caret--
			m.unsaved = true
			m.openFinder(actLinkInsert)
			return m, nil
		}

		// "((" opens the mirror finder: the second "(" drops the first, mirroring
		// the [[ gesture but creating a structural mirror rather than an inline link.
		// Keep it off rows where parentheses are syntax (bash/code/query/math/json).
		if tgt := m.editTargetOf(cur); tgt != nil && string(k.Runes) == "(" && !k.Paste &&
			linkChipTrigger(tgt.typ) && runeBeforeCaretIs(tgt, m.caret, '(') {
			runes := []rune(tgt.name)
			m.boundCaret(len(runes))
			tgt.name = string(runes[:m.caret-1]) + string(runes[m.caret:])
			m.caret--
			m.unsaved = true
			m.openFinder(actMirrorHere)
			return m, nil
		}

		// "$$" lands an empty cmd chip: the second "$" drops the first and splices
		// a blank $ chip to fill in (alt+i edits its command). A single "$" is
		// always literal — "$i", "$(seq …)" and "$HOME" are shell syntax that type
		// normally in a bash node, never a chip. There is no cancel path, matching
		// "[["; /insert → cmd is the other way in.
		if tgt := m.editTargetOf(cur); tgt != nil && string(k.Runes) == "$" && !k.Paste &&
			chipsEnabled(tgt) && runeBeforeCaretIs(tgt, m.caret, '$') {
			runes := []rune(tgt.name)
			m.boundCaret(len(runes))
			tgt.name = string(runes[:m.caret-1]) + string(runes[m.caret:])
			m.caret--
			m.unsaved = true
			if anchor := m.createChip(chipKindCmd, ""); anchor != "" {
				m.insertLiteralAt(cur, m.caret, anchor)
				m.flash = "empty $ chip · alt+i edits the command"
			}
			return m, nil
		}

		// "@@" opens the cite picker: the second "@" drops the first and opens the
		// Zotero library search, exactly like "[[" opens the link picker. A single
		// "@" always stays literal (an email address, a handle), which is why the
		// gesture is doubled.
		if tgt := m.editTargetOf(cur); tgt != nil && string(k.Runes) == "@" && !k.Paste &&
			linkChipTrigger(tgt.typ) && runeBeforeCaretIs(tgt, m.caret, '@') {
			runes := []rune(tgt.name)
			m.boundCaret(len(runes))
			tgt.name = string(runes[:m.caret-1]) + string(runes[m.caret:])
			m.caret--
			m.unsaved = true
			return m.openCitePicker(citeChip)
		}

		// "#" opens the tag completer at a word boundary; ":" opens the query-command
		// completer in a query node, or the icon shortcode picker on every other
		// inline-editable node. Both stay literal mid-word so "C#"/"a:b" type
		// normally; tags skip bash/code where "#" is a comment. Typing a digit or
		// special char right after "#" closes the completer again, so "#1" stays a
		// literal "number one". Query nodes reach icons via /insert → icon (see insertChip).
		if tgt := m.editTargetOf(cur); tgt != nil && string(k.Runes) == "#" && !k.Paste &&
			tagPickerTrigger(tgt.typ) && atWordStart(tgt, m.caret) {
			return m.openCompleter(tgt, complTag, "#")
		}
		if tgt := m.editTargetOf(cur); tgt != nil && string(k.Runes) == ":" && !k.Paste &&
			atWordStart(tgt, m.caret) {
			if tgt.typ == database.TypeQuery {
				return m.openCompleter(tgt, complQueryCmd, ":")
			}
			return m.openIconPicker(tgt)
		}
		// a fixed row (query result, Zotero, locked, json) takes the key and does
		// nothing with it; a mirror of an ordinary node types into its source
		tgt := m.editTargetOf(cur)
		if tgt == nil {
			return m, nil
		}
		cur = tgt

		text := string(k.Runes)
		if k.Paste {
			if lines := pasteLines(text); len(lines) > 1 {
				return m.pasteFanOut(cur, lines)
			} else if len(lines) == 1 {
				text = lines[0]
			} else {
				text = ""
			}
			// a pasted service URL (Google Sheets/Docs/Drive …) lands as its
			// branded chip instead of a wall of URL; every other paste is text
			// exactly as before (see service.go)
			if m.pasteServiceLink(cur, text) {
				return m, nil
			}
		}

		// guard against a caret left stale by a cursor move (e.g. landing on a
		// shorter node) — slicing runes[:m.caret] would otherwise panic
		m.boundCaret(len([]rune(cur.name)))

		// typing a space commits a #tag / date token before it into a chip. A "$"
		// command never auto-chips: "$$" is the only way a cmd chip forms by
		// typing (a single "$" is literal everywhere — $i, $(…), $HOME), and a
		// bash node's whole row is shell syntax anyway (bashLiteralRow).
		if text == " " && !k.Paste {
			m.chipifyBeforeCaret(cur)
		}

		runes := []rune(cur.name)
		m.boundCaret(len(runes)) // chipify may have changed the name/caret
		ins := []rune(text)
		cur.name = string(runes[:m.caret]) + string(ins) + string(runes[m.caret:])
		if len(ins) > 0 {
			shiftSpans(cur.uuid, m.caret, len(ins)) // painted runs ride along
			m.persistSpans(cur.uuid)
		}
		m.caret += len(ins)
		m.unsaved = true
		m.maybeLinkToMirror(cur)
		return m, nil
	}

	return m, nil
}

// wheelStep is how many body rows one mouse-wheel notch scrolls — the small
// sibling of the half-viewport pgup/pgdown step.
const wheelStep = 3

// scrollBody pins the viewport (entering scroll mode from what is currently on
// screen) and moves it delta rows — the shared engine behind pgup/pgdown and
// the mouse wheel. The upper bound is clamped by viewWindow.
func (m *Model) scrollBody(delta int) {
	if !m.scrolling {
		m.scrolling = true
		m.scrollTop = m.viewTop // start from what is currently on screen
	}
	m.scrollTop += delta
	if m.scrollTop < 0 {
		m.scrollTop = 0
	}
}

// handleMouse: the wheel scrolls the body like pgup/pgdown but in small steps.
// When the wheel is over the read-only Temporary Domain panel (bottom of the
// screen, unfocused), it scrolls that panel's window instead of the main body.
// Everything else (clicks, motion) is ignored — the mouse is captured only so
// the terminal reports wheel events (hold shift to select text natively).
// Wheel events bypass handleKey, so they never clear the scroll pin; the next
// real key does, exactly like after a pgup.
func (m *Model) handleMouse(msg tea.MouseMsg) {
	if m.mode != modeOutline || msg.Action != tea.MouseActionPress {
		return
	}
	switch msg.Button {
	case tea.MouseButtonWheelUp:
		if m.scrollTempPanel(-wheelStep, msg.Y) {
			return
		}
		m.scrollBody(-wheelStep)
	case tea.MouseButtonWheelDown:
		if m.scrollTempPanel(wheelStep, msg.Y) {
			return
		}
		m.scrollBody(wheelStep)
	}
}

// scrollTempPanel wheels the read-only temp panel (when visible and unfocused) if
// the event is over its region, returning true when it handled the scroll. The
// offset is clamped to zero here; the upper bound is clamped by
// readonlyRegionLines, which also writes the clamped value back to m.tempScroll.
func (m *Model) scrollTempPanel(delta, y int) bool {
	if m.tempActive || m.tempHeight < 1 {
		return false
	}
	row := y - 1 // mouse Y is 1-based, screen rows are 0-based
	if row < m.tempTop || row >= m.tempTop+m.tempHeight {
		return false
	}
	m.tempScroll += delta
	if m.tempScroll < 0 {
		m.tempScroll = 0
	}
	return true
}
