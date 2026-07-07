package editor

import "github.com/lflow/lflow/pkg/tui/style"

// A node's visual styling — set by /color, /bold, /italic and /underline — is
// stored as a comma-separated token list in item.style, e.g. "bold,color:blue".
// The token vocabulary (attributes + color names) lives in pkg/tui/style, the
// single source shared with the CLI's --style/--color flags; this file owns only
// what the editor adds on top: the SGR escape codes and render-time helpers.

// styleColorOrder is the eight /color options, in the order the picker lists
// them. Each maps to a truecolor SGR foreground in styleColorCode, drawn from
// the locked editor palette so styled text stays on-brand.
var styleColorOrder = style.Colors

var styleColorCode = map[string]string{
	"red":    "\x1b[38;2;244;71;71m",   // #f44747
	"orange": "\x1b[38;2;206;145;120m", // #ce9178
	"yellow": "\x1b[38;2;255;215;95m",  // #ffd75f
	"green":  "\x1b[38;2;106;153;85m",  // #6a9955
	"cyan":   "\x1b[38;2;78;201;176m",  // #4ec9b0
	"blue":   "\x1b[38;2;86;156;214m",  // #569cd6
	"purple": "\x1b[38;2;197;134;192m", // #c586c0
	"gray":   "\x1b[38;2;122;122;122m", // #7a7a7a
}

// The token-list helpers live in pkg/tui/style; these thin aliases keep the
// editor's existing call sites readable.
func styleHas(s, tok string) bool      { return style.Has(s, tok) }
func styleToggle(s, tok string) string { return style.Toggle(s, tok) }
func styleColor(s string) string       { return style.Color(s) }
func styleSetColor(s, color string) string {
	return style.SetColor(s, color)
}

// styleAttrs returns the SGR attribute codes (bold/italic/underline) implied by
// the style, to be appended to the per-row attribute string in render.
func styleAttrs(s string) string {
	var out string
	if styleHas(s, "bold") {
		out += cBold
	}
	if styleHas(s, "italic") {
		out += cItalic
	}
	if styleHas(s, "underline") {
		out += cUnderline
	}
	if styleHas(s, "strike") {
		out += cStrike
	}
	return out
}

// styleBaseColor returns the SGR foreground code for the node's color, or "" to
// keep the default foreground.
func styleBaseColor(s string) string {
	return styleColorCode[styleColor(s)]
}
