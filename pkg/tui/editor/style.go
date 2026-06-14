package editor

import "strings"

// A node's visual styling — set by /color, /bold, /italic and /underline — is
// stored as a comma-separated token list in item.style, e.g. "bold,color:blue".
// Tokens: "bold", "italic", "underline", and "color:<name>". An empty string is
// an unstyled node. These helpers parse and edit that list; render.go turns the
// tokens into SGR sequences via styleAttrs/styleBaseColor.

// styleColorOrder is the eight /color options, in the order the picker lists
// them. Each maps to a truecolor SGR foreground in styleColorCode, drawn from
// the locked editor palette so styled text stays on-brand.
var styleColorOrder = []string{"red", "orange", "yellow", "green", "cyan", "blue", "purple", "gray"}

var styleColorCode = map[string]string{
	"red":    "\x1b[38;2;244;71;71m",   // #f44747
	"orange": "\x1b[38;2;206;145;120m", // #ce9178
	"yellow": "\x1b[38;2;220;220;170m", // #dcdcaa
	"green":  "\x1b[38;2;106;153;85m",  // #6a9955
	"cyan":   "\x1b[38;2;78;201;176m",  // #4ec9b0
	"blue":   "\x1b[38;2;86;156;214m",  // #569cd6
	"purple": "\x1b[38;2;197;134;192m", // #c586c0
	"gray":   "\x1b[38;2;122;122;122m", // #7a7a7a
}

// styleTokens splits a style string into its tokens, dropping empties.
func styleTokens(style string) []string {
	if style == "" {
		return nil
	}
	var out []string
	for _, t := range strings.Split(style, ",") {
		if t != "" {
			out = append(out, t)
		}
	}
	return out
}

// styleHas reports whether the style carries the given flag token.
func styleHas(style, tok string) bool {
	for _, t := range styleTokens(style) {
		if t == tok {
			return true
		}
	}
	return false
}

// styleToggle adds the flag token if absent, removes it if present, preserving
// the order of the remaining tokens.
func styleToggle(style, tok string) string {
	var out []string
	found := false
	for _, t := range styleTokens(style) {
		if t == tok {
			found = true
			continue
		}
		out = append(out, t)
	}
	if !found {
		out = append(out, tok)
	}
	return strings.Join(out, ",")
}

// styleColor returns the current color name, or "" when none is set.
func styleColor(style string) string {
	for _, t := range styleTokens(style) {
		if c, ok := strings.CutPrefix(t, "color:"); ok {
			return c
		}
	}
	return ""
}

// styleSetColor replaces any existing color with the given one. Re-picking the
// color already in effect clears it, so the picker doubles as a toggle.
func styleSetColor(style, color string) string {
	var out []string
	for _, t := range styleTokens(style) {
		if strings.HasPrefix(t, "color:") {
			continue
		}
		out = append(out, t)
	}
	if color != "" && styleColor(style) != color {
		out = append(out, "color:"+color)
	}
	return strings.Join(out, ",")
}

// styleAttrs returns the SGR attribute codes (bold/italic/underline) implied by
// the style, to be appended to the per-row attribute string in render.
func styleAttrs(style string) string {
	var s string
	if styleHas(style, "bold") {
		s += cBold
	}
	if styleHas(style, "italic") {
		s += cItalic
	}
	if styleHas(style, "underline") {
		s += cUnderline
	}
	return s
}

// styleBaseColor returns the SGR foreground code for the node's color, or "" to
// keep the default foreground.
func styleBaseColor(style string) string {
	return styleColorCode[styleColor(style)]
}
