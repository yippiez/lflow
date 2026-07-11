package editor

import (
	"context"
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
	// follow a space (a standalone token). Skip whole chip anchors so a path chip
	// (or any chip) spliced into the command doesn't stop the scan — its sentinels
	// would otherwise read as stray markers and abort the token.
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
	// expand any chips folded into the command (e.g. a path chip → its full path)
	// so the cmd chip's stored command is plain, runnable shell. An EMPTY
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

// runCmdChip runs (or cancels a running) cmd chip. Output streams into the run
// band keyed by the chip id, mirroring runBash; a second alt+r cancels.
func (m *Model) runCmdChip(c database.Chip) tea.Cmd {
	if r := m.run(c.ID); r != nil && r.cancel != nil {
		r.cancel()
		m.finishRun(c.ID)
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	r := m.ensureRun(c.ID)
	r.cancel = cancel
	r.dropped = 0
	r.loaded = true // a fresh run owns the band; memory is authoritative
	r.out = nil
	m.captureRunPWD(c.ID)
	ch := make(chan tea.Msg, 1024)
	r.ch = ch
	go startBash(c.ID, c.Value, ctx, ch)
	return waitBashCmd(ch)
}

// setCmdPreview refreshes a cmd chip's inline preview — its in-memory label — from
// the first non-blank line of its run output. The label is mutated in m.chips
// only and never upserted into the chips table: the command persists, the →
// chrome does not (WARNING (invariant): run output is never synced). The full
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
	if r := m.run(id); r != nil {
		out = r.out
	}
	for _, l := range out {
		if t := strings.TrimSpace(stripSGR(l.text)); t != "" {
			preview = t
			break
		}
	}
	c.Label = clipStr(preview, 32)
	m.chips[id] = c
}

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

// stripSGR removes ANSI SGR escapes from s, leaving plain text — used to derive a
// clean inline preview from coloured command output.
func stripSGR(s string) string {
	var b strings.Builder
	inEsc := false
	for _, r := range s {
		if inEsc {
			if r == 'm' {
				inEsc = false
			}
			continue
		}
		if r == '\x1b' {
			inEsc = true
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// ── alt+e inline cmd-output view ─────────────────────────────────────────────

// cmdChipView is a cmd chip's inline expanded output viewer: the same
// band-beneath-the-node surface as a focused bash node (see bashView), keyed by
// the focused chip id (m.focusChip) instead of the node uuid. Stateless like
// every nodeView; it is reached through m.activeView, never the type registry —
// the chip lives inside a plain text node whose type has no view of its own.
type cmdChipView struct{}

// focusCmdChip focuses the chip's inline output band (alt+e on a cmd chip).
func (m *Model) focusCmdChip(c database.Chip) {
	m.focusChip = c.ID
	m.focused = true
	m.focusScroll = 0
	m.ensureRunOutLoaded(c.ID)
}

// activeView resolves the focused inline view: the cmd-chip band when a chip
// is focused, else the node type's own view.
func (m *Model) activeView(it *item) nodeView {
	if m.focusChip != "" {
		return cmdChipView{}
	}
	return nodeViewOf(it)
}

func (cmdChipView) Enter(m *Model, it *item) bool { return m.focusChip != "" }

func (cmdChipView) Leave(m *Model, it *item) { m.focusChip = "" }

// Lines is a header plus optional pwd row plus one row per output line (or a
// placeholder when empty).
func (cmdChipView) Lines(m *Model, it *item, width int) int {
	m.ensureRunOutLoaded(m.focusChip)
	r := m.run(m.focusChip) // non-nil after ensureRunOutLoaded
	n := len(r.out)
	if n == 0 {
		n = 1 // the "no output" placeholder
	}
	if r.pwd != "" {
		n++
	}
	return 1 + n
}

// Key scrolls the band; alt+r re-runs (or cancels) the chip in place; esc/alt+e
// fall through to central defocus.
func (cmdChipView) Key(m *Model, it *item, k tea.KeyMsg) (tea.Cmd, bool) {
	switch k.String() {
	case "alt+r":
		if c, ok := m.chips[m.focusChip]; ok && c.Kind == chipKindCmd {
			return m.runCmdChip(c), true
		}
	case "down", "j", "pgdown":
		step := 1
		if k.String() == "pgdown" {
			step = 10
		}
		m.focusScroll += step
		return nil, true
	case "up", "k", "pgup":
		step := 1
		if k.String() == "pgup" {
			step = 10
		}
		m.focusScroll -= step
		if m.focusScroll < 0 {
			m.focusScroll = 0
		}
		return nil, true
	case "home", "g":
		m.focusScroll = 0
		return nil, true
	case "end", "G":
		m.focusScroll = 1 << 30 // central clamp pins it to the last page
		return nil, true
	}
	return nil, false
}

// Bands renders the header and output lines, self-windowed to [scroll, scroll+winH).
func (cmdChipView) Bands(m *Model, it *item, rail string, width, scroll, winH int, focused bool) []string {
	m.ensureRunOutLoaded(m.focusChip)
	r := m.run(m.focusChip) // non-nil after ensureRunOutLoaded
	out := r.out
	running := r.cancel != nil

	cmd := ""
	if c, ok := m.chips[m.focusChip]; ok {
		cmd = c.Value
	}
	hdr := fmt.Sprintf("  $ %s · %d lines", cmd, len(out))
	if d := r.dropped; d > 0 {
		hdr += fmt.Sprintf(" · %d dropped", d)
	}
	if running {
		hdr += " · running… · ⌥r stop"
	} else {
		hdr += " · ⌥r re-run"
	}
	hdr += " · ↑↓ scroll · esc close"
	content := []string{clip(rail+cReset+cDim+hdr+cReset, width)}
	if pwd := r.pwd; pwd != "" {
		content = append(content, clip(rail+cReset+cDim+"  pwd: "+pwd+cReset, width))
	}

	if len(out) == 0 {
		content = append(content, clip(rail+cReset+cDim+"  no output yet · ⌥r runs"+cReset, width))
	}
	for _, l := range out {
		content = append(content, clip(rail+cReset+"  "+styleOutLine(l), width))
	}

	if scroll < 0 {
		scroll = 0
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
