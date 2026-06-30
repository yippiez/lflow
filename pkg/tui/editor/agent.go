package editor

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mattn/go-runewidth"

	"github.com/lflow/lflow/pkg/tui/database"
)

// Coding-agent session nodes — a saved CLI session you re-enter from the
// outline. There is one node type per agent (claude code, pi …); the node's
// name is the human session label and its note carries the session metadata
// (id, working dir, byte size). The row renders as a full-width colored bar via
// the registry `band` hook, showing only what matters at a glance: the provider,
// the session name, and the on-disk size. alt+r re-enters the live session.
//
// Adding another agent is a new TypeXxx constant + agentProviders entry + two
// registry entries — no DB migration (free-string node types).

// agentProvider is the per-agent descriptor: its display label and glyph, the
// CLI binary to launch, and the bar's tint (accent text, background, selected
// background, muted size color). Colors are precomputed SGR strings.
type agentProvider struct {
	label  string // "claude code" (long form, for the /type picker tooltip)
	code   string // 2-letter chip code, e.g. "CC" / "PI"
	glyph  string // optional bar glyph, e.g. "✳"
	bin    string // CLI binary to exec on launch
	accent string // provider fg accent
	bg     string // bar background (unselected)
	bgSel  string // bar background (selected/cursor row)
	pillBg string // code-chip background (the accent as a bg block)
	ink    string // code-chip foreground (dark, reads on the bright pill)
	boxBg  string // size-chip background (a raised box on the bar)
	size   string // size-chip foreground
}

var agentProviders = map[string]agentProvider{
	database.TypeAgentClaude: {
		label: "claude code", code: "CC", glyph: "✳", bin: "claude",
		accent: fg(224, 158, 123), bg: bg(58, 42, 34), bgSel: bg(88, 62, 48),
		pillBg: bg(224, 158, 123), ink: fg(40, 26, 18), boxBg: bg(82, 58, 46), size: fg(232, 200, 178),
	},
	database.TypeAgentPi: {
		label: "pi", code: "PI", glyph: "π", bin: "pi",
		accent: fg(120, 210, 190), bg: bg(22, 52, 48), bgSel: bg(34, 76, 70),
		pillBg: bg(120, 210, 190), ink: fg(10, 34, 30), boxBg: bg(32, 70, 64), size: fg(160, 216, 204),
	},
}

// agentShowGlyph toggles the provider glyph on the bar. The provider label and
// tint already identify the agent, so the glyph is optional chrome — default
// off for a cleaner bar with a consistent name column across providers.
var agentShowGlyph = false

func agentProviderOf(typ string) (agentProvider, bool) {
	p, ok := agentProviders[typ]
	return p, ok
}

// agentGlyph is the registry glyph hook — used wherever a non-band surface needs
// the bullet (e.g. the /type picker preview). The bar itself draws its own glyph.
func agentGlyph(typ string) func(it *item) (string, string) {
	return func(it *item) (string, string) {
		if p, ok := agentProviders[typ]; ok {
			return p.glyph, p.accent
		}
		return glyphOpen, cDim
	}
}

// ── session metadata (stored in the node note) ──────────────────────────────

// agentMeta is the session metadata parsed from a node's note: the resumable
// session id, the working directory, and the session's on-disk byte size.
type agentMeta struct {
	sid   string
	cwd   string
	bytes int64
}

// parseAgentMeta reads the newline-separated "key=value" metadata out of a
// node's note. Unknown keys are ignored, so the format can grow.
func parseAgentMeta(note string) agentMeta {
	var a agentMeta
	for _, ln := range strings.Split(note, "\n") {
		k, v, ok := strings.Cut(ln, "=")
		if !ok {
			continue
		}
		switch strings.TrimSpace(k) {
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

// formatAgentMeta renders agentMeta back to the note storage form.
func formatAgentMeta(a agentMeta) string {
	return fmt.Sprintf("sid=%s\ncwd=%s\nbytes=%d", a.sid, a.cwd, a.bytes)
}

// sizeLabel is the human-readable size shown on the bar; an unknown size shows
// a muted dash.
func (a agentMeta) sizeLabel() string {
	if a.bytes <= 0 {
		return "—"
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

// ── full-width bar (registry band hook) ─────────────────────────────────────

// agentBand renders a coding-agent session node as a single 100%-width colored
// bar: <glyph> <provider> · <session name> ………… <size>. The tree rail stays
// dim and uncolored to its left so nesting reads; the colored region fills the
// rest of the width. caret >= 0 draws the inline edit cursor in the name.
func agentBand(m *Model, r row, width, caret int, selected bool) []string {
	prov, ok := agentProviderOf(r.it.typ)
	if !ok {
		return nil
	}
	meta := parseAgentMeta(r.it.note)
	size := meta.sizeLabel()

	rail := " " + connector(r) // dim nesting rail, outside the bar
	inner := width - visibleWidth(rail)
	if inner < 14 {
		inner = 14
	}

	barBg := prov.bg
	if selected {
		barBg = prov.bgSel
	}

	// the bar carries two chips — a 2-letter provider chip on the left and a
	// boxed size chip on the right — with the session name between them. Each
	// chip is a padded pill (one space each side), so its width is text+2.
	glyph := prov.glyph
	codeW := runewidth.StringWidth(prov.code) + 2
	sizeW := runewidth.StringWidth(size) + 2
	const gap = 2 // code chip → name

	leadW := 1 // leading pad inside the bar
	if agentShowGlyph {
		leadW += runewidth.StringWidth(glyph) + 1
	}
	leftW := leadW + codeW + gap   // up to where the name starts
	rightW := 1 + sizeW + 1        // gap before the size chip + chip + trailing pad

	name := r.it.name
	if strings.TrimSpace(name) == "" {
		name = "untitled session"
	}
	editing := caret >= 0
	caretExtra := 0
	if editing && caret >= len([]rune(name)) {
		caretExtra = 1 // the block cursor parks one cell past the end
	}

	nameBudget := inner - leftW - rightW - caretExtra
	if nameBudget < 4 {
		nameBudget = 4
	}
	clipped := false
	if runewidth.StringWidth(name) > nameBudget {
		name = clipPlain(name, nameBudget)
		clipped = true
		caretExtra = 0
	}
	nameW := runewidth.StringWidth(name)
	pad := inner - leftW - nameW - caretExtra - rightW
	if pad < 0 {
		pad = 0
	}

	var nameSeg string
	if editing && !clipped {
		nameSeg = caretOnBg(name, caret, barBg, cFG)
	} else {
		nameSeg = cFG + name
	}

	codeChip := prov.pillBg + prov.ink + cBold + " " + prov.code + " " + cReset + barBg
	sizeChip := prov.boxBg + prov.size + " " + size + " " + cReset + barBg

	var b strings.Builder
	b.WriteString(cDim + rail)          // rail (no bar bg)
	b.WriteString(cReset + barBg + " ") // open the bar
	if agentShowGlyph {
		b.WriteString(prov.accent + cBold + glyph + cReset + barBg + " ")
	}
	b.WriteString(codeChip)
	b.WriteString(strings.Repeat(" ", gap))
	b.WriteString(nameSeg + cReset + barBg)
	b.WriteString(strings.Repeat(" ", pad))
	b.WriteString(" " + sizeChip + " ")
	b.WriteString(cReset)
	return []string{b.String()}
}

// caretOnBg renders text with a block cursor at caret, restoring the bar's
// background after the inverted cell (withCaret resets to the bare palette,
// which would punch a hole in the colored bar).
func caretOnBg(text string, caret int, barBg, fgCol string) string {
	runes := []rune(text)
	if caret < 0 {
		return fgCol + text
	}
	if caret >= len(runes) {
		return fgCol + string(runes) + cInvert + " " + cReset + barBg + fgCol
	}
	return fgCol + string(runes[:caret]) + cInvert + string(runes[caret]) +
		cReset + barBg + fgCol + string(runes[caret+1:])
}

// clipPlain truncates plain text to display width w, appending an ellipsis.
func clipPlain(s string, w int) string {
	if runewidth.StringWidth(s) <= w {
		return s
	}
	if w <= 1 {
		return "…"
	}
	var out []rune
	cur := 0
	for _, r := range s {
		rw := runewidth.RuneWidth(r)
		if cur+rw > w-1 {
			break
		}
		out = append(out, r)
		cur += rw
	}
	return string(out) + "…"
}

// ── launch (registry run hook, alt+r) ───────────────────────────────────────

// launchAgent re-enters the saved session: it suspends the inline TUI, execs the
// provider CLI (resuming the stored session id, in the stored working dir) bound
// to the terminal, and restores the editor when the agent exits. Mirrors how the
// file node shells out to $EDITOR.
func launchAgent(m *Model, it *item) tea.Cmd {
	prov, ok := agentProviderOf(it.typ)
	if !ok {
		return nil
	}
	if _, err := exec.LookPath(prov.bin); err != nil {
		m.flash = prov.bin + " not found — install it to launch this session"
		return nil
	}
	meta := parseAgentMeta(it.note)
	var args []string
	if meta.sid != "" {
		args = append(args, "--resume", meta.sid)
	}
	c := exec.Command(prov.bin, args...)
	if cwd := expandHome(meta.cwd); cwd != "" {
		if st, err := os.Stat(cwd); err == nil && st.IsDir() {
			c.Dir = cwd
		}
	}
	m.flash = "launching " + prov.bin + "…"
	return tea.ExecProcess(c, func(error) tea.Msg { return nil })
}
