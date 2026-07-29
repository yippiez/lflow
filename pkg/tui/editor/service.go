package editor

import (
	"strings"

	"github.com/lflow/lflow/pkg/tui/chiptext"
	"github.com/lflow/lflow/pkg/tui/database"
)

// Service links are the same link chip, dressed for the target it points at. A
// link to a known service — the Google suite today (registry:
// pkg/tui/chiptext/service.go) — swaps the generic "→name" for the service's
// unicode mark and paints the chip in the service's own color: a Sheets link is
// a dark-green "▦ Q3 budget", a Docs link a blue "▤ spec". Everything else about
// it is unchanged — it is still a link chip, renamed with ⌥e, opened with ⌥g/⌥r.
//
// Nothing new is stored: the service is DERIVED from the chip's target, so links
// made long before this existed light up on sight, and retargeting a chip in the
// ⌥e editor redresses it. This file is the editor half — the colors; the
// recognition and the glyph are shared with the CLI in chiptext.

// serviceColors keys each service's text color by registry key. Google's own
// brand values, nudged only where a color would be illegible as FOREGROUND on a
// dark terminal. Drive's tri-color triangle has no single brand color, so the
// container takes Google's neutral gray and stays distinct from the document
// types it holds.
//
// These are the one palette /theme does NOT reseed (see theme.go's invariant on
// what a theme owns): a brand color is identity, not palette — Sheets is green
// in every theme. The neutral link chip, which carries no identity, still
// follows the theme and the /settings link.color preference.
var serviceColors = map[string]string{
	"docs":     fg(66, 133, 244),  // #4285f4 blue
	"sheets":   fg(15, 157, 88),   // #0f9d58 dark green
	"slides":   fg(244, 180, 0),   // #f4b400 yellow
	"forms":    fg(145, 104, 214), // #9168d6 purple (lifted off #7248b9 to read on dark)
	"drive":    fg(154, 160, 166), // #9aa0a6 gray
	"calendar": fg(26, 115, 232),  // #1a73e8 deep blue
	"gmail":    fg(234, 67, 53),   // #ea4335 red
	"meet":     fg(0, 172, 155),   // #00ac9b teal (lifted off #00897b to read on dark)
}

// linkService returns the service a link chip points at. A node link
// (lflow://node/…) is never a service — it never leaves the outline.
func linkService(c database.Chip) (chiptext.Service, bool) {
	if c.Kind != chipKindLink {
		return chiptext.Service{}, false
	}
	if _, isNode := nodeLinkUUID(c.Value); isNode {
		return chiptext.Service{}, false
	}
	return chiptext.ServiceFor(c.Value)
}

// serviceChipColor is the SGR a service link renders in: the brand color, still
// underlined so the chip reads as a link like every other one (the /settings
// link.color preference only governs the neutral links it does not recognize).
// A service with no color yet falls back to that neutral styling.
func serviceChipColor(s chiptext.Service) string {
	if col, ok := serviceColors[s.Key]; ok {
		return col + cUnderline
	}
	return linkChipColorCode()
}

// pasteServiceLink turns a pasted service URL into its dressed link chip at the
// caret, returning whether it consumed the paste. Only a RECOGNIZED service
// chips this way: every other URL still pastes as plain text, so the gesture
// gives a face to the links that have one and changes nothing else.
func (m *Model) pasteServiceLink(cur *item, text string) bool {
	if cur == nil || !chipsEnabled(cur) {
		return false
	}
	target := strings.TrimSpace(text)
	svc, ok := chiptext.ServiceFor(target)
	if !ok {
		return false
	}
	// ServiceFor already vouched for the host, so a missing scheme is safe to
	// add: the stored target must be openable by the browser as-is.
	if !strings.Contains(target, "://") {
		target = "https://" + target
	}
	m.insertLinkChip(target, svc.Label)
	m.flash = svc.Label + " link · ⌥e rename · ⌥r open"
	return true
}
