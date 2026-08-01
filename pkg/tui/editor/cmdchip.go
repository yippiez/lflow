package editor

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/lflow/lflow/pkg/tui/database"
)

// A cmd chip is inline runnable shell: a standalone "$" starts a live gray
// command draft inside any text node, and a DOUBLE space commits it (single
// spaces stay part of the command; a bare "$" still chips, empty). The command
// lives in the chip value (persisted); its run band is local-only in node_output
// (keyed by chip id — never synced, exactly like a bash node). alt+r runs the
// chip the caret sits on; the chip then renders "$ cmd → <first line>" from an
// in-memory label rehydrated on open via hydrateCmdPreviews; alt+e expands the
// full band.

// bashCmdBeforeCaret converts a "$<command>" token terminated by a double space
// into a cmd chip. It is called from the space-typed path only when the rune
// before the caret is already a space (so this is the second space). Returns true
// if it converted, in which case the committing space is consumed by the caller.
func (m *Model) bashCmdBeforeCaret(cur *item) bool {
	if cur == nil || cur.mirrorOf != "" || !typeOf(cur.typ).inlineEditable || cur.readonly {
		return false
	}
	runes := []rune(cur.name)
	// the caret sits just after the first of the two spaces.
	if m.caret < 2 || m.caret > len(runes) || runes[m.caret-1] != ' ' {
		return false
	}
	end := m.caret - 1 // command ends just before the trailing space
	spans := anchorSpans(runes)
	// walk back to the "$" that opens the command: it must be at the node start or
	// follow a space (a standalone token). Skip whole chip anchors so any chip
	// spliced into the command doesn't stop the scan — its sentinels would
	// otherwise read as stray markers and abort the token.
	start := -1
	for i := end - 1; i >= 0; {
		if sp := spanEndingAt(spans, i+1); sp != nil {
			i = sp.start - 1
			continue
		}
		if runes[i] == '$' && (i == 0 || runes[i-1] == ' ') {
			start = i
			break
		}
		i--
	}
	if start < 0 {
		return false
	}
	// expand any chips folded into the command (e.g. a tag chip → "#value") so
	// the cmd chip's stored command is plain, runnable shell. An EMPTY
	// command still chips: "$" + double space lands a blank $ chip to fill in.
	cmd := strings.TrimSpace(expandAnchors(string(runes[start+1:end]), m.chips))
	// those inner chips are now baked into the cmd value; drop their records before
	// their anchors are removed from the name, so no orphan chip rows are left.
	for _, sp := range spans {
		if sp.start >= start && sp.end <= end {
			m.deleteChipID(sp.id)
		}
	}
	if !m.replaceRangeWithChip(cur, start, end, chipKindCmd, cmd) {
		return false
	}
	// park the caret after the single space that remains past the new chip.
	r := []rune(cur.name)
	if m.caret < len(r) && r[m.caret] == ' ' {
		m.caret++
	}
	return true
}

// markCmdDraft snapshots the edit site after a text edit; renderBody shows the
// live cmd-chip draft tint only while the caret still sits there, so any caret
// move (navigation, node switch) ends the draft display without extra clearing.
func (m *Model) markCmdDraft(cur *item) {
	m.cmdDraftUUID, m.cmdDraftCaret = cur.uuid, m.caret
}

// cmdDraftLive reports whether the caret still sits where the last text edit
// on this node left it — the gate for painting a "$…" run as a live cmd draft.
func (m *Model) cmdDraftLive(it *item) bool {
	return it != nil && m.cmdDraftUUID == it.uuid && m.cmdDraftCaret == m.caret
}

// cmdChipAtCaret returns the cmd chip the caret sits on (its anchor begins at the
// caret, or ends exactly at it), or ok=false.
func (m *Model) cmdChipAtCaret(cur *item) (database.Chip, bool) {
	if cur == nil {
		return database.Chip{}, false
	}
	spans := anchorSpans([]rune(cur.name))
	for _, sp := range []*anchorSpan{spanStartingAt(spans, m.caret), spanEndingAt(spans, m.caret)} {
		if sp == nil {
			continue
		}
		if c, ok := m.chips[sp.id]; ok && c.Kind == chipKindCmd {
			return c, true
		}
	}
	return database.Chip{}, false
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
	preview := ""
	var out []outLine
	running := false
	if r := m.run(id); r != nil {
		out, running = r.lines(), r.cancel != nil
	}
	if running {
		// stream the tail: walk back from the newest line so the label tracks the
		// stream, and stop at the first line that has content (a trailing blank
		// line must not blank the preview).
		for i := len(out) - 1; i >= 0; i-- {
			if t := strings.TrimSpace(stripSGR(out[i].text)); t != "" {
				preview = t
				break
			}
		}
	} else {
		for _, l := range out {
			if t := strings.TrimSpace(stripSGR(l.text)); t != "" {
				preview = t
				break
			}
		}
	}
	c.Label = clipStr(preview, 32)
	m.chips[id] = c
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

func (cmdChipView) Enter(m *Model, it *item) bool { return m.focusChip != "" }

func (cmdChipView) Leave(m *Model, it *item) { m.focusChip, m.focusFollow = "", false }

func (cmdChipView) Lines(m *Model, it *item, width int) int {
	return runViewLines(m, m.focusChip)
}

// Key scrolls the band; alt+r re-runs (or cancels) the chip in place; esc/alt+e
// fall through to central defocus.
func (cmdChipView) Key(m *Model, it *item, k tea.KeyMsg) (tea.Cmd, bool) {
	if k.String() == "alt+r" {
		if c, ok := m.chips[m.focusChip]; ok && c.Kind == chipKindCmd {
			return m.runCmdChip(c), true
		}
	}
	return runViewKey(m, k)
}

// Bands renders the chip's terminal: the same shared surface a Bash node's
// expanded view uses, headed by the command itself.
func (cmdChipView) Bands(m *Model, it *item, rail string, width, scroll, winH int, focused bool) []string {
	m.ensureRunOutLoaded(m.focusChip)
	r := m.run(m.focusChip) // non-nil after ensureRunOutLoaded
	cmd := ""
	if c, ok := m.chips[m.focusChip]; ok {
		cmd = c.Value
	}
	hdr := fmt.Sprintf("  $ %s · %d lines", cmd, len(r.lines()))
	return runViewBands(m, r, hdr, "⌥r", rail, width, scroll, winH)
}
