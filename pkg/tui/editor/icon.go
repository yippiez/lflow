package editor

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mattn/go-runewidth"
)

// Icons are plain unicode spliced into a node's name — not chips. The ":"
// completer (non-query nodes) and /insert → icon open the same shortcode
// picker; picking replaces the typed ":query" with the glyph.

// iconEntry is one selectable icon: the glyph that lands in the name, its
// shortcode (picker shows :shortcode), and a palette color name used only in
// the picker row ("white" → fg, "none" → uncolored emoji, else styleColorCode).
type iconEntry struct {
	glyph     string
	shortcode string
	color     string
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
	{"🔎︎", "magnifier", "white"},
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
