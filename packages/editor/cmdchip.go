package editor

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/lflow/lflow/packages/database"
)

// A cmd chip is inline runnable shell: "$$" lands an empty $ chip anywhere (a
// single "$" is always literal — $i, $(…), $HOME are shell syntax, never a
// chip), and /insert → cmd is the other way in. The command lives in the chip
// value (persisted); its run band is local-only in node_output (keyed by chip id
// — never synced, exactly like a bash node). alt+i edits the command, alt+r
// runs the chip the caret sits on; the chip then renders "$ cmd → <first line>"
// from an in-memory label rehydrated on open via hydrateCmdPreviews; alt+e
// expands the full band.

// bashLiteralRow reports whether this row is a Bash node — one whose text IS a
// shell command. Such a row treats its shell punctuation as literal: "$", "#"
// comments and dates are the command's own syntax, never auto-chipped (a bash
// tree composes its command from the raw text). Chips still land via explicit
// insertion — "$$", /insert and the [[ finder — but typing never invents them.
func bashLiteralRow(it *item) bool {
	return it != nil && it.typ == database.TypeBash
}

// cmdChipAtCaret returns the cmd chip the caret sits on (its anchor begins at the
// caret, or ends exactly at it), or ok=false.
func (m *Model) cmdChipAtCaret(cur *item) (database.Chip, bool) {
	return m.chipAtCaret(cur, chipKindCmd)
}

// runCmdChip runs (or cancels a running) cmd chip: the exact toggle and launch
// path a Bash node takes (runShell — a PTY + a terminal screen), keyed by chip
// id instead of node uuid. The previous run's → tail would otherwise sit there
// looking like this run's result until the first line arrives; refresh it, the
// shimmer says "working".
func (m *Model) runCmdChip(c database.Chip) tea.Cmd {
	cmd := runShell(m, c.ID, c.Value)
	m.setCmdPreview(c.ID)
	return cmd
}

// setCmdPreview refreshes a cmd chip's inline preview — its in-memory label. A
// finished band settles on its FIRST non-blank line (the run's headline); a band
// still running shows its NEWEST non-blank line instead, so a long-running
// command streams its progress in place on the row. The label is mutated in
// m.chips only and never upserted into the chips table: the command persists, the
// → chrome does not (WARNING (invariant): run output is never synced). The full
// band lives in local node_output; ensureRunOutLoaded rehydrates it so a reopen
// can rebuild this label without re-running.
func (m *Model) setCmdPreview(id string) {
	c, ok := m.chips[id]
	if !ok || c.Kind != chipKindCmd {
		return
	}
	m.ensureRunOutLoaded(id)
	c.Label = runHeadline(m.run(id))
	m.chips[id] = c
}

// runHeadline is the one-line summary of a run that both shell surfaces hang
// after their "→": a band still running shows its NEWEST non-blank line, so a
// long command streams its progress in place; a finished band settles on its
// FIRST non-blank line, the run's headline. Clipped to a fixed width so a
// torrent of output cannot rewrap the row it sits on. Runs of spaces are
// collapsed to one (a wide ASCII-art or columned line would otherwise crowd the
// row's limited preview width) — only in this preview; the band keeps its own
// spacing.
func runHeadline(r *runState) string {
	if r == nil {
		return ""
	}
	out := r.lines()
	pick := func(i int) string { return collapseSpaces(stripSGR(out[i].text)) }
	if r.cancel != nil {
		// walk back from the newest line, stopping at the first with content (a
		// trailing blank line must not blank the preview)
		for i := len(out) - 1; i >= 0; i-- {
			if t := pick(i); t != "" {
				return clipStr(t, runTailWidth)
			}
		}
		return ""
	}
	for i := range out {
		if t := pick(i); t != "" {
			return clipStr(t, runTailWidth)
		}
	}
	return ""
}

// collapseSpaces trims a line and runs every run of whitespace together into one
// space — the preview's space saver, so "grill-me␣␣␣␣neural_netwo…" reads as
// "grill-me neural_netwo…". Only display: the stored output line is untouched.
func collapseSpaces(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// ── live run feedback ────────────────────────────────────────────────────────

// liveCmdRuns is the render-time set of cmd chips with a command in flight, so a
// running chip can be painted with the shimmering code cell. It is a render-time
// global for the same reason animFrame is: renderBody/renderCmdChip are pure
// functions of (item, name, chips) reached from several surfaces (outline, temp
// panel, final frame, tests), and threading a model handle through all of them
// just to answer "is this chip running" would touch every caller. syncLiveCmdRuns
// refreshes it once per frame, and both it and the readers run on bubbletea's
// single event-loop goroutine, so no synchronization is needed.
var liveCmdRuns map[string]bool

// syncLiveCmdRuns snapshots which cmd chips are running, for this frame's render.
func (m *Model) syncLiveCmdRuns() {
	live := map[string]bool{}
	for id, r := range m.runs {
		if r.cancel == nil {
			continue
		}
		if c, ok := m.chips[id]; ok && c.Kind == chipKindCmd {
			live[id] = true
		}
	}
	liveCmdRuns = live
}

// cmdChipRunning reports whether a chip id is mid-run for this frame's render.
func cmdChipRunning(id string) bool { return liveCmdRuns[id] }

// A running cmd chip claims NO space under its row: a collapsed chip streams
// entirely on the row itself — the shimmering cell says it is working and the →
// tail carries the newest line — and the output band belongs to the expanded view
// (alt+e, cmdChipView), which streams the same live feed. So a run never pushes
// the outline around, and the row's height is the same whether it is running or
// idle.

// hydrateCmdPreviews rebuilds every cmd chip's in-memory → preview from local
// node_output (via setCmdPreview). Called after LoadChips — on editor open and
// after an aux reload that replaces m.chips — so reopening a client restores
// last-run chrome without re-running and without writing the chip row.
func (m *Model) hydrateCmdPreviews() {
	for id, c := range m.chips {
		if c.Kind != chipKindCmd {
			continue
		}
		m.setCmdPreview(id)
	}
}

// stripSGR removes ANSI escapes from s, leaving plain text — used to derive a
// clean inline preview from coloured command output. Command output can carry
// OSC 8 hyperlinks too (ls --hyperlink), so it scans with the shared,
// OSC-aware ansiEscapeEnd rather than running to the next 'm'.
func stripSGR(s string) string {
	if !strings.ContainsRune(s, '\x1b') {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); {
		if s[i] == '\x1b' {
			i = ansiEscapeEnd(s, i)
			continue
		}
		j := i
		for j < len(s) && s[j] != '\x1b' {
			j++
		}
		b.WriteString(s[i:j])
		i = j
	}
	return b.String()
}

// ── alt+i: the cmd-chip editor (modeCmdEdit) ──────────────────────────────

// A cmd chip is "$ command" — one editable field: the command. alt+e shows the
// run output, alt+i edits the command itself, the one edit a $ chip allows (a
// chip's anchors make inline editing impossible, and delete would drop the whole
// chip — the user has to be able to change what a "$" runs).
func (m *Model) openCmdEdit(c database.Chip) {
	if c.Kind != chipKindCmd && c.Kind != chipKindElement {
		return
	}
	m.mode = modeCmdEdit
	m.cmdEditID = c.ID
	m.cmdEditValue = c.Value
	m.cmdEditCaret = len([]rune(c.Value))
}

// saveCmdEdit writes the edited command back to the chip row and store.
func (m *Model) saveCmdEdit() {
	c, ok := m.chips[m.cmdEditID]
	if !ok {
		return
	}
	c.Value = strings.TrimSpace(m.cmdEditValue)
	if c.Kind == chipKindElement {
		c.Label = elementChipLabel(c.Value)
	}
	m.chips[c.ID] = c
	if m.ctx.DB != nil {
		_ = database.UpsertChip(m.ctx.DB, c)
	}
	// the command changed: drop the stale run band AND its in-memory preview so
	// the old result does not read as this command's output
	m.deleteRunOut(c.ID)
	m.setCmdPreview(c.ID)
	m.unsaved = true
}

// handleCmdEditKey edits the command with the same caret vocabulary as the
// outline editor, exactly like the link-chip editor.
func (m *Model) handleCmdEditKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "esc":
		m.mode = modeOutline
		return m, nil
	case "enter":
		m.saveCmdEdit()
		m.mode = modeOutline
		m.refreshRows()
		return m, nil
	}
	f := textField{value: m.cmdEditValue, caret: m.cmdEditCaret}
	if f.handleKey(k) {
		m.cmdEditValue = f.value
		m.cmdEditCaret = f.caret
	}
	return m, nil
}

func (m *Model) viewCmdEdit(maxLine int) []string {
	title, mark := " edit command", " $ "
	if c, ok := m.chips[m.cmdEditID]; ok && c.Kind == chipKindElement {
		title, mark = " edit element · tag and attributes", " <> "
	}
	var lines []string
	lines = append(lines, clip(cDim+title+cReset, maxLine))
	lines = append(lines, clip(cAccent+mark+cReset+cFG+withCaret(m.cmdEditValue, m.cmdEditCaret)+cReset, maxLine))
	lines = append(lines, "")
	lines = append(lines, clip(cDim+" enter save · esc cancel"+cReset, maxLine))
	m.pageRows = len(lines) // no status bar here — the whole frame is main region
	return lines
}

// ── alt+e inline cmd-output view ─────────────────────────────────────────────

// cmdChipView is a cmd chip's inline expanded output viewer: the same
// band-beneath-the-node surface as a focused bash node (see runOutView), keyed
// by the focused chip id (m.focusChip) instead of the node uuid. Stateless like
// every nodeView; it is reached through m.activeView, never the type registry —
// the chip lives inside a plain text node whose type has no view of its own.
type cmdChipView struct{}

// focusCmdChip focuses the chip's inline output band (alt+e on a cmd chip).
func (m *Model) focusCmdChip(c database.Chip) {
	m.focusChip = c.ID
	m.focused = true
	m.focusScroll, m.focusFollow = 0, true // open at the tail, following output
	m.ensureRunOutLoaded(c.ID)
}

// activeView resolves the focused inline view: a focused chip's own band (the
// cmd chip's run output, a session chip's transcript), else the node type's view.
func (m *Model) activeView(it *item) nodeView {
	if m.focusChip != "" {
		if c, ok := m.chips[m.focusChip]; ok && c.Kind == chipKindAgent {
			return agentChipView{}
		}
		return cmdChipView{}
	}
	return nodeViewOf(it)
}

func (cmdChipView) enter(m *Model, it *item) bool { return m.focusChip != "" }

func (cmdChipView) leave(m *Model, it *item) { m.focusChip, m.focusFollow = "", false }

func (cmdChipView) lines(m *Model, it *item, width int) int {
	return runViewLines(m, m.focusChip)
}

// Key scrolls the band; alt+r re-runs (or cancels) the chip in place; esc/alt+e
// fall through to central defocus.
func (cmdChipView) key(m *Model, it *item, k tea.KeyMsg) (tea.Cmd, bool) {
	if k.String() == "alt+r" {
		if c, ok := m.chips[m.focusChip]; ok && c.Kind == chipKindCmd {
			return m.runCmdChip(c), true
		}
	}
	return runViewKey(m, k)
}

// Bands renders the chip's terminal: the same shared surface a Bash node's
// expanded view uses, headed by the command itself.
func (cmdChipView) bands(m *Model, it *item, rail string, width, scroll, winH int, focused bool) []string {
	m.ensureRunOutLoaded(m.focusChip)
	r := m.run(m.focusChip) // non-nil after ensureRunOutLoaded
	cmd := ""
	if c, ok := m.chips[m.focusChip]; ok {
		cmd = c.Value
	}
	hdr := fmt.Sprintf("  $ %s · %d lines", cmd, len(r.lines()))
	return runViewBands(m, r, hdr, "⌥r", rail, width, scroll, winH)
}
