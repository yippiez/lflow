package editor

import (
	"context"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/lflow/lflow/pkg/tui/database"
)

// A cmd chip is inline runnable shell: type "$<command>" inside any text node and
// commit it with a DOUBLE space (single spaces stay part of the command, so
// "$ls -la" keeps typing). The command lives in the chip value (persisted); its
// run output is ephemeral (node_output, keyed by chip id — local-only, never
// synced, exactly like a bash node). alt+r runs the chip the caret sits on, and
// the chip then renders "$cmd → <first line>"; alt+e opens the full output.

// bashCmdBeforeCaret converts a "$<command>" token terminated by a double space
// into a cmd chip. It is called from the space-typed path only when the rune
// before the caret is already a space (so this is the second space). Returns true
// if it converted, in which case the committing space is consumed by the caller.
func (m *Model) bashCmdBeforeCaret(cur *item) bool {
	if cur == nil || cur.mirrorOf != "" || !typeOf(cur.typ).inlineEditable || cur.readonly {
		return false
	}
	// inside a bash node the whole node already IS the command; "$" stays literal.
	if cur.typ == database.TypeBash {
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
	// so the cmd chip's stored command is plain, runnable shell.
	cmd := strings.TrimSpace(expandAnchors(string(runes[start+1:end]), m.chips))
	if cmd == "" {
		return false
	}
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
	if m.runCancel == nil {
		m.runCancel = map[string]func(){}
		m.runOut = map[string][]outLine{}
		m.runCh = map[string]chan tea.Msg{}
	}
	if cancel, running := m.runCancel[c.ID]; running {
		cancel()
		m.finishRun(c.ID)
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.runCancel[c.ID] = cancel
	if m.runDropped != nil {
		delete(m.runDropped, c.ID)
	}
	if m.runOutLoaded == nil {
		m.runOutLoaded = map[string]bool{}
	}
	m.runOutLoaded[c.ID] = true // a fresh run owns the band; memory is authoritative
	m.runOut[c.ID] = nil
	ch := make(chan tea.Msg, 1024)
	m.runCh[c.ID] = ch
	go startBash(c.ID, c.Value, ctx, ch)
	return waitBashCmd(ch)
}

// setCmdPreview refreshes a cmd chip's inline preview — its in-memory label — from
// the first non-blank line of its run output. The label is mutated in m.chips
// only and never upserted, so the preview is session-local: the command persists,
// the output does not (WARNING (invariant): run output is never persisted/synced).
func (m *Model) setCmdPreview(id string) {
	c, ok := m.chips[id]
	if !ok || c.Kind != chipKindCmd {
		return
	}
	preview := ""
	for _, l := range m.runOut[id] {
		if t := strings.TrimSpace(stripSGR(l.text)); t != "" {
			preview = t
			break
		}
	}
	c.Label = clipStr(preview, 32)
	m.chips[id] = c
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

// Lines is a header plus one row per output line (or a placeholder when empty).
func (cmdChipView) Lines(m *Model, it *item, width int) int {
	m.ensureRunOutLoaded(m.focusChip)
	n := len(m.runOut[m.focusChip])
	if n == 0 {
		n = 1 // the "no output" placeholder
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
	out := m.runOut[m.focusChip]
	_, running := m.runCancel[m.focusChip]

	cmd := ""
	if c, ok := m.chips[m.focusChip]; ok {
		cmd = c.Value
	}
	hdr := fmt.Sprintf("  $ %s · %d lines", cmd, len(out))
	if d := m.runDropped[m.focusChip]; d > 0 {
		hdr += fmt.Sprintf(" · %d dropped", d)
	}
	if running {
		hdr += " · running… · ⌥r stop"
	} else {
		hdr += " · ⌥r re-run"
	}
	hdr += " · ↑↓ scroll · esc close"
	content := []string{clip(rail+cReset+cDim+hdr+cReset, width)}

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
