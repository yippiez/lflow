package editor

import (
	"strings"

	"github.com/lflow/lflow/packages/chips"
	"github.com/lflow/lflow/packages/database"
)

// Service links are the same link chip, marked for the target it points at. A
// link to a known service — the Google suite today (registry:
// packages/chips/service.go) — carries that service's unicode mark beside its
// name: "→▦ Q3 budget" instead of "→Q3 budget". Nothing else changes. Same
// arrow, same link color and underline, same ⌥e rename, same ⌥g/⌥r open.
//
// Nothing new is stored either: the service is DERIVED from the chip's target,
// so links made long before this existed pick up their mark on sight, and
// retargeting a chip in the ⌥e editor remarks it. This file is the editor half;
// the recognition and the marks are shared with the CLI in chips.

// serviceColors keys each service's link color by registry key. These are MUTED
// tints, not brand values: a link chip is deliberately quiet (dim gray by
// default, see linkChipColorCode), and a service link keeps that weight — it
// only trades the gray for the service's own hue. Full-strength brand colors
// would make a row of links shout over the text they sit in.
//
// The hues are spread so the services stay apart at a glance, and each sits
// where its own brand does: Sheets green, Drive light blue, Colab amber,
// Claude clay, Gemini orchid, ChatGPT teal.
//
// This is the one palette /theme does not reseed (see theme.go's invariant on
// what a theme owns): the hue identifies the service, so Sheets is green in
// every theme. Links to anything unrecognized still follow the theme and the
// /settings link.color preference.
var serviceColors = map[string]string{
	"sheets":  fg(107, 154, 114), // #6b9a72 green
	"slides":  fg(176, 154, 92),  // #b09a5c gold
	"drive":   fg(139, 184, 222), // #8bb8de light blue
	"colab":   fg(196, 135, 79),  // #c4874f amber
	"gmail":   fg(181, 127, 122), // #b57f7a brick
	"claude":  fg(198, 129, 103), // #c68167 clay
	"gemini":  fg(181, 143, 196), // #b58fc4 orchid
	"chatgpt": fg(111, 160, 140), // #6fa08c teal
}

// serviceChipColor is the SGR a service link renders in: its muted hue, still
// underlined like every other link — the whole chip, arrow included, is one run
// of colored text with no filled cell behind it. A service with no color yet
// falls back to the ordinary link styling.
func serviceChipColor(s chips.Service) string {
	if col, ok := serviceColors[s.Key]; ok {
		return col + cUnderline
	}
	return linkChipColorCode()
}

// linkService returns the service a link chip points at. A node link
// (lflow://node/…) is never a service — it never leaves the outline.
func linkService(c database.Chip) (chips.Service, bool) {
	if c.Kind != chipKindLink {
		return chips.Service{}, false
	}
	if _, isNode := nodeLinkUUID(c.Value); isNode {
		return chips.Service{}, false
	}
	return chips.ServiceFor(c.Value)
}

// pasteServiceLink turns a pasted service URL into a marked link chip at the
// caret, returning whether it consumed the paste. Only a RECOGNIZED service
// chips this way: every other URL still pastes as plain text, so the gesture
// gives a name to the links that have one and changes nothing else.
func (m *Model) pasteServiceLink(cur *item, text string) bool {
	if cur == nil || !chipsEnabled(cur) {
		return false
	}
	target := strings.TrimSpace(text)
	svc, ok := chips.ServiceFor(target)
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
