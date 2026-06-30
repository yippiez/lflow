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
	// walk back to the "$" that opens the command: it must be at the node start or
	// follow a space (a standalone token). Bail on an anchor so we never swallow a
	// neighbouring chip.
	start := -1
	for i := end - 1; i >= 0; i-- {
		if runes[i] == chipSentinel {
			return false
		}
		if runes[i] == '$' && (i == 0 || runes[i-1] == ' ') {
			start = i
			break
		}
	}
	if start < 0 {
		return false
	}
	cmd := strings.TrimSpace(string(runes[start+1 : end]))
	if cmd == "" {
		return false
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
	return m.chipAtCaret(cur, chipKindCmd)
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
		delete(m.runCancel, c.ID)
		m.persistRunOut(c.ID)
		m.setCmdPreview(c.ID)
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.runCancel[c.ID] = cancel
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

// ── alt+e cmd-output viewer (modeCmdView) ───────────────────────────────────

// openCmdView opens the scrollable full-output viewer for a cmd chip.
func (m *Model) openCmdView(c database.Chip) {
	m.mode = modeCmdView
	m.cmdViewID = c.ID
	m.cmdViewCmd = c.Value
	m.ensureRunOutLoaded(c.ID)
	m.focusScroll = 0
}

func (m *Model) handleCmdViewKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "esc", "ctrl+c", "alt+e", "q":
		m.mode = modeOutline
		return m, nil
	case "alt+r":
		// re-run the command straight from the viewer
		if c, ok := m.chips[m.cmdViewID]; ok {
			return m, m.runCmdChip(c)
		}
	case "down", "j", "pgdown":
		step := 1
		if k.String() == "pgdown" {
			step = 10
		}
		m.focusScroll += step
	case "up", "k", "pgup":
		step := 1
		if k.String() == "pgup" {
			step = 10
		}
		m.focusScroll -= step
		if m.focusScroll < 0 {
			m.focusScroll = 0
		}
	case "home", "g":
		m.focusScroll = 0
	case "end", "G":
		m.focusScroll = 1 << 30
	}
	return m, nil
}

func (m *Model) viewCmdView(maxLine int) []string {
	out := m.runOut[m.cmdViewID]
	_, running := m.runCancel[m.cmdViewID]

	hdr := fmt.Sprintf(" $ %s · %d lines", m.cmdViewCmd, len(out))
	if running {
		hdr += " · running…"
	}
	content := []string{clip(cDim+hdr+cReset, maxLine)}
	if len(out) == 0 {
		content = append(content, clip(cDim+" no output yet · ⌥r runs"+cReset, maxLine))
	}
	for _, l := range out {
		content = append(content, clip(cReset+" "+styleOutLine(l), maxLine))
	}
	content = append(content, "", clip(cDim+" ↑↓ scroll · ⌥r re-run · esc close"+cReset, maxLine))

	scroll := m.focusScroll
	if scroll < 0 {
		scroll = 0
	}
	if scroll > len(content) {
		scroll = len(content)
	}
	return content[scroll:]
}
