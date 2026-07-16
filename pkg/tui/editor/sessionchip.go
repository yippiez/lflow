package editor

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lflow/lflow/pkg/tui/database"
)

// Coding-agent session chip — a single inline chip kind (like a link or cmd
// chip) that points at a saved CLI session for a coding agent. The agent is a
// VARIATION stored in the chip, not a separate kind: one /insert entry ("agent")
// makes the chip, and its editor switches the provider (Claude Code, Pi, …).
//
// It renders as a compact "inset box" pill — a bold provider badge (CC/PI) and
// the session name on a mid-tint body, then the size in a recessed darker box.
// alt+g re-enters the live session (suspends the TUI, execs the agent in the
// saved cwd, restores on exit); alt+e edits it.
//
// The chip record carries everything in its Value (newline "key=value" pairs:
// provider, name, session id, cwd, byte size); its Label is unused. Adding
// another agent is one sessionProviders entry — no new kind, no schema change.
//
// NB: distinct from the @mention `agent` chip (chipKindAgent, see agent.go),
// which is an agent NAME token worn red; this is a saved coding-agent SESSION.

const chipKindCodingSession = "coding_session"

// sessionProvider is one agent variation: its 2-letter chip code, the CLI binary
// to launch, and the "inset box" palette (mid-tint pill body + recessed box).
type sessionProvider struct {
	id     string // stable variation key stored in the chip ("claude" / "pi")
	code   string // "CC" / "PI"
	label  string // "claude code" (edit-panel provider row)
	bin    string // CLI binary to exec on launch
	accent string // bright fg for the code badge
	nameFg string // softer fg for the session name
	sizeFg string // fg for the size readout
	pillBg string // pill body background (mid tint)
	boxBg  string // recessed size-box background (dark)
}

// sessionProviderOrder is the variation cycle order; the first is the default.
var sessionProviderOrder = []string{"claude", "pi"}

var sessionProviders = map[string]sessionProvider{
	"claude": {
		id: "claude", code: "CC", label: "claude code", bin: "claude",
		accent: fg(230, 168, 132), nameFg: fg(220, 193, 177), sizeFg: fg(232, 169, 136),
		pillBg: bg(70, 51, 42), boxBg: bg(34, 21, 14),
	},
	"pi": {
		id: "pi", code: "PI", label: "pi", bin: "pi",
		accent: fg(143, 220, 200), nameFg: fg(194, 230, 220), sizeFg: fg(134, 216, 195),
		pillBg: bg(33, 75, 68), boxBg: bg(12, 31, 27),
	},
}

// sessionProviderByID resolves a variation, defaulting to the first when the id
// is empty or unknown (a legacy or hand-written chip).
func sessionProviderByID(id string) sessionProvider {
	if p, ok := sessionProviders[id]; ok {
		return p
	}
	return sessionProviders[sessionProviderOrder[0]]
}

// cycleProvider steps the variation id by dir (+1/-1) around the order.
func cycleProvider(id string, dir int) string {
	idx := 0
	for i, p := range sessionProviderOrder {
		if p == id {
			idx = i
		}
	}
	n := len(sessionProviderOrder)
	return sessionProviderOrder[(idx+dir+n)%n]
}

// isSessionChipKind reports whether a chip kind is the coding-agent session chip.
func isSessionChipKind(kind string) bool { return kind == chipKindCodingSession }

// registerSessionChip wires the single kind into the shared chip-kind registry so
// the standard display/expand paths pick it up; the editor paints the colored
// segments in renderSessionChip.
func init() {
	chipKinds[chipKindCodingSession] = chipKind{
		key:     chipKindCodingSession,
		color:   sessionProviders["claude"].pillBg + sessionProviders["claude"].accent, // non-editor fallback
		display: sessionChipDisplay,
		expand:  sessionChipExpand,
	}
}

// ── session metadata (stored in the chip Value) ─────────────────────────────

type sessionMeta struct {
	provider string
	name     string
	sid      string
	cwd      string
	bytes    int64
}

func parseSessionMeta(value string) sessionMeta {
	var a sessionMeta
	for _, ln := range strings.Split(value, "\n") {
		k, v, ok := strings.Cut(ln, "=")
		if !ok {
			continue
		}
		switch strings.TrimSpace(k) {
		case "provider":
			a.provider = strings.TrimSpace(v)
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
	return fmt.Sprintf("provider=%s\nname=%s\nsid=%s\ncwd=%s\nbytes=%d", a.provider, a.name, a.sid, a.cwd, a.bytes)
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

func sessionChipSegs(value string) []sessionSeg {
	a := parseSessionMeta(value)
	p := sessionProviderByID(a.provider)
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
func sessionChipDisplay(value string) string {
	var b strings.Builder
	for _, s := range sessionChipSegs(value) {
		b.WriteString(s.text)
	}
	return b.String()
}

// sessionChipExpand is the machine-readable form for cmd/search/export.
func sessionChipExpand(value string) string {
	a := parseSessionMeta(value)
	p := sessionProviderByID(a.provider)
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
	var b strings.Builder
	for _, s := range sessionChipSegs(c.Value) {
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
	a := parseSessionMeta(c.Value)
	p := sessionProviderByID(a.provider)
	if _, err := exec.LookPath(p.bin); err != nil {
		m.flash = p.bin + " not found — install it to launch this session"
		return m, nil
	}
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

// insertSessionChip splices a new session chip at the caret (default provider,
// cwd seeded from the process working dir) and opens its editor so the provider
// variation / session name / id can be set.
func (m *Model) insertSessionChip() {
	cur := m.cursorItem()
	if cur == nil {
		return
	}
	m.pushUndo("")
	cwd, _ := os.Getwd()
	anchor := m.createChip(chipKindCodingSession, formatSessionMeta(sessionMeta{provider: sessionProviderOrder[0], cwd: cwd}))
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

// ── alt+e editor (modeSessionEdit): provider / name / session id / cwd ───────

// session-edit field indices.
const (
	sessFieldProvider = iota
	sessFieldName
	sessFieldSid
	sessFieldCwd
	sessFieldCount
)

func (m *Model) openSessionEdit(c database.Chip) {
	a := parseSessionMeta(c.Value)
	if _, ok := sessionProviders[a.provider]; !ok {
		a.provider = sessionProviderOrder[0]
	}
	m.mode = modeSessionEdit
	m.sessionEditID = c.ID
	m.sessionEditProvider = a.provider
	m.sessionEditName = a.name
	m.sessionEditSid = a.sid
	m.sessionEditCwd = a.cwd
	m.sessionEditField = sessFieldName // start on the name; provider sits above
	m.sessionEditCaret = len([]rune(a.name))
}

// sessionEditActive returns the active TEXT field's value (provider is a cycle,
// not a text field, so it returns "").
func (m *Model) sessionEditActive() string {
	switch m.sessionEditField {
	case sessFieldName:
		return m.sessionEditName
	case sessFieldSid:
		return m.sessionEditSid
	case sessFieldCwd:
		return m.sessionEditCwd
	}
	return ""
}

func (m *Model) setSessionEditActive(s string) {
	switch m.sessionEditField {
	case sessFieldName:
		m.sessionEditName = s
	case sessFieldSid:
		m.sessionEditSid = s
	case sessFieldCwd:
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
		provider: m.sessionEditProvider,
		name:     strings.TrimSpace(m.sessionEditName),
		sid:      strings.TrimSpace(m.sessionEditSid),
		cwd:      strings.TrimSpace(m.sessionEditCwd),
		bytes:    old.bytes, // size is computed elsewhere, not user-edited
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
		m.sessionEditField = (m.sessionEditField + 1) % sessFieldCount
		m.sessionEditCaret = len([]rune(m.sessionEditActive()))
		return m, nil
	case "shift+tab", "up":
		m.sessionEditField = (m.sessionEditField + sessFieldCount - 1) % sessFieldCount
		m.sessionEditCaret = len([]rune(m.sessionEditActive()))
		return m, nil
	case "enter":
		m.saveSessionEdit()
		m.mode = modeOutline
		m.refreshRows()
		return m, nil
	}
	// the provider field is a variation cycle, not a text field: ←/→ (or space)
	// step through the agents; typing is ignored.
	if m.sessionEditField == sessFieldProvider {
		switch k.String() {
		case "left":
			m.sessionEditProvider = cycleProvider(m.sessionEditProvider, -1)
		case "right", " ", "space":
			m.sessionEditProvider = cycleProvider(m.sessionEditProvider, +1)
		}
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
	prov := sessionProviderByID(m.sessionEditProvider)
	lines := []string{clip(cDim+" edit coding-agent session"+cReset, maxLine)}

	// provider row: a ‹ variation › cycle
	provLbl := cDim
	provVal := prov.accent + prov.label + cReset
	if m.sessionEditField == sessFieldProvider {
		provLbl = cAccent
		provVal = cAccent + "‹ " + cReset + prov.accent + prov.label + cReset + cAccent + " ›" + cReset + cDim + "  ←/→ change" + cReset
	}
	lines = append(lines, clip(provLbl+" agent   "+cReset+provVal, maxLine))

	rows := []struct {
		field    int
		lbl, val string
	}{
		{sessFieldName, "name   ", m.sessionEditName},
		{sessFieldSid, "session", m.sessionEditSid},
		{sessFieldCwd, "cwd    ", m.sessionEditCwd},
	}
	for _, r := range rows {
		lblCol, val := cDim, r.val
		if m.sessionEditField == r.field {
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
