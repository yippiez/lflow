package editor

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/lflow/lflow/pkg/tui/database"
)

// View implements tea.Model.
func (m *Model) View() string {
	width := m.width
	if width <= 0 {
		width = 80
	}
	maxLine := width - 1 // never touch the last column: deferred-wrap desync

	if m.quitting {
		if m.err != nil {
			return ""
		}
		// the final frame is what the terminal scrollback keeps: the whole
		// outline, fully expanded, styled exactly like the live editor. The
		// trailing newline matters: the renderer erases the last line of the
		// final frame on shutdown, so give it an empty one to eat.
		return strings.Join(m.finalView(maxLine), "\n") + "\n"
	}

	var lines []string

	if m.mode == modeFinder {
		lines = m.viewFinder(maxLine)
	} else if m.mode == modeLinkEdit {
		lines = m.viewLinkEdit(maxLine)
	} else {
		lines = m.viewOutline(maxLine)
	}

	// The inline renderer (no alt screen) can only move the cursor back over the
	// lines of the previous frame — it cannot reach into scrollback. A frame
	// taller than the terminal therefore strands its top lines: on the next
	// flush the renderer clears only what it last rendered, leaving the overflow
	// behind, which is what doubles the outline across a shrink-then-grow resize.
	// Cap every frame at the window height so each node renders exactly once.
	if m.height > 0 && len(lines) > m.height {
		lines = lines[:m.height]
	}

	// Erase the line before drawing it, not after. The inline renderer rewrites
	// lines in place without clearing, so a frame that grows after a shrink would
	// leave the previous narrower line's trailing cells behind the new one — the
	// 40-col and 60-col renders overlaid. cClearEOL erases from the cursor to the
	// end of the line; the renderer leaves the cursor at column 0 before painting
	// each row, so prefixing clears the whole row first. It must lead the line: a
	// full-width row is truncated to the terminal width by the renderer, and that
	// truncation drops any escape bytes past the cut — a trailing cClearEOL would
	// be silently discarded on exactly the wide rows that need clearing.
	//
	// A themed page background (bgPage) must paint REAL cells edge to edge —
	// the selFill trick: the renderer appends its own end-of-line erase after
	// each row, which would repaint an \x1b[K flood back to the terminal
	// default, leaving gray only behind the text. So each main-region line is
	// padded with spaces to full width under the bg, re-arming it after every
	// interior reset so mixed-color rows stay on the page. Only the main
	// region gets it — the first pageRows lines; the status bar and the temp
	// panel below always keep the terminal background.
	pageEnd := 0
	if bgPage != "" {
		pageEnd = min(m.pageRows, len(lines))
	}
	for i, l := range lines {
		if i < pageEnd {
			if pad := maxLine - visibleWidth(l); pad > 0 {
				l += strings.Repeat(" ", pad)
			}
			lines[i] = cClearEOL + bgPage + strings.ReplaceAll(l, cReset, cReset+bgPage) + cReset
			continue
		}
		lines[i] = cClearEOL + l + cReset
	}

	return strings.Join(lines, "\n")
}

// finalView renders the complete tree with glyphs and connectors but no
// cursor, caret or bottom bar. Long rows wrap.
func (m *Model) finalView(maxLine int) []string {
	var lines []string
	allRows := m.tree.allRows()
	for i, r := range allRows {
		below := i+1 < len(allRows) && allRows[i+1].depth > r.depth
		if r.it.typ == database.TypeDivider {
			lines = append(lines, dividerLine(r, maxLine, false))
			lines = append(lines, m.noteBandLines(r, maxLine, below, -1)...)
			continue
		}
		glyph, glyphColor := glyphFor(r.it)
		name := m.tree.displayName(r.it)
		body := renderBody(r.it, name, -1, false, m.chips, false)
		if rm := typeOf(r.it.typ).renderM; rm != nil {
			body = rm(m, r.it)
		}
		line := " " + cDim + connector(r) + glyphColor + glyph + cReset + " " + body + m.typeSuffix(r)
		lines = append(lines, wrapLine(line, maxLine, continuationPrefix(r, below))...)
		lines = append(lines, m.noteBandLines(r, maxLine, below, -1)...)
		if b := typeOf(r.it.typ).bands; b != nil {
			lines = append(lines, b(m, r, below, maxLine)...)
		}
	}
	return lines
}

func (m *Model) viewOutline(maxLine int) []string {
	groups, bands := m.viewRenderRows(maxLine)
	bar := m.bottomBar(maxLine)
	lay := m.viewBudgets(len(bar))
	m.viewFocusedBand(groups, bands, lay, maxLine)
	body := m.viewWindow(groups, bands, lay, maxLine)
	body = append(body, m.viewOverlays(lay, maxLine)...)
	return m.viewFrame(body, bar, lay, maxLine)
}

// viewLayout carries the height budgets computed once per frame from stages 2+4
// (viewBudgets) down into the stages that window and assemble the frame
// (viewFocusedBand, viewWindow, viewFrame).
type viewLayout struct {
	rowBudget     int
	mainBudget    int
	tempBudget    int
	focusedBudget int
	maxRows       int
	showTemp      bool
}

// viewRenderRows renders every row to its wrapped lines up front: the viewport
// then works in screen lines, so wrapped rows never push the cursor off screen.
// groups[i]/bands[i] index-align with m.rows[i].
func (m *Model) viewRenderRows(maxLine int) (groups, bands [][]string) {
	rows := m.rows
	groups = make([][]string, len(rows))
	bands = make([][]string, len(rows))
	for i, r := range rows {
		it := r.it
		selected := i == m.cursor

		// a divider is a full-width rule with no glyph/body; it still hangs a note
		if it.typ == database.TypeDivider {
			below := i+1 < len(rows) && rows[i+1].depth > r.depth
			groups[i] = []string{dividerLine(r, maxLine, selected && m.mode != modeFlash)} // single line, never wrapped
			if m.inSelection(i) {
				groups[i][0] = selFill(groups[i][0], maxLine)
			}
			noteCaret := -1
			if selected && m.mode == modeNote {
				noteCaret = m.caret
			}
			bands[i] = m.noteBandLines(r, maxLine, below, noteCaret)
			continue
		}

		// a code-block node renders AS the borderless block, standing in for its row
		// (no glyph/body line): the line-number gutter + code hang at the node's
		// indent. While focused the block carries the edit caret (viewFocusedBand
		// then skips it — the group already shows it).
		if bc := typeOf(it.typ).blockCode; bc != nil {
			if code, caret, ok := bc(m, it, selected && m.focused); ok {
				below := i+1 < len(rows) && rows[i+1].depth > r.depth
				inner := maxLine - visibleWidth(continuationPrefix(r, below))
				content := codeBlockLines(code, caret, inner)
				glyph, glyphColor := glyphFor(it)
				if m.tempActive && !r.mirrored {
					glyph = glyphDotted
				}
				if selected || m.inSelection(i) {
					glyphColor = cRed
				}
				groups[i] = m.blockGroupLines(r, content, below, glyphColor+glyph+cReset)
				if m.inSelection(i) {
					for j := range groups[i] {
						groups[i][j] = selFill(groups[i][j], maxLine)
					}
				}
				noteCaret := -1
				if selected && m.mode == modeNote {
					noteCaret = m.caret
				}
				bands[i] = m.noteBandLines(r, maxLine, below, noteCaret)
				continue
			}
		}

		glyph, glyphColor := glyphFor(it)
		if m.tempActive && !r.mirrored {
			glyph = glyphDotted // every Temporary Domain node shows a dashed icon
		}
		if selected || m.inSelection(i) {
			glyphColor = cRed
		}
		name := m.tree.displayName(it)

		caret := -1
		if selected && m.mode != modeNote && m.mode != modeFlash && it.mirrorOf == "" {
			caret = m.caret
		}
		body := renderBody(it, name, caret, selected, m.chips, m.cmdDraftLive(it))
		if rm := typeOf(it.typ).renderM; rm != nil {
			body = rm(m, it) // Model-aware override (voice waveform)
		}
		if it.queryGenerated() {
			if it.readonly {
				// Breadcrumb scaffolding is a real nested tree, but deliberately
				// gray and content-locked because it is context, not a result.
				body = cDim + stripSGR(body) + cReset
			} else {
				body = m.highlightQueryHit(it, name, body)
			}
		}

		suffix := m.typeSuffix(r)
		// flash mode grays the whole outline so the colored action chips are the only
		// highlights: dim the glyph, the body and the type suffix down to plain gray.
		if m.mode == modeFlash {
			glyphColor = cDim
			body = cDim + stripSGR(body) + cReset
			suffix = cDim + stripSGR(suffix) + cReset
		}
		line := " " + cDim + connector(r) + glyphColor + glyph + cReset + " " + body + suffix
		// flash mode hangs each row's action labels off the end of the line
		if m.mode == modeFlash {
			line += m.flashRowSuffix(i)
		}

		below := i+1 < len(rows) && rows[i+1].depth > r.depth
		groups[i] = wrapLine(line, maxLine, continuationPrefix(r, below))
		// the shift+↑/↓ selection reads as one solid block: every selected row
		// (wrapped continuations included) gets the full-width blue bar
		if m.inSelection(i) {
			for j, l := range groups[i] {
				groups[i][j] = selFill(l, maxLine)
			}
		}
		// the note hangs beneath the node as a tinted band; on the selected row in
		// note mode that same band becomes the editing surface (block cursor)
		noteCaret := -1
		if selected && m.mode == modeNote {
			noteCaret = m.caret
		}
		bands[i] = m.noteBandLines(r, maxLine, below, noteCaret)
		// runnable nodes (bash/query) hang their ephemeral output beneath them.
		// the focused bash node shows its full scrollable viewer (the nodeView band
		// below) instead of this capped inline band, so don't render both
		focusedView := m.focused && i == m.cursor && m.activeView(it) != nil
		if !focusedView {
			bands[i] = append(bands[i], m.runBandLines(r, below, maxLine)...)
			if b := typeOf(it.typ).bands; b != nil {
				bands[i] = append(bands[i], b(m, r, below, maxLine)...)
			}
			// a running @mention hangs its live "last tool call" line beneath it
			if t := m.thread(it.uuid); t != nil && t.busy {
				bands[i] = append(bands[i], m.agentBandLines(r, below, maxLine)...)
			}
		}
		// flash grays the note / run-output bands too, so nothing competes with the chips
		if m.mode == modeFlash {
			for k := range bands[i] {
				bands[i][k] = cDim + stripSGR(bands[i][k]) + cReset
			}
		}
	}
	return groups, bands
}

// viewBudgets computes the frame's height budgets (stage 2) and the picker /
// settings overlay carve-out that shrinks the body window (stage 4). barLines is
// len(m.bottomBar(...)) — the status bar can wrap to several lines.
func (m *Model) viewBudgets(barLines int) viewLayout {
	// The Temporary Domain panel is always visible during normal editing — only
	// modal overlays (slash menu, pickers, prompts) take the full body. Layout:
	// main notes on top, the status bar acting as the divider, then the
	// always-visible Temporary Domain panel below it. Below ~3 body rows there is
	// no room for that stack, so fall back to the plain outline.
	rowBudget := m.rowBudget()
	// The status bar wraps instead of truncating (see bottomBar), so its extra
	// lines come out of the body budget — the frame must never exceed the
	// terminal height, or the height cap would cut the bar off the bottom.
	rowBudget -= barLines - 1
	if rowBudget < 1 {
		rowBudget = 1
	}
	// A focused inline view takes the whole body (like a picker) — the temp split
	// is suppressed so a tall view (e.g. bash output) isn't crammed into the panel.
	showTemp := (m.mode == modeOutline || m.mode == modeNote) && rowBudget >= 3 && !m.focused
	tempBudget, mainBudget := 0, rowBudget
	if showTemp {
		m.ensureTempTree() // always-visible panel must exist before we render it
		tempBudget = m.tempPanelBudget(rowBudget)
		mainBudget = rowBudget - tempBudget
		if mainBudget < 1 {
			mainBudget = 1
			tempBudget = rowBudget - 1
		}
	}
	focusedBudget := mainBudget
	if showTemp && m.tempActive {
		focusedBudget = tempBudget
	}

	maxRows := focusedBudget
	// Pickers (slash menu, /type, /style) are modal overlays drawn above the status
	// bar. Each reserves a small, FIXED-height scrolling window by shrinking the body
	// budget, so the picker never takes over the screen — the outline stays visible
	// and the list scrolls to keep the selection in view. headerRows includes the
	// /type search header.
	pickerItems, headerRows := 0, 0
	if src := m.listSource(); src != nil {
		pickerItems, headerRows = m.list.counts(m, src)
	} else if m.mode == modeSettings {
		pickerItems = len(settingDefs)
	}
	pickerRows := 0
	if pickerItems > 0 || headerRows > 0 {
		win := pickerItems
		if win > pickerMaxRows {
			win = pickerMaxRows
		}
		pickerRows = win + headerRows
		if pickerRows > rowBudget-1 { // always leave at least one body row
			pickerRows = rowBudget - 1
		}
		if pickerRows < 1 {
			pickerRows = 1
		}
		maxRows = rowBudget - pickerRows
		if maxRows < 1 {
			maxRows = 1
		}
	}
	return viewLayout{
		rowBudget:     rowBudget,
		mainBudget:    mainBudget,
		tempBudget:    tempBudget,
		focusedBudget: focusedBudget,
		maxRows:       maxRows,
		showTemp:      showTemp,
	}
}

// viewFocusedBand appends the focused node's inline expanded view as a band
// beneath it (stage 3), self-windowed to the focused budget so the node header
// stays pinned above while a tall view (e.g. long bash output) scrolls within
// its window. Mutates bands[cursor] and clamps m.focusScroll.
func (m *Model) viewFocusedBand(groups, bands [][]string, lay viewLayout, maxLine int) {
	rows := m.rows
	if m.focused && m.cursor >= 0 && m.cursor < len(rows) {
		cur := rows[m.cursor].it
		// a code-block node draws its focused editor in the group (block replaces
		// the row), not as a hanging band — don't double-render it.
		if bc := typeOf(cur.typ).blockCode; bc != nil {
			if _, _, ok := bc(m, cur, true); ok {
				return
			}
		}
		if v := m.activeView(cur); v != nil {
			r := rows[m.cursor]
			below := m.cursor+1 < len(rows) && rows[m.cursor+1].depth > r.depth
			winH := lay.focusedBudget - len(groups[m.cursor]) - 1
			if winH < 1 {
				winH = 1
			}
			if total := v.Lines(m, cur, maxLine); m.focusScroll > total-winH {
				m.focusScroll = total - winH
			}
			if m.focusScroll < 0 {
				m.focusScroll = 0
			}
			bands[m.cursor] = append(bands[m.cursor],
				v.Bands(m, cur, continuationPrefix(r, below), maxLine, m.focusScroll, winH, true)...)
		}
	}
}

// viewWindow flattens the rendered rows to screen lines and slices out the
// visible window (stage 5): root-note prepend, cursor-follow vs pgup/pgdown
// scroll, and the m.viewTop/m.viewRows/m.scrollTop cache writes.
func (m *Model) viewWindow(groups, bands [][]string, lay viewLayout, maxLine int) []string {
	var lines []string
	rows := m.rows
	if len(rows) == 0 {
		lines = append(lines, cDim+" empty - type to add a node"+cReset)
	}
	maxRows := lay.maxRows
	cursorStart, cursorEnd := 0, 0
	var flat []string
	// the zoomed-in (view-root) node has no row of its own, so surface its note
	// as a band at the top of the view — the same band a row would hang below it.
	rootNote := m.noteBandLines(row{it: m.viewRoot(), depth: 0}, maxLine, false, -1)
	if m.mode == modeFlash {
		for k := range rootNote {
			rootNote[k] = cDim + stripSGR(rootNote[k]) + cReset
		}
	}
	flat = append(flat, rootNote...)
	for i := range groups {
		if i == m.cursor {
			cursorStart = len(flat)
			// scroll to keep the node itself in view, not the tail of its band —
			// except while editing the note, where the band is what needs to show
			cursorEnd = len(flat) + len(groups[i]) - 1
			// while editing the note, or while a focused inline view hangs beneath
			// the node, the band is what must stay on screen
			if m.mode == modeNote || m.focused {
				cursorEnd += len(bands[i])
			}
		}
		flat = append(flat, groups[i]...)
		flat = append(flat, bands[i]...)
	}
	start := 0
	if m.scrolling {
		// manual scroll (pgup/pgdown): pin the window at scrollTop, clamped to the
		// content, independent of where the cursor is.
		start = m.scrollTop
		if maxStart := len(flat) - maxRows; start > maxStart {
			start = maxStart
		}
		if start < 0 {
			start = 0
		}
		m.scrollTop = start
	} else {
		// follow the cursor, but keep the previous window when the cursor is already
		// fully visible in it — otherwise a pgdown that peeks past the cursor is
		// yanked back the moment the user types or moves within the page.
		start = m.viewTop
		if maxStart := len(flat) - maxRows; start > maxStart {
			start = maxStart
		}
		if start < 0 {
			start = 0
		}
		if cursorEnd >= start+maxRows {
			start = cursorEnd - maxRows + 1
		}
		if cursorStart < start {
			start = cursorStart
		}
	}
	m.viewTop, m.viewRows = start, maxRows // cache for pgup/pgdown stepping
	end := start + maxRows
	if end > len(flat) {
		end = len(flat)
	}
	lines = append(lines, flat[start:end]...)
	return lines
}

// viewOverlays draws the modal overlays that sit above the status bar (stage
// 6a): the delete-confirm prompt, the shared listPicker window, and the bespoke
// /settings rows.
func (m *Model) viewOverlays(lay viewLayout, maxLine int) []string {
	var lines []string

	// The delete confirm sits above the status line, not below it: the inline
	// renderer leaves a shrinking frame's old last line in place, so if the
	// confirm prompt were the final line, canceling it (one line shorter) would
	// strand the status bar blank until the next keypress repainted. Keeping the
	// bottomBar as every frame's last line means ESC-cancel restores it at once.
	if m.mode == modeConfirm {
		if cur := m.cursorItem(); cur != nil {
			// Build suffix-first: the count and keybind hints must never be clipped,
			// so reserve their width plus the fixed " delete " prefix and quotes,
			// then elide the middle of the name to fit whatever room is left.
			prefix := " " + cRed + "delete " + cReset
			suffix := cDim + fmt.Sprintf(" - %s - enter delete - esc keep", nodeNoun(subtreeSize(cur))) + cReset
			room := maxLine - visibleWidth(prefix) - visibleWidth(suffix) - 2 // 2 for the quotes
			name := elideMiddle(displayAnchors(m.tree.displayName(cur), m.chips), room)
			line := prefix + cYellow + fmt.Sprintf("%q", name) + cReset + suffix
			lines = append(lines, clip(line, maxLine))
		}
	}

	// The Group-A pickers (slash menu, /type, /style, /theme, completer) list their
	// options above the status line, same as the confirm prompt and for the same
	// reason: the inline renderer skips repainting an unchanged last line, so if the
	// bottomBar were the final line with the menu below it, dismissing the menu
	// (Backspace on an empty query) would shrink the frame without moving the bar's
	// row — the renderer would skip the bar and then erase-below it, blanking the
	// status bar for a frame. Listing the menu above the bar shifts the bar's row
	// when the menu vanishes, which forces the repaint. The shared listPicker
	// renders a bounded, scrolling window (see picker_list.go).
	if src := m.listSource(); src != nil {
		lines = append(lines, m.list.render(m, src, maxLine)...)
	}

	if m.mode == modeSettings {
		lines = append(lines, m.viewSettings(maxLine)...)
	}
	return lines
}

// viewSettings renders the /settings picker: one row per preference as
// `label · value` — muted label, middle dot, value colored by settingValueColor
// (green affirmative, red negative). The theme row previews the selected palette
// as a swatch strip so colors are visible before committing. It keeps its own
// bespoke mode (not a listPicker) because left/right cycles a value in place
// rather than picking one option and closing.
func (m *Model) viewSettings(maxLine int) []string {
	var lines []string
	win := pickerMaxRows
	s2 := scrollStart(m.settingsSel, len(settingDefs), win)
	e2 := s2 + win
	if e2 > len(settingDefs) {
		e2 = len(settingDefs)
	}
	for i := s2; i < e2; i++ {
		d := settingDefs[i]
		val := m.setting(d.key)
		mark := "  "
		if i == m.settingsSel {
			mark = cAccent + "→ " + cReset // one joined arrow, not "-" + ">"
		}
		value := settingValueColor(val) + settingValueLabel(d, val) + cReset
		extra := ""
		if d.key == "theme" {
			if t, ok := themeByName(val); ok {
				extra = "  " + t.accent + "●" + t.red + "●" + t.yellow + "●" +
					t.green + "●" + t.cyan + "●" + t.purple + "●" + cReset
			}
		}
		line := " " + mark + cDim + fmt.Sprintf("%-14s", d.label) + "· " + cReset + value + extra
		lines = append(lines, clip(line, maxLine))
	}
	return lines
}

// viewFrame assembles the final frame (stage 6b). In showTemp mode it stacks the
// three regions — main notes, the status bar acting as the divider, then the
// Temporary Domain panel — padded to a constant height. Otherwise it pads the
// focused/plain body and appends the bar as the last line. Both branches write
// m.pageRows. body is the windowed body (viewWindow + viewOverlays); bar is
// m.bottomBar(...).
func (m *Model) viewFrame(body, bar []string, lay viewLayout, maxLine int) []string {
	// Assemble the body: main notes, then the status bar (which doubles as the
	// divider), then the Temporary Domain panel below it. `body` here is the
	// focused region's body (no modal menus are open in showTemp modes). The frame
	// is padded to a constant height so the inline renderer never strands stale
	// lines despite the status bar sitting mid-frame.
	if lay.showTemp {
		focused := body
		if len(focused) > lay.focusedBudget {
			focused = focused[:lay.focusedBudget]
		}
		var mainLines, tempLines []string
		if m.tempActive {
			// guard a malformed stash: a nil tree or empty view-stack must degrade to a
			// blank region, never panic on the slice index.
			var mainRoot *item
			if n := len(m.mainStash.viewStack); n > 0 {
				mainRoot = m.mainStash.viewStack[n-1]
			}
			mainLines = m.readonlyRegionLines(m.mainStash.tree, mainRoot, m.mainStash.cursor, lay.mainBudget, maxLine, false)
			tempLines = focused // live, focused temp
		} else {
			mainLines = focused // live, focused main
			// read-only temp panel: the dashed-glyph Temporary Domain look
			tempLines = m.readonlyRegionLines(m.tempTree, m.tempTree.root, 0, lay.tempBudget, maxLine, true)
		}
		// NOTE: the page background never adds filler rows — the layout (where
		// the divider sits) must be identical across themes; gray paints
		// exactly the rows the main region already has, edge to edge.
		out := mainLines
		m.pageRows = len(out)     // page bg stops at the divider; temp stays bare
		out = append(out, bar...) // the status bar is the divider
		out = append(out, tempLines...)
		total := lay.rowBudget + len(bar) // main + status + temp, fixed for a stable frame
		for len(out) < total {
			out = append(out, "")
		}
		if len(out) > total {
			out = out[:total]
		}
		return out
	}

	// A focused inline view (alt+e on a bash/json/agent node) replaces the temp
	// split, so it takes this non-showTemp path instead of the padded block above
	// — and its body is only as tall as the expanded content. Pad it to the same
	// constant height the showTemp frame uses (rowBudget body rows + the status
	// bar) so toggling the view never changes the frame height, keeping the status
	// bar the last line. Without this the frame oscillates between the tall padded
	// outline and the short expanded view on every alt+e/esc; on a terminal whose
	// frame sits near the bottom, the grow half of that cycle scrolls rows the
	// inline renderer can no longer reach up to, stranding a ghost line below and
	// pushing the outline up one row each toggle (the bleed).
	if m.focused {
		for len(body) < lay.rowBudget {
			body = append(body, "")
		}
	}

	m.pageRows = len(body) // page bg covers everything above the status bar
	body = append(body, bar...)

	return body
}

// WARNING (invariant): the bottom/status bar is the LAST rendered line of every
// frame — tooling and the inline renderer treat the final line as the status line.
// Always append it last (see viewOutline); never emit content below it. The bar
// wraps instead of truncating, so it may span several lines; callers must budget
// for len(bottomBar(...)) lines, keeping the wrapped tail the frame's last line.
func (m *Model) bottomBar(maxLine int) []string {
	// The toolbar always describes the main outline. Entering the Temporary Domain
	// must not rewrite it to "Temp · 1/1" — the temp panel is a side region, not a
	// different page, so breadcrumb and cursor position stay on the main stash.
	pos, total := m.cursor+1, len(m.rows)
	if len(m.rows) == 0 {
		pos = 0
	}
	viewStack, ancestors := m.viewStack, m.ancestors
	dispTree := m.tree
	if m.tempActive && m.mainStash.tree != nil {
		dispTree = m.mainStash.tree
		viewStack = m.mainStash.viewStack
		ancestors = m.mainStash.ancestors
		pos = m.mainStash.cursor + 1
		total = 0
		if n := len(viewStack); n > 0 && viewStack[n-1] != nil {
			total = len(dispTree.visibleRows(viewStack[n-1], m.hideCompleted, m.unroll))
		}
		if total == 0 {
			pos = 0
		}
	}
	state := ""
	if m.unsaved {
		// with a daemon the edits auto-flush in ~1s: the moment is "syncing",
		// not a warning about unsaved work
		if m.live != nil {
			state = " · syncing"
		} else {
			state = " · unsaved"
		}
	}
	if m.selOn {
		lo, hi := m.selectionBounds()
		state += fmt.Sprintf(" · "+cRed+"%d selected"+cDim, hi-lo+1)
	}
	// the agent signals the bar carries, in the same slot: how many agents are
	// thinking, or the last failure. No install/reply/progress chatter — the
	// outline itself shows results.
	if n := m.busyThreadCount() + m.computingNodeCount(); n > 0 {
		state += fmt.Sprintf(" · "+cRed+"%d thinking"+cDim, n)
	}
	if m.agentErr != "" {
		// lowercase label; the message itself already starts capital (error
		// strings are capitalized at the source — see rules/capitalize-error-strings)
		state += " · " + cRed + "error: " + m.agentErr + cDim
	}
	if m.flash != "" {
		state += " · " + m.flash
	}
	if m.mode == modeFlash {
		hint := cFG + "flash" + cReset + cDim
		if m.flashInput != "" {
			hint += " " + cFG + m.flashInput + cReset + cDim
		}
		state += " · " + hint + " · esc cancel"
	}
	// offer the date conversion while a non-canonical time phrase sits under the
	// cursor; an already-canonical date needs no conversion and is chipped as-is.
	// while in temp the bar describes the main outline, so skip temp-cursor phrases
	if m.mode == modeOutline && !m.tempActive {
		if cur := m.cursorItem(); cur != nil && cur.mirrorOf == "" {
			if d := detectDate(cur.name, m.caret, time.Now()); d != nil && d.phrase != d.canonical() {
				// the date picker hint reads white against the dim status bar, then
				// hands the color back so the rest of the bar stays muted
				state += fmt.Sprintf(" · "+cFG+"ctrl+t %q → %s"+cDim, d.phrase, d.canonical())
			}
		}
	}
	// breadcrumb: the forest path down to the current view root (main outline,
	// even while the Temporary Domain holds focus)
	parts := append([]string(nil), ancestors...)
	for _, v := range viewStack {
		name := displayAnchors(dispTree.displayName(v), m.chips) // resolve chip anchors
		if name == "" {
			name = "untitled"
		}
		parts = append(parts, name)
	}
	title := strings.Join(parts, " › ")
	if title == "" {
		title = "untitled"
	}
	bar := fmt.Sprintf(" %s · %d/%d", title, pos, total)
	// process cwd, last two path segments — pure os.Getwd, never stored. $ and
	// @ runs inherit this same directory at alt+r (see startBash / CLIClient).
	if pwd, err := os.Getwd(); err == nil {
		if short := cwdShort(pwd); short != "" {
			bar += " · cwd " + short
		}
	}
	bar += state
	return wrapSGR(cDim+bar+cReset, maxLine)
}

// cwdShort renders a working directory for the status bar: the last two path
// segments, with a leading "…/" when the path is deeper. Short paths (root,
// one segment, or exactly two) are shown as-is.
//
//	/home/eren/work2/lflow → …/work2/lflow
//	/home/eren            → /home/eren
//	/tmp                  → /tmp
func cwdShort(pwd string) string {
	pwd = filepath.Clean(pwd)
	if pwd == "" || pwd == "." {
		return ""
	}
	const sep = string(filepath.Separator)
	parts := strings.Split(pwd, sep)
	var segs []string
	for _, p := range parts {
		if p != "" {
			segs = append(segs, p)
		}
	}
	if len(segs) == 0 {
		return sep // filesystem root
	}
	abs := strings.HasPrefix(pwd, sep)
	join := func(ss []string) string {
		s := strings.Join(ss, sep)
		if abs {
			return sep + s
		}
		return s
	}
	if len(segs) <= 2 {
		return join(segs)
	}
	return "…/" + segs[len(segs)-2] + "/" + segs[len(segs)-1]
}
