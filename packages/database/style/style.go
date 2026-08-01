// Package style holds the node styling vocabulary — the text attributes and
// color names that make up a node's comma-separated style token list, e.g.
// "bold,color:blue". It is the single source of truth shared by the editor's
// /style picker (which renders the tokens to SGR) and the CLI's --style/--color
// flags (which set them). Only plain tokens live here; the SGR escape codes stay
// in the editor, which owns rendering.
package style

import (
	"sort"
	"strings"

	"github.com/pkg/errors"
)

// Attrs is the set of boolean text attributes, in picker order.
var Attrs = []string{"bold", "italic", "underline", "strike"}

// Colors is the eight color names, in picker order.
var Colors = []string{"red", "orange", "yellow", "green", "cyan", "blue", "purple", "gray"}

func isAttr(tok string) bool {
	for _, a := range Attrs {
		if a == tok {
			return true
		}
	}
	return false
}

func isColor(name string) bool {
	for _, c := range Colors {
		if c == name {
			return true
		}
	}
	return false
}

// Tokens splits a style string into its tokens, dropping empties.
func Tokens(style string) []string {
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

// Has reports whether the style carries the given attribute token.
func Has(style, tok string) bool {
	for _, t := range Tokens(style) {
		if t == tok {
			return true
		}
	}
	return false
}

// Set adds the attribute token if on, removes it if not, preserving the order
// of the remaining tokens.
func Set(style, tok string, on bool) string {
	var out []string
	for _, t := range Tokens(style) {
		if t == tok {
			continue
		}
		out = append(out, t)
	}
	if on {
		out = append(out, tok)
	}
	return strings.Join(out, ",")
}

// Toggle adds the attribute token if absent, removes it if present.
func Toggle(style, tok string) string {
	return Set(style, tok, !Has(style, tok))
}

// Color returns the current color name, or "" when none is set.
func Color(style string) string {
	for _, t := range Tokens(style) {
		if c, ok := strings.CutPrefix(t, "color:"); ok {
			return c
		}
	}
	return ""
}

// SetColor replaces any existing color with the given one. An empty color, or
// re-picking the color already in effect, clears it — so the picker doubles as a
// toggle.
func SetColor(style, color string) string {
	var out []string
	for _, t := range Tokens(style) {
		if strings.HasPrefix(t, "color:") {
			continue
		}
		out = append(out, t)
	}
	if color != "" && Color(style) != color {
		out = append(out, "color:"+color)
	}
	return strings.Join(out, ",")
}

// Validate checks that every token in style is a known attribute or a
// "color:<name>" with a known color. An empty style is valid (unstyled).
func Validate(style string) error {
	for _, t := range Tokens(style) {
		if name, ok := strings.CutPrefix(t, "color:"); ok {
			if !isColor(name) {
				return errors.Errorf("unknown color %q: %s", name, strings.Join(Colors, ", "))
			}
			continue
		}
		if !isAttr(t) {
			return errors.Errorf("unknown style token %q: %s, or color:<name>", t, strings.Join(Attrs, ", "))
		}
	}
	return nil
}

// Change describes a style edit requested from CLI flags. A nil pointer means
// the corresponding flag was not set, so that aspect is left unchanged; this
// lets one struct express "set bold on, leave color alone" unambiguously.
type Change struct {
	Style     *string // --style: replace the whole token list
	Color     *string // --color: set the color ("" clears it)
	Bold      *bool   // --bold
	Italic    *bool   // --italic
	Underline *bool   // --underline
	Strike    *bool   // --strike
}

// Any reports whether the change touches anything.
func (c Change) Any() bool {
	return c.Style != nil || c.Color != nil || c.Bold != nil ||
		c.Italic != nil || c.Underline != nil || c.Strike != nil
}

// Apply applies the change to the current style and returns the validated,
// canonically-ordered result. --style replaces wholesale first, then the
// color and attribute flags refine it, so `--style bold --color red` composes.
func (c Change) Apply(cur string) (string, error) {
	s := cur
	if c.Style != nil {
		s = *c.Style
	}
	if c.Color != nil {
		var out []string
		for _, t := range Tokens(s) {
			if !strings.HasPrefix(t, "color:") {
				out = append(out, t)
			}
		}
		if *c.Color != "" {
			out = append(out, "color:"+*c.Color)
		}
		s = strings.Join(out, ",")
	}
	for _, a := range []struct {
		tok string
		on  *bool
	}{{"bold", c.Bold}, {"italic", c.Italic}, {"underline", c.Underline}, {"strike", c.Strike}} {
		if a.on != nil {
			s = Set(s, a.tok, *a.on)
		}
	}
	if err := Validate(s); err != nil {
		return "", err
	}
	return Normalize(s), nil
}

// Normalize de-duplicates tokens and orders them canonically (attributes in
// Attrs order, then a single color), so equal styles compare equal as strings.
func Normalize(style string) string {
	seen := map[string]bool{}
	for _, t := range Tokens(style) {
		seen[t] = true
	}
	var out []string
	for _, a := range Attrs {
		if seen[a] {
			out = append(out, a)
		}
	}
	var colors []string
	for t := range seen {
		if strings.HasPrefix(t, "color:") {
			colors = append(colors, t)
		}
	}
	sort.Strings(colors)
	out = append(out, colors...)
	return strings.Join(out, ",")
}
