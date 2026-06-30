package editor

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lflow/lflow/pkg/tui/database"
)

// Coding-agent session chips — an inline chip (like a link or path chip) that
// points at a saved CLI session. Each provider (claude code, pi …) is its own
// chip kind. The chip renders as a compact provider-tinted pill — "CC <session
// name> · <size>" — and alt+g re-enters the live session (suspends the TUI,
// execs the agent in the saved cwd, restores on exit); alt+e edits it.
//
// The chip record carries everything in its Value (newline "key=value" pairs:
// name, session id, cwd, byte size); its Label is unused. Adding another agent
// is a new kind constant + agentChipProviders entry — no schema change.

const (
	chipKindAgentClaude = "agent_claude"
	chipKindAgentPi     = "agent_pi"
)

// agentChipProvider is the per-agent descriptor: the 2-letter chip code, the CLI
// binary, and the "inset box" pill palette — a mid-tint pill body holding the
// code + name, with a recessed darker box at the right end holding the size.
type agentChipProvider struct {
	code   string // "CC" / "PI"
	label  string // "claude code" (edit-panel heading)
	bin    string // CLI binary to exec on launch
	accent string // bright fg for the code badge
	nameFg string // softer fg for the session name
	sizeFg string // fg for the size readout
	pillBg string // pill body background (mid tint)
	boxBg  string // recessed size-box background (dark)
}

var agentChipProviders = map[string]agentChipProvider{
	chipKindAgentClaude: {
		code: "CC", label: "claude code", bin: "claude",
		accent: fg(230, 168, 132), nameFg: fg(220, 193, 177), sizeFg: fg(232, 169, 136),
		pillBg: bg(70, 51, 42), boxBg: bg(34, 21, 14),
	},
	chipKindAgentPi: {
		code: "PI", label: "pi", bin: "pi",
		accent: fg(143, 220, 200), nameFg: fg(194, 230, 220), sizeFg: fg(134, 216, 195),
		pillBg: bg(33, 75, 68), boxBg: bg(12, 31, 27),
	},
}

// isAgentChipKind reports whether a chip kind is a coding-agent session chip.
func isAgentChipKind(kind string) bool {
	_, ok := agentChipProviders[kind]
	return ok
}

// registerAgentChips wires the agent providers into the shared chip-kind
// registry so the standard render/expand paths pick them up — one kind per
// provider, each with its own pill color and display closure.
func init() {
	for kind, p := range agentChipProviders {
		p := p
		k := kind
		chipKinds[k] = chipKind{
			key: k,
			// fallback single color for surfaces that don't special-case agent
			// chips; the editor paints the multi-shade segments (agentChipRender).
			color:   p.pillBg + p.accent,
			display: func(v string) string { return agentChipDisplay(p, v) },
			expand:  func(v string) string { return agentChipExpand(p, v) },
		}
	}
}

// ── session metadata (stored in the chip Value) ─────────────────────────────

type agentMeta struct {
	name  string
	sid   string
	cwd   string
	bytes int64
}

func parseAgentMeta(value string) agentMeta {
	var a agentMeta
	for _, ln := range strings.Split(value, "\n") {
		k, v, ok := strings.Cut(ln, "=")
		if !ok {
			continue
		}
		switch strings.TrimSpace(k) {
		case "name":
			a.name = strings.TrimSpace(v)
		case "sid":
			a.sid = strings.TrimSpace(v)
		case "cwd":
			a.cwd = strings.TrimSpace(v)
		case "bytes":
			fmt.Sscan(strings.TrimSpace(v), &a.bytes)
		}
	}
	return a
}

func formatAgentMeta(a agentMeta) string {
	return fmt.Sprintf("name=%s\nsid=%s\ncwd=%s\nbytes=%d", a.name, a.sid, a.cwd, a.bytes)
}

func (a agentMeta) sizeLabel() string {
	if a.bytes <= 0 {
		return ""
	}
	return humanSize(a.bytes)
}

// humanSize formats a byte count as "4.2 MB" (binary units, one decimal).
func humanSize(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	units := []string{"KB", "MB", "GB", "TB", "PB"}
	f := float64(n)
	i := -1
	for f >= unit && i < len(units)-1 {
		f /= unit
		i++
	}
	return fmt.Sprintf("%.1f %s", f, units[i])
}

// ── chip display / expand ───────────────────────────────────────────────────

// agentSeg is one painted slice of the chip: its text and the bg/fg (and weight)
// it renders with. The "inset box" design has up to three: a bold code badge and
// the name on the pill body, then the size in a recessed darker box.
type agentSeg struct {
	text string
	bg   string
	fg   string
	bold bool
}

// agentChipSegs builds the chip's painted segments. An unnamed session falls
// back to its short session id, then to "session".
func agentChipSegs(p agentChipProvider, value string) []agentSeg {
	a := parseAgentMeta(value)
	name := a.name
	if name == "" {
		if a.sid != "" {
			name = a.sid
		} else {
			name = "session"
		}
	}
	segs := []agentSeg{
		{text: " " + p.code + " ", bg: p.pillBg, fg: p.accent, bold: true},
		{text: name + " ", bg: p.pillBg, fg: p.nameFg},
	}
	if sz := a.sizeLabel(); sz != "" {
		segs = append(segs, agentSeg{text: " " + sz + " ", bg: p.boxBg, fg: p.sizeFg})
	}
	return segs
}

// agentChipDisplay is the PLAIN pill text — the concatenated segment text, used
// for width math and machine-readable surfaces (CLI list/grep). The editor
// paints the colored segments via agentChipRender; both derive from the same
// segments, so their visible width is identical.
func agentChipDisplay(p agentChipProvider, value string) string {
	var b strings.Builder
	for _, s := range agentChipSegs(p, value) {
		b.WriteString(s.text)
	}
	return b.String()
}

// agentChipRender is the editor-only colored form: each segment painted with its
// own bg/fg shade, ending in a reset. Visible width equals agentChipDisplay's.
func agentChipRender(c database.Chip) string {
	p, ok := agentChipProviders[c.Kind]
	if !ok {
		return ""
	}
	var b strings.Builder
	for _, s := range agentChipSegs(p, c.Value) {
		b.WriteString(cReset + s.bg + s.fg)
		if s.bold {
			b.WriteString(cBold)
		}
		b.WriteString(s.text)
	}
	b.WriteString(cReset)
	return b.String()
}

// agentChipExpand is the machine-readable form for bash/search/export.
func agentChipExpand(p agentChipProvider, value string) string {
	a := parseAgentMeta(value)
	cmd := p.bin
	if a.sid != "" {
		cmd += " --resume " + a.sid
	}
	name := a.name
	if name == "" {
		name = p.label + " session"
	}
	return "[" + name + "](" + cmd + ")"
}

// ── caret lookup, launch ────────────────────────────────────────────────────

// agentChipAtCaret returns the agent-session chip the caret sits on (its anchor
// begins at the caret, or ends exactly at it), or ok=false.
func (m *Model) agentChipAtCaret(cur *item) (database.Chip, bool) {
	if cur == nil {
		return database.Chip{}, false
	}
	spans := anchorSpans([]rune(cur.name))
	for _, sp := range []*anchorSpan{spanStartingAt(spans, m.caret), spanEndingAt(spans, m.caret)} {
		if sp == nil {
			continue
		}
		if c, ok := m.chips[sp.id]; ok && isAgentChipKind(c.Kind) {
			return c, true
		}
	}
	return database.Chip{}, false
}

// launchAgentChip re-enters a session chip: suspends the inline TUI, execs the
// provider CLI (--resume <sid> in the saved cwd) bound to the terminal, and
// restores the editor when the agent exits. Mirrors the file node's $EDITOR.
func (m *Model) launchAgentChip(c database.Chip) (tea.Model, tea.Cmd) {
	p, ok := agentChipProviders[c.Kind]
	if !ok {
		return m, nil
	}
	if _, err := exec.LookPath(p.bin); err != nil {
		m.flash = p.bin + " not found — install it to launch this session"
		return m, nil
	}
	a := parseAgentMeta(c.Value)
	var args []string
	if a.sid != "" {
		args = append(args, "--resume", a.sid)
	}
	cmd := exec.Command(p.bin, args...)
	if cwd := expandHome(a.cwd); cwd != "" {
		if st, err := os.Stat(cwd); err == nil && st.IsDir() {
			cmd.Dir = cwd
		}
	}
	m.flash = "launching " + p.bin + "…"
	return m, tea.ExecProcess(cmd, func(error) tea.Msg { return nil })
}

// ── creation ────────────────────────────────────────────────────────────────

// insertAgentChip splices a new agent-session chip of the given kind at the
// caret (cwd seeded from the process working dir) and opens its editor so the
// session name / id can be filled in.
func (m *Model) insertAgentChip(kind string) {
	cur := m.cursorItem()
	if cur == nil {
		return
	}
	m.pushUndo("")
	cwd, _ := os.Getwd()
	anchor := m.createChip(kind, formatAgentMeta(agentMeta{cwd: cwd}))
	if anchor == "" {
		return
	}
	runes := []rune(cur.name)
	m.boundCaret(len(runes))
	cur.name = string(runes[:m.caret]) + anchor + string(runes[m.caret:])
	m.caret += len([]rune(anchor))
	m.unsaved = true
	if spans := anchorSpans([]rune(anchor)); len(spans) == 1 {
		if c, ok := m.chips[spans[0].id]; ok {
			m.openAgentEdit(c)
		}
	}
}

// ── alt+e editor (modeAgentEdit): name / session id / working dir ───────────

func (m *Model) openAgentEdit(c database.Chip) {
	a := parseAgentMeta(c.Value)
	m.mode = modeAgentEdit
	m.agentEditID = c.ID
	m.agentEditKind = c.Kind
	m.agentEditName = a.name
	m.agentEditSid = a.sid
	m.agentEditCwd = a.cwd
	m.agentEditField = 0
}

func (m *Model) saveAgentEdit() {
	c, ok := m.chips[m.agentEditID]
	if !ok {
		return
	}
	old := parseAgentMeta(c.Value)
	c.Value = formatAgentMeta(agentMeta{
		name:  strings.TrimSpace(m.agentEditName),
		sid:   strings.TrimSpace(m.agentEditSid),
		cwd:   strings.TrimSpace(m.agentEditCwd),
		bytes: old.bytes, // size is computed elsewhere, not user-edited
	})
	m.chips[c.ID] = c
	if m.ctx.DB != nil {
		_ = database.UpsertChip(m.ctx.DB, c)
	}
	m.unsaved = true
}

func (m *Model) handleAgentEditKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	field := func() *string {
		switch m.agentEditField {
		case 0:
			return &m.agentEditName
		case 1:
			return &m.agentEditSid
		default:
			return &m.agentEditCwd
		}
	}
	switch k.String() {
	case "esc":
		m.mode = modeOutline
		return m, nil
	case "tab", "down":
		m.agentEditField = (m.agentEditField + 1) % 3
		return m, nil
	case "shift+tab", "up":
		m.agentEditField = (m.agentEditField + 2) % 3
		return m, nil
	case "enter":
		m.saveAgentEdit()
		m.mode = modeOutline
		m.refreshRows()
		return m, nil
	case "backspace":
		f := field()
		if r := []rune(*f); len(r) > 0 {
			*f = string(r[:len(r)-1])
		}
		return m, nil
	}
	if k.Type == tea.KeySpace && !k.Alt {
		k.Type, k.Runes = tea.KeyRunes, []rune{' '}
	}
	if k.Type == tea.KeyRunes && !k.Alt {
		f := field()
		*f += string(k.Runes)
	}
	return m, nil
}

func (m *Model) viewAgentEdit(maxLine int) []string {
	p := agentChipProviders[m.agentEditKind]
	fields := []struct {
		lbl, val string
	}{
		{"name   ", m.agentEditName},
		{"session", m.agentEditSid},
		{"cwd    ", m.agentEditCwd},
	}
	lines := []string{clip(cDim+" edit "+p.label+" session"+cReset, maxLine)}
	for i, f := range fields {
		lblCol, val := cDim, f.val
		if i == m.agentEditField {
			lblCol = cAccent
			val = withCaret(val, len([]rune(val)))
		}
		lines = append(lines, clip(lblCol+" "+f.lbl+" "+cReset+cFG+val+cReset, maxLine))
	}
	lines = append(lines, "")
	lines = append(lines, clip(cDim+" tab switch field · enter save · esc cancel · ⌥g launch"+cReset, maxLine))
	return lines
}
