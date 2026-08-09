package editor

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/lflow/lflow/tui/database"
)

// A bash chip is inline runnable shell: "$$" lands an empty $ chip anywhere (a
// single "$" is always literal — $i, $(…), $HOME are shell syntax, never a
// chip), and /insert → cmd is the other way in. The command lives in the chip
// value (persisted); its run band is local-only in node_output (keyed by chip id
// — never synced, exactly like a bash node). alt+i edits the command, alt+r
// runs the chip the caret sits on; the chip then renders "$ cmd → <first line>"
// from an in-memory label rehydrated on open via hydrateBashChipPreviews; alt+e
// expands the full band.

// bashLiteralRow reports whether this row is a Bash node — one whose text IS a
// shell command. Such a row treats its shell punctuation as literal: "$", "#"
// comments and dates are the command's own syntax, never auto-chipped (a bash
// tree composes its command from the raw text). Chips still land via explicit
// insertion — "$$", /insert and the [[ finder — but typing never invents them.
func bashLiteralRow(it *item) bool {
	return it != nil && it.typ == database.TypeBash
}

// bashChipAtCaret returns the bash chip the caret sits on (its anchor begins at the
// caret, or ends exactly at it), or ok=false.
func (m *Model) bashChipAtCaret(cur *item) (database.Chip, bool) {
	return m.chipAtCaret(cur, chipKindBash)
}

// runBashChip runs (or cancels a running) bash chip: the exact toggle and launch
// path a Bash node takes (runShell — a PTY + a terminal screen), keyed by chip
// id instead of node uuid. The previous run's → tail would otherwise sit there
// looking like this run's result until the first line arrives; refresh it, the
// shimmer says "working".
func (m *Model) runBashChip(c database.Chip) tea.Cmd {
	cmd := runShell(m, c.ID, c.Value)
	m.setBashChipPreview(c.ID)
	return cmd
}

// setBashChipPreview refreshes a bash chip's inline preview — its in-memory label. A
// finished band settles on its FIRST non-blank line (the run's headline); a band
// still running shows its NEWEST non-blank line instead, so a long-running
// command streams its progress in place on the row. The label is mutated in
// m.chips only and never upserted into the chips table: the command persists, the
// → chrome does not (WARNING (invariant): run output is never synced). The full
// band lives in local node_output; ensureRunOutLoaded rehydrates it so a reopen
// can rebuild this label without re-running.
func (m *Model) setBashChipPreview(id string) {
	c, ok := m.chips[id]
	if !ok || c.Kind != chipKindBash {
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

// liveBashChipRuns is the render-time set of bash chips with a command in flight, so a
// running chip can be painted with the shimmering code cell. It is a render-time
// global for the same reason animFrame is: renderBody/renderBashChip are pure
// functions of (item, name, chips) reached from several surfaces (outline, temp
// panel, final frame, tests), and threading a model handle through all of them
// just to answer "is this chip running" would touch every caller. syncLiveBashChipRuns
// refreshes it once per frame, and both it and the readers run on bubbletea's
// single event-loop goroutine, so no synchronization is needed.
var liveBashChipRuns map[string]bool

// syncLiveBashChipRuns snapshots which bash chips are running, for this frame's render.
func (m *Model) syncLiveBashChipRuns() {
	live := map[string]bool{}
	for id, r := range m.runs {
		if r.cancel == nil {
			continue
		}
		if c, ok := m.chips[id]; ok && c.Kind == chipKindBash {
			live[id] = true
		}
	}
	liveBashChipRuns = live
}

// bashChipRunning reports whether a chip id is mid-run for this frame's render.
func bashChipRunning(id string) bool { return liveBashChipRuns[id] }

// A running bash chip claims NO space under its row: a collapsed chip streams
// entirely on the row itself — the shimmering cell says it is working and the →
// tail carries the newest line — and the output band belongs to the expanded view
// (alt+e, bashChipView), which streams the same live feed. So a run never pushes
// the outline around, and the row's height is the same whether it is running or
// idle.

// hydrateBashChipPreviews rebuilds every bash chip's in-memory → preview from local
// node_output (via setBashChipPreview). Called after LoadChips — on editor open and
// after an aux reload that replaces m.chips — so reopening a client restores
// last-run chrome without re-running and without writing the chip row.
func (m *Model) hydrateBashChipPreviews() {
	for id, c := range m.chips {
		if c.Kind != chipKindBash {
			continue
		}
		m.setBashChipPreview(id)
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

// ── alt+i: the chip-field editor (chipEditView) ─────────────────────────────

// A bash chip is "$ command" and an element chip is "<tag attrs>" — either way
// one editable field. alt+e shows a bash chip's run output, alt+i edits the
// field itself, the one edit either chip allows (a chip's anchors make inline
// editing impossible, and delete would drop the whole chip — the user has to
// be able to change what a "$" runs or what an element's tag/attrs are).
func (m *Model) openChipEdit(c database.Chip) {
	if c.Kind != chipKindBash && c.Kind != chipKindElement {
		return
	}
	m.focusChip = c.ID
	m.focusChipEditing = true
	m.focused = true
	m.chipEditID = c.ID
	m.chipEditValue = c.Value
	m.chipEditCaret = len([]rune(c.Value))
}

// saveChipEdit writes the edited command back to the chip row and store.
func (m *Model) saveChipEdit() {
	c, ok := m.chips[m.chipEditID]
	if !ok {
		return
	}
	c.Value = strings.TrimSpace(m.chipEditValue)
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
	m.setBashChipPreview(c.ID)
	m.unsaved = true
}

// handleChipEditKey edits the command with the same caret vocabulary as the
// outline editor, exactly like the link-chip editor.
func (m *Model) handleChipEditKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "esc":
		m.focusChip = ""
		m.focusChipEditing = false
		m.focused = false
		return m, nil
	case "enter":
		m.saveChipEdit()
		m.focusChip = ""
		m.focusChipEditing = false
		m.focused = false
		m.refreshRows()
		return m, nil
	}
	f := textField{value: m.chipEditValue, caret: m.chipEditCaret}
	if f.handleKey(k) {
		m.chipEditValue = f.value
		m.chipEditCaret = f.caret
	}
	return m, nil
}

// chipEditView renders the alt+i field editor — a bash chip's command or an
// element chip's tag/attrs — as a band beneath the chip's own row, the same
// surface a bash chip's run output (bashChipView) uses, never a separate
// full-screen page. Stateless like every nodeView; the working copy lives in
// m.chipEdit*, focused via m.focusChip + m.focusChipEditing (the flag that
// tells activeView this is the field editor, not a bash chip's output band).
type chipEditView struct{}

func (chipEditView) enter(m *Model, it *item) bool { return m.focusChip != "" }

// leave only drops focus — esc is a cancel, not a save (enter is the only save
// path; see handleChipEditKey).
func (chipEditView) leave(m *Model, it *item) {
	m.focusChip = ""
	m.focusChipEditing = false
}

func (chipEditView) lines(m *Model, it *item, width int) int { return 3 }

// Key delegates to the field-editing vocabulary shared with every alt+e/alt+i
// editor; it always swallows (the editor owns every key while it is up, same
// as before this became a band).
func (chipEditView) key(m *Model, it *item, k tea.KeyMsg) (tea.Cmd, bool) {
	_, cmd := m.handleChipEditKey(k)
	return cmd, true
}

// Bands renders the one-field editor, self-windowed to [scroll, scroll+winH)
// like every other band even though its 2 lines never scroll in practice.
func (chipEditView) bands(m *Model, it *item, rail string, width, scroll, winH int, focused bool) []string {
	title, mark := "edit command", " $ "
	if c, ok := m.chips[m.chipEditID]; ok && c.Kind == chipKindElement {
		title, mark = "edit element · tag and attributes", " <> "
	}
	pad := rail + cReset + "  "
	content := []string{
		clip(pad+cDim+title+cReset, width),
		clip(pad+cAccent+mark+cReset+cFG+withCaret(m.chipEditValue, m.chipEditCaret)+cReset, width),
		clip(pad+cDim+"enter save · esc cancel"+cReset, width),
	}
	if scroll > len(content) {
		scroll = len(content)
	}
	end := scroll + winH
	if end > len(content) {
		end = len(content)
	}
	return content[scroll:end]
}

// ── alt+e inline cmd-output view ─────────────────────────────────────────────

// bashChipView is a bash chip's inline expanded output viewer: the same
// band-beneath-the-node surface as a focused bash node (see runOutView), keyed
// by the focused chip id (m.focusChip) instead of the node uuid. Stateless like
// every nodeView; it is reached through m.activeView, never the type registry —
// the chip lives inside a plain text node whose type has no view of its own.
type bashChipView struct{}

// focusBashChip focuses the chip's inline output band (alt+e on a bash chip).
func (m *Model) focusBashChip(c database.Chip) {
	m.focusChip = c.ID
	m.focusChipEditing = false
	m.focused = true
	m.focusScroll, m.focusFollow = 0, true // open at the tail, following output
	m.ensureRunOutLoaded(c.ID)
}

// activeView resolves the focused inline view: a focused chip's own band —
// its run output (cmd), its name+target editor (link) or its name+color
// editor (session) — else the node type's view.
func (m *Model) activeView(it *item) nodeView {
	if m.focusChip != "" {
		switch m.chips[m.focusChip].Kind {
		case chipKindLink:
			return linkEditView{}
		case chipKindAgent:
			return agentEditView{}
		case chipKindElement:
			return chipEditView{} // an element chip only ever opens its field editor
		case chipKindBash:
			if m.focusChipEditing {
				return chipEditView{} // alt+i: the command field
			}
			return bashChipView{} // alt+e: the run output
		}
	}
	return nodeViewOf(it)
}

func (bashChipView) enter(m *Model, it *item) bool { return m.focusChip != "" }

func (bashChipView) leave(m *Model, it *item) { m.focusChip, m.focusFollow = "", false }

func (bashChipView) lines(m *Model, it *item, width int) int {
	return runViewLines(m, m.focusChip)
}

// Key scrolls the band; alt+r re-runs (or cancels) the chip in place; esc/alt+e
// fall through to central defocus.
func (bashChipView) key(m *Model, it *item, k tea.KeyMsg) (tea.Cmd, bool) {
	if k.String() == "alt+r" {
		if c, ok := m.chips[m.focusChip]; ok && c.Kind == chipKindBash {
			return m.runBashChip(c), true
		}
	}
	return runViewKey(m, k)
}

// Bands renders the chip's terminal: the same shared surface a Bash node's
// expanded view uses, headed by the command itself.
func (bashChipView) bands(m *Model, it *item, rail string, width, scroll, winH int, focused bool) []string {
	m.ensureRunOutLoaded(m.focusChip)
	r := m.run(m.focusChip) // non-nil after ensureRunOutLoaded
	cmd := ""
	if c, ok := m.chips[m.focusChip]; ok {
		cmd = c.Value
	}
	hdr := fmt.Sprintf("  $ %s · %d lines", cmd, len(r.lines()))
	return runViewBands(m, r, hdr, "⌥r", rail, width, scroll, winH)
}
