package editor

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mattn/go-runewidth"

	"github.com/lflow/lflow/pkg/tui/database"
)

// Icons insert via the ":" shortcode completer (non-query nodes) and
// /insert → icon. White arrows/marks and emojis land as plain unicode; painted
// service glyphs (trello, claude, …) land as icon chips so render can keep the
// brand color — a bare "Z" in text must not turn red.

// iconEntry is one selectable icon: the glyph, its shortcode (picker shows
// :shortcode), and a palette color name ("white" → fg, "none" → uncolored
// emoji, else a /color swatch applied in the picker and on icon chips).
type iconEntry struct {
	glyph     string
	shortcode string
	color     string
}

// iconIsPainted reports whether the entry keeps a brand color after insert
// (via an icon chip). White and none stay plain text.
func iconIsPainted(e iconEntry) bool {
	return e.color != "" && e.color != "white" && e.color != "none"
}

// iconColorForChip resolves the paint color for an icon chip: label holds the
// shortcode (preferred); value is the glyph fallback for older/hand-edited rows.
func iconColorForChip(c database.Chip) string {
	if e, ok := iconByShortcode(c.Label); ok {
		return e.color
	}
	for _, e := range iconCatalog {
		if e.glyph == c.Value {
			return e.color
		}
	}
	return "white"
}

// iconCatalog is the fixed shortcode table. Order is the empty-query list order.
var iconCatalog = []iconEntry{
	// arrows — white
	{"←", "larrow", "white"},
	{"→", "rarrow", "white"},
	{"⇄", "doublearrow", "white"},
	{"⇐", "ldarrow", "white"},
	{"⇒", "rdarrow", "white"},
	{"⇔", "iff", "white"},
	{"⟳", "loop", "white"},
	{"⟴", "rlooparrow", "white"},
	// other — white
	{"⫘", "chain", "white"},
	{"⌕", "magnifier", "white"}, // U+2315, not the 🔎 emoji
	{"◇", "decision", "white"},
	// services — brand-ish picker tint
	{"▯◨", "trello", "blue"},
	{"⦿─○", "cpapers", "cyan"},
	{"Z", "zotero", "red"},
	{"✽", "claude", "red"},
	{"⬖", "obsidian", "purple"},
	// emojis — native color only
	{"🫠", "melt", "none"},
	{"🤫", "shush", "none"},
	{"🥶", "cold", "none"},
	{"🤚", "hand", "none"},
	{"👎", "no", "none"},
	{"🚧", "warning", "none"},
}

// iconByShortcode looks up a catalog entry by its shortcode (no leading colon).
func iconByShortcode(code string) (iconEntry, bool) {
	code = strings.ToLower(strings.TrimPrefix(code, ":"))
	for _, e := range iconCatalog {
		if e.shortcode == code {
			return e, true
		}
	}
	return iconEntry{}, false
}

// filterIcons returns catalog entries whose shortcode contains query (case
// insensitive). A leading ":" on the query is ignored so typing after the
// trigger char matches cleanly. Empty query returns the full catalog.
func filterIcons(query string) []iconEntry {
	q := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(query), ":"))
	if q == "" {
		out := make([]iconEntry, len(iconCatalog))
		copy(out, iconCatalog)
		return out
	}
	var out []iconEntry
	for _, e := range iconCatalog {
		if strings.Contains(e.shortcode, q) {
			out = append(out, e)
		}
	}
	return out
}

// iconColorSGR returns the SGR prefix for a catalog color name. "none" leaves
// emojis uncolored; "white"/"" use the theme foreground; named colors pull from
// the live /color swatch map so picker tints stay on-theme.
func iconColorSGR(color string) string {
	switch color {
	case "none":
		return ""
	case "", "white":
		return cFG
	}
	if s := styleColorCode[color]; s != "" {
		return s
	}
	return cFG
}

// iconRowRender builds the completer row content for one icon: colored glyph,
// padded to a fixed cell width, then the dim :shortcode.
func iconRowRender(e iconEntry) string {
	col := iconColorSGR(e.color)
	glyph := e.glyph
	if col != "" {
		glyph = col + e.glyph + cReset
	}
	// pad to 4 cells so shortcodes column-align across single- and multi-cell glyphs
	pad := 4 - runewidth.StringWidth(e.glyph)
	if pad < 1 {
		pad = 1
	}
	return glyph + strings.Repeat(" ", pad) + cDim + ":" + e.shortcode + cReset
}

// openIconPicker types ":" and opens the icon shortcode completer at the caret.
func (m *Model) openIconPicker(cur *item) (tea.Model, tea.Cmd) {
	return m.openCompleter(cur, complIcon, ":")
}
