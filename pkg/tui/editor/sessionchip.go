package editor

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lflow/lflow/pkg/tui/database"
)

// Coding-agent session chips — an inline chip (like a link or cmd chip) that
// points at a saved CLI session for a coding agent (Claude Code, Pi …). Each
// provider is its own chip kind. The chip renders as a compact "inset box" pill
// — a bold provider badge (CC/PI) and the session name on a mid-tint body, then
// the size in a recessed darker box — and alt+g re-enters the live session
// (suspends the TUI, execs the agent in the saved cwd, restores on exit); alt+e
// edits it. Insert via the /insert picker ("claude" / "pi").
//
// The chip record carries everything in its Value (newline "key=value" pairs:
// name, session id, cwd, byte size); its Label is unused. Adding another agent
// is a new kind constant + sessionProviders entry — no schema change.
//
// NB: distinct from the @mention `agent` chip (chipKindAgent, see agent.go),
// which is an agent NAME token worn red; these are saved coding-agent SESSIONS.

const (
	chipKindClaudeSession = "claude_session"
	chipKindPiSession     = "pi_session"
)

// sessionProvider is the per-agent descriptor: the 2-letter chip code, the CLI
// binary, and the "inset box" palette (mid-tint pill body + recessed size box).
type sessionProvider struct {
	code   string // "CC" / "PI"
	label  string // "claude code" (edit-panel heading + /insert desc)
	bin    string // CLI binary to exec on launch
	accent string // bright fg for the code badge
	nameFg string // softer fg for the session name
	sizeFg string // fg for the size readout
	pillBg string // pill body background (mid tint)
	boxBg  string // recessed size-box background (dark)
}

var sessionProviders = map[string]sessionProvider{
	chipKindClaudeSession: {
		code: "CC", label: "claude code", bin: "claude",
		accent: fg(230, 168, 132), nameFg: fg(220, 193, 177), sizeFg: fg(232, 169, 136),
		pillBg: bg(70, 51, 42), boxBg: bg(34, 21, 14),
	},
	chipKindPiSession: {
		code: "PI", label: "pi", bin: "pi",
		accent: fg(143, 220, 200), nameFg: fg(194, 230, 220), sizeFg: fg(134, 216, 195),
		pillBg: bg(33, 75, 68), boxBg: bg(12, 31, 27),
	},
}

// insertSessionKind maps the /insert picker value ("claude"/"pi") to a chip kind.
var insertSessionKind = map[string]string{
	"claude": chipKindClaudeSession,
	"pi":     chipKindPiSession,
}

func sessionProviderOf(kind string) (sessionProvider, bool) {
	p, ok := sessionProviders[kind]
	return p, ok
}

// isSessionChipKind reports whether a chip kind is a coding-agent session chip.
func isSessionChipKind(kind string) bool {
	_, ok := sessionProviders[kind]
	return ok
}

// registerSessionChips wires the providers into the shared chip-kind registry so
// the standard display/expand paths pick them up — one kind per provider, each
// with a plain display closure (the editor paints the colored segments in
// renderSessionChip).
func init() {
	for kind, p := range sessionProviders {
		p := p
		k := kind
		chipKinds[k] = chipKind{
			key: k,
			// fallback single color for non-editor surfaces; the editor paints the
			// multi-shade segments (renderSessionChip).
			color:   p.pillBg + p.accent,
			display: func(v string) string { return sessionChipDisplay(p, v) },
			expand:  func(v string) string { return sessionChipExpand(p, v) },
		}
	}
}

// ── session metadata (stored in the chip Value) ─────────────────────────────

type sessionMeta struct {
	name  string
	sid   string
	cwd   string
	bytes int64
}

func parseSessionMeta(value string) sessionMeta {
	var a sessionMeta
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

func formatSessionMeta(a sessionMeta) string {
	return fmt.Sprintf("name=%s\nsid=%s\ncwd=%s\nbytes=%d", a.name, a.sid, a.cwd, a.bytes)
}

// sizeLabel is the human-readable size shown in the chip's box, or "" when the
// session size is unknown (reuses the shared humanSize from the image node).
func (a sessionMeta) sizeLabel() string {
	if a.bytes <= 0 {
		return ""
	}
	return humanSize(a.bytes)
}

// ── display / expand (plain) ────────────────────────────────────────────────

// sessionSeg is one painted slice of the chip: its text and the bg/fg (and
// weight) it renders with — a bold code badge and the name on the pill body,
// then the size in a recessed darker box.
type sessionSeg struct {
	text string
	bg   string
	fg   string
	bold bool
}

func sessionChipSegs(p sessionProvider, value string) []sessionSeg {
	a := parseSessionMeta(value)
	name := a.name
	if name == "" {
		if a.sid != "" {
			name = a.sid
		} else {
			name = "session"
		}
	}
	segs := []sessionSeg{
		{text: " " + p.code + " ", bg: p.pillBg, fg: p.accent, bold: true},
		{text: name + " ", bg: p.pillBg, fg: p.nameFg},
	}
	if sz := a.sizeLabel(); sz != "" {
		segs = append(segs, sessionSeg{text: " " + sz + " ", bg: p.boxBg, fg: p.sizeFg})
	}
	return segs
}

// sessionChipDisplay is the PLAIN pill text — the concatenated segment text,
// used for width math and machine-readable surfaces (CLI list/grep). The editor
// paints the colored segments via renderSessionChip; both derive from the same
// segments, so their visible width is identical.
func sessionChipDisplay(p sessionProvider, value string) string {
	var b strings.Builder
	for _, s := range sessionChipSegs(p, value) {
		b.WriteString(s.text)
	}
	return b.String()
}

// sessionChipExpand is the machine-readable form for cmd/search/export.
func sessionChipExpand(p sessionProvider, value string) string {
	a := parseSessionMeta(value)
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

// renderSessionChip is the editor-only colored form: each segment painted with
// its own bg/fg shade, ending in a reset. Visible width equals the display's.
// selected (caret-on-chip) currently just paints normally — the tinted pill is
// already distinct and the node's cursor cue carries the selection.
func renderSessionChip(c database.Chip, selected bool) string {
	p, ok := sessionProviders[c.Kind]
	if !ok {
		return ""
	}
	var b strings.Builder
	for _, s := range sessionChipSegs(p, c.Value) {
		b.WriteString(cReset + s.bg + s.fg)
		if s.bold {
			b.WriteString(cBold)
		}
		b.WriteString(s.text)
	}
	b.WriteString(cReset)
	return b.String()
}

// ── caret lookup, launch ────────────────────────────────────────────────────

// sessionChipAtCaret returns the session chip the caret sits on (its anchor
// begins at the caret, or ends exactly at it), or ok=false.
func (m *Model) sessionChipAtCaret(cur *item) (database.Chip, bool) {
	if cur == nil {
		return database.Chip{}, false
	}
	spans := anchorSpans([]rune(cur.name))
	for _, sp := range []*anchorSpan{spanStartingAt(spans, m.caret), spanEndingAt(spans, m.caret)} {
		if sp == nil {
			continue
		}
		if c, ok := m.chips[sp.id]; ok && isSessionChipKind(c.Kind) {
			return c, true
		}
	}
	return database.Chip{}, false
}

// launchSessionChip re-enters a session chip: suspends the inline TUI, execs the
// provider CLI (--resume <sid> in the saved cwd) bound to the terminal, and
// restores the editor when the agent exits. Mirrors the file node's $EDITOR.
func (m *Model) launchSessionChip(c database.Chip) (tea.Model, tea.Cmd) {
	p, ok := sessionProviders[c.Kind]
	if !ok {
		return m, nil
	}
	if _, err := exec.LookPath(p.bin); err != nil {
		m.flash = p.bin + " not found — install it to launch this session"
		return m, nil
	}
	a := parseSessionMeta(c.Value)
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

// ── creation (from the /insert picker) ──────────────────────────────────────

// insertSessionChip splices a new session chip of the given kind at the caret
// (cwd seeded from the process working dir) and opens its editor so the session
// name / id can be filled in.
func (m *Model) insertSessionChip(kind string) {
	cur := m.cursorItem()
	if cur == nil {
		return
	}
	m.pushUndo("")
	cwd, _ := os.Getwd()
	anchor := m.createChip(kind, formatSessionMeta(sessionMeta{cwd: cwd}))
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
			m.openSessionEdit(c)
		}
	}
}

// ── alt+e editor (modeSessionEdit): name / session id / working dir ─────────

func (m *Model) openSessionEdit(c database.Chip) {
	a := parseSessionMeta(c.Value)
	m.mode = modeSessionEdit
	m.sessionEditID = c.ID
	m.sessionEditKind = c.Kind
	m.sessionEditName = a.name
	m.sessionEditSid = a.sid
	m.sessionEditCwd = a.cwd
	m.sessionEditField = 0
	m.sessionEditCaret = len([]rune(a.name))
}

// sessionEditActive returns the active field's text; the setter writes it back.
func (m *Model) sessionEditActive() string {
	switch m.sessionEditField {
	case 0:
		return m.sessionEditName
	case 1:
		return m.sessionEditSid
	default:
		return m.sessionEditCwd
	}
}

func (m *Model) setSessionEditActive(s string) {
	switch m.sessionEditField {
	case 0:
		m.sessionEditName = s
	case 1:
		m.sessionEditSid = s
	default:
		m.sessionEditCwd = s
	}
}

func (m *Model) saveSessionEdit() {
	c, ok := m.chips[m.sessionEditID]
	if !ok {
		return
	}
	old := parseSessionMeta(c.Value)
	c.Value = formatSessionMeta(sessionMeta{
		name:  strings.TrimSpace(m.sessionEditName),
		sid:   strings.TrimSpace(m.sessionEditSid),
		cwd:   strings.TrimSpace(m.sessionEditCwd),
		bytes: old.bytes, // size is computed elsewhere, not user-edited
	})
	m.chips[c.ID] = c
	if m.ctx.DB != nil {
		_ = database.UpsertChip(m.ctx.DB, c)
	}
	m.unsaved = true
}

func (m *Model) handleSessionEditKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "esc":
		m.mode = modeOutline
		return m, nil
	case "tab", "down":
		m.sessionEditField = (m.sessionEditField + 1) % 3
		m.sessionEditCaret = len([]rune(m.sessionEditActive()))
		return m, nil
	case "shift+tab", "up":
		m.sessionEditField = (m.sessionEditField + 2) % 3
		m.sessionEditCaret = len([]rune(m.sessionEditActive()))
		return m, nil
	case "enter":
		m.saveSessionEdit()
		m.mode = modeOutline
		m.refreshRows()
		return m, nil
	}
	f := textField{value: m.sessionEditActive(), caret: m.sessionEditCaret}
	if f.handleKey(k) {
		m.setSessionEditActive(f.value)
		m.sessionEditCaret = f.caret
	}
	return m, nil
}

func (m *Model) viewSessionEdit(maxLine int) []string {
	p := sessionProviders[m.sessionEditKind]
	rows := []struct {
		lbl, val string
	}{
		{"name   ", m.sessionEditName},
		{"session", m.sessionEditSid},
		{"cwd    ", m.sessionEditCwd},
	}
	lines := []string{clip(cDim+" edit "+p.label+" session"+cReset, maxLine)}
	for i, r := range rows {
		lblCol, val := cDim, r.val
		if i == m.sessionEditField {
			lblCol = cAccent
			val = withCaret(val, m.sessionEditCaret)
		}
		lines = append(lines, clip(lblCol+" "+r.lbl+" "+cReset+cFG+val+cReset, maxLine))
	}
	lines = append(lines, "")
	lines = append(lines, clip(cDim+" tab switch field · enter save · esc cancel · ⌥g launch"+cReset, maxLine))
	m.pageRows = len(lines)
	return lines
}
