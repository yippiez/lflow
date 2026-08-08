package multiplexer

import (
	"regexp"
	"strings"
	"time"
)

// Agent status is read off the SCREEN, not off byte activity: a working agent
// is one whose visible chrome says so (herdr's insight, ported here). Claude
// Code paints a braille spinner into its OSC title while it works and a ❯
// prompt box when it waits; permission prompts carry their own hint lines.
// There are no output-silence timers — a silent agent mid-tool-call is still
// working, and only its chrome knows.

// Status is an agent session's read state.
type Status int

const (
	StatusIdle Status = iota
	StatusWorking
	StatusBlocked // waiting on the user: a permission prompt, a picker
)

func (st Status) String() string {
	switch st {
	case StatusWorking:
		return "working"
	case StatusBlocked:
		return "blocked"
	default:
		return "idle"
	}
}

// idleConfirmAfter is the working→idle debounce: a chrome-less idle reading
// must hold this long before it publishes. Working and blocked — and any idle
// backed by VISIBLE chrome (the prompt box) — publish immediately; only the
// ambiguous "nothing matched" idle is held, so a repaint flicker between two
// working frames cannot blink the shimmer off.
const idleConfirmAfter = 700 * time.Millisecond

// Status returns the session's current debounced status.
func (s *Session) Status() Status {
	if !s.Live() {
		return StatusIdle
	}
	return s.status
}

// UpdateStatus re-reads the screen and advances the status machine. The owner
// calls it after writing output into Scr and on animation ticks (so the
// debounce clock advances while the agent is silent).
func (s *Session) UpdateStatus() Status {
	if !s.Live() {
		s.status = StatusIdle
		return s.status
	}
	raw, visible := detectRaw(s.Agent, s.Scr.Title(), s.Scr.GridPlain())
	now := time.Now()
	switch {
	case raw == s.status:
		s.candidate = raw
	case visible || raw != StatusIdle:
		// backed by chrome, or a busier state: publish immediately
		s.status, s.visible, s.candidate = raw, visible, raw
	default:
		// working/blocked → chrome-less idle: hold until the reading sticks
		if s.candidate != raw {
			s.candidate, s.candidateAt = raw, now
		} else if now.Sub(s.candidateAt) >= idleConfirmAfter {
			s.status, s.visible = raw, visible
		}
	}
	return s.status
}

// TitleLine is the agent's one-line "what I'm doing" — its terminal title with
// the leading activity glyph stripped. Claude Code titles read "⠹ Fixing the
// parser" while working and "✳ Fixing the parser" at rest; the text is the
// summary either way.
func (s *Session) TitleLine() string {
	t := strings.TrimSpace(s.Scr.Title())
	r := []rune(t)
	if len(r) >= 2 && r[1] == ' ' && (isBraille(r[0]) || strings.ContainsRune("·✢✳✶✻✽", r[0])) {
		return strings.TrimSpace(string(r[2:]))
	}
	return t
}

func isBraille(r rune) bool { return r >= 0x2800 && r <= 0x28FF }

// ── the per-agent rules ─────────────────────────────────────────────────────
//
// Condensed from herdr's published detection manifests
// (herdr.dev/agent-detection): the highest-priority signals per agent, on the
// whole live grid rather than herdr's finer regions. No match for a known
// agent means idle.

var (
	rePromptBox = regexp.MustCompile(`(?m)^[\s│]*❯`)
	reYesOption = regexp.MustCompile(`(?mi)^[\s│]*❯?\s*1?\.?\s*yes\b`)
	reProgress  = regexp.MustCompile(`(■|⬝){4,}`)
)

// detectRaw reads one frame's status straight off the chrome. visible reports
// that the state is backed by something seen (a spinner, a prompt box, a
// permission form) rather than being the fallback.
func detectRaw(agent, title string, grid []string) (raw Status, visible bool) {
	text := strings.ToLower(strings.Join(grid, "\n"))
	has := func(needle string) bool { return strings.Contains(text, needle) }

	// a title that starts with a braille spinner frame is working, whoever set it
	tr := []rune(strings.TrimSpace(title))
	titleSpinner := len(tr) >= 2 && isBraille(tr[0]) && tr[1] == ' '

	switch agent {
	case "claude":
		if titleSpinner {
			return StatusWorking, true
		}
		if has("esc to cancel") && (has("enter to confirm") || (has("enter to select") && has("navigate"))) {
			return StatusBlocked, true
		}
		if has("do you want to proceed?") && reYesOption.MatchString(text) {
			return StatusBlocked, true
		}
		if has("esc to interrupt") {
			return StatusWorking, true
		}
		if rePromptBox.MatchString(strings.Join(grid, "\n")) && !has("enter to select") && !has("esc to cancel") {
			return StatusIdle, true
		}
		if strings.HasPrefix(strings.TrimSpace(title), "✳ ") {
			return StatusIdle, true
		}
	case "opencode":
		if has("△ permission required") ||
			(has("esc dismiss") && (has("enter confirm") || has("enter submit") || has("enter toggle"))) {
			return StatusBlocked, true
		}
		if has("esc to interrupt") || has("ctrl+c to interrupt") || reProgress.MatchString(text) {
			return StatusWorking, true
		}
	case "pi", "prime-agent":
		// prime-agent is pi's TUI on another runtime; the same chrome applies
		if has("working...") || has("esc to interrupt") {
			return StatusWorking, true
		}
	default:
		if titleSpinner || has("esc to interrupt") {
			return StatusWorking, true
		}
	}
	return StatusIdle, false
}
