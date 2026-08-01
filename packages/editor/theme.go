package editor

import (
	"fmt"
	"path/filepath"
)

// A theme is a named palette. Today a theme is purely colors, but the struct is
// the single extension point for future theme-controlled aspects (glyphs,
// density, signs, ...): grow the struct + applyTheme, not the call sites.
//
// WARNING (invariant): SGR *attributes* (bold/italic/underline/reset/invert) are
// universal and NEVER themed — only colors are. applyTheme reseeds the palette
// vars in render.go and the /color swatch map in style.go from the chosen theme,
// so every render reads through the active theme without per-call-site branching.
type theme struct {
	name string
	// UI palette: foreground SGR sequences.
	fg, dim, accent, red, orange, yellow, green, cyan, purple string
	// background blocks behind code rows, bash rows, date pills, and query hits.
	bgCode, bgTerm, bgPill, bgHit string
	// page background for the main region ("" = transparent / terminal
	// default). Never covers the status bar or the temp panel below it.
	bgPage string
}

// fg / bg build truecolor SGR sequences so theme definitions read as plain RGB.
func fg(r, g, b int) string { return fmt.Sprintf("\x1b[38;2;%d;%d;%dm", r, g, b) }
func bg(r, g, b int) string { return fmt.Sprintf("\x1b[48;2;%d;%d;%dm", r, g, b) }

// themes is the ordered registry; the /theme picker lists it in this order.
// "system" is the default — the locked design-v4 palette — and is reseeded at
// init(). Adding a theme is one entry here.
var themes = []theme{
	{
		name: "system",
		fg:   fg(212, 212, 212), dim: fg(122, 122, 122), accent: fg(86, 156, 214),
		red: fg(244, 71, 71), orange: fg(206, 145, 120), yellow: fg(255, 215, 95),
		green: fg(106, 153, 85), cyan: fg(78, 201, 176), purple: fg(197, 134, 192),
		bgCode: bg(31, 31, 31), bgTerm: bg(30, 34, 48), bgPill: bg(38, 79, 120), bgHit: bg(92, 72, 12),
	},
	{
		// "gray" is system with a gray page behind the main region instead of
		// the terminal's own background; bar + temp panel stay transparent.
		name: "gray",
		fg:   fg(212, 212, 212), dim: fg(122, 122, 122), accent: fg(86, 156, 214),
		red: fg(244, 71, 71), orange: fg(206, 145, 120), yellow: fg(255, 215, 95),
		green: fg(106, 153, 85), cyan: fg(78, 201, 176), purple: fg(197, 134, 192),
		bgCode: bg(31, 31, 31), bgTerm: bg(30, 34, 48), bgPill: bg(38, 79, 120), bgHit: bg(92, 72, 12),
		bgPage: bg(38, 38, 38),
	},
	{
		name: "nord",
		fg:   fg(216, 222, 233), dim: fg(97, 110, 136), accent: fg(129, 161, 193),
		red: fg(191, 97, 106), orange: fg(208, 135, 112), yellow: fg(235, 203, 139),
		green: fg(163, 190, 140), cyan: fg(136, 192, 208), purple: fg(180, 142, 173),
		bgCode: bg(46, 52, 64), bgTerm: bg(59, 66, 82), bgPill: bg(67, 76, 94), bgHit: bg(92, 78, 34),
	},
	{
		name: "gruvbox",
		fg:   fg(235, 219, 178), dim: fg(146, 131, 116), accent: fg(131, 165, 152),
		red: fg(251, 73, 52), orange: fg(254, 128, 25), yellow: fg(250, 189, 47),
		green: fg(184, 187, 38), cyan: fg(142, 192, 124), purple: fg(211, 134, 155),
		bgCode: bg(60, 56, 54), bgTerm: bg(50, 48, 47), bgPill: bg(80, 73, 69), bgHit: bg(104, 78, 12),
	},
}

// activeThemeName is the name of the theme currently applied (for the status bar
// and the /theme picker's pre-selection).
var activeThemeName = "system"

// themeByName looks a theme up by name; ok=false for an unknown name.
func themeByName(name string) (theme, bool) {
	for _, t := range themes {
		if t.name == name {
			return t, true
		}
	}
	return theme{}, false
}

// applyTheme reseeds the live palette from t: the render.go UI vars and the
// style.go /color swatch map. The eight named swatches map onto the theme's
// colors (blue→accent, gray→dim, the rest by name) so per-node /color stays
// on-theme.
func applyTheme(t theme) {
	cFG, cDim, cAccent = t.fg, t.dim, t.accent
	cRed, cYellow, cGreen = t.red, t.yellow, t.green
	cCyan, cMagenta = t.cyan, t.purple
	bgCode, bgTerm, bgPill, bgHit, bgPage = t.bgCode, t.bgTerm, t.bgPill, t.bgHit, t.bgPage
	styleColorCode = map[string]string{
		"red": t.red, "orange": t.orange, "yellow": t.yellow,
		"green": t.green, "cyan": t.cyan, "blue": t.accent,
		"purple": t.purple, "gray": t.dim,
	}
	activeThemeName = t.name
}

func init() { applyTheme(themes[0]) } // seed "system" before the first render

// themeConfigPath is the legacy text file that held the chosen theme name under
// the lflow config dir. The theme is now a DB-backed setting; this path survives
// only so loadSettings can migrate an old file-based choice into the DB once.
func (m *Model) themeConfigPath() string {
	return filepath.Join(m.ctx.Paths.Config, "lflow", "theme")
}
