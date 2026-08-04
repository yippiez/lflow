package editor

import (
	"testing"

	"github.com/lflow/lflow/pkg/tui/style"
)

// hsv returns the saturation and value of an SGR foreground, the two numbers
// "light" is a claim about: a light color is brighter (higher value) AND more
// colorful (higher saturation), not just washed out toward white.
func hsv(t *testing.T, sgr string) (sat, val float64) {
	t.Helper()
	r, g, b, ok := sgrRGB(sgr)
	if !ok {
		t.Fatalf("not a truecolor SGR: %q", sgr)
	}
	hi := float64(max(r, max(g, b))) / 255
	lo := float64(min(r, min(g, b))) / 255
	if hi == 0 {
		return 0, 0
	}
	return (hi - lo) / hi, hi
}

// TestPaletteCoversEveryColorName: a name in the shared vocabulary with no SGR
// behind it renders as unstyled text — the node keeps a color the eye never
// sees. Every theme has to answer for every name.
func TestPaletteCoversEveryColorName(t *testing.T) {
	defer applyTheme(themes[0])
	for _, th := range themes {
		applyTheme(th)
		if len(styleColorCode) != len(style.Colors) {
			t.Errorf("theme %s: %d swatches for %d names", th.name, len(styleColorCode), len(style.Colors))
		}
		for _, name := range style.Colors {
			code, ok := styleColorCode[name]
			if !ok || code == "" {
				t.Errorf("theme %s has no swatch for %q", th.name, name)
				continue
			}
			if _, _, _, ok := sgrRGB(code); !ok {
				t.Errorf("theme %s swatch %q is not a truecolor SGR: %q", th.name, name, code)
			}
		}
	}
}

// TestLightColorsAreLighter: the light variants have to earn their names in
// every theme — lightgreen brighter AND more saturated than green (neon, not
// pale), and plain purple darker than lightpurple, which is the purple lflow
// shipped first.
func TestLightColorsAreLighter(t *testing.T) {
	defer applyTheme(themes[0])
	for _, th := range themes {
		applyTheme(th)

		gs, gv := hsv(t, styleColorCode["green"])
		ls, lv := hsv(t, styleColorCode["lightgreen"])
		if lv <= gv {
			t.Errorf("theme %s: lightgreen value %.2f is not brighter than green %.2f", th.name, lv, gv)
		}
		if ls <= gs {
			t.Errorf("theme %s: lightgreen saturation %.2f is not above green %.2f — pale, not neon",
				th.name, ls, gs)
		}

		dark := lumOf(t, styleColorCode["purple"])
		light := lumOf(t, styleColorCode["lightpurple"])
		if dark >= light {
			t.Errorf("theme %s: purple luminance %.3f is not darker than lightpurple %.3f",
				th.name, dark, light)
		}
	}
}

// TestLightpurpleIsTheOldPurple: the rename must not restyle anything already
// on screen — lightpurple is byte-for-byte what "purple" used to render, and the
// UI's own magenta decoration still reads from that same theme color.
func TestLightpurpleIsTheOldPurple(t *testing.T) {
	defer applyTheme(themes[0])
	for _, th := range themes {
		applyTheme(th)
		if styleColorCode["lightpurple"] != th.purple {
			t.Errorf("theme %s: lightpurple %q is not the theme's purple %q",
				th.name, styleColorCode["lightpurple"], th.purple)
		}
		if cMagenta != th.purple {
			t.Errorf("theme %s: UI magenta %q drifted off the theme's purple %q",
				th.name, cMagenta, th.purple)
		}
	}
}

// TestPaletteStaysReadable: every swatch is a color somebody sets on TEXT, so
// each one has to clear a reasonable contrast against the page it is read on.
// The dark purple is the tight one — dark enough to be a different color, light
// enough to still be words.
func TestPaletteStaysReadable(t *testing.T) {
	defer applyTheme(themes[0])
	for _, th := range themes {
		applyTheme(th)
		page := relLuminance(30, 30, 30) // the darkest page any theme draws
		for _, name := range style.Colors {
			l := lumOf(t, styleColorCode[name])
			if ratio := (l + 0.05) / (page + 0.05); ratio < 3 {
				t.Errorf("theme %s: %s contrasts %.2f:1 with the page — too dim to read",
					th.name, name, ratio)
			}
		}
	}
}

func lumOf(t *testing.T, sgr string) float64 {
	t.Helper()
	r, g, b, ok := sgrRGB(sgr)
	if !ok {
		t.Fatalf("not a truecolor SGR: %q", sgr)
	}
	return relLuminance(r, g, b)
}

// TestStylePickerFollowsTheVocabulary: the picker is generated, so a color added
// to the shared list shows up with a label instead of an unnamed blank row.
func TestStylePickerFollowsTheVocabulary(t *testing.T) {
	if got, want := len(stylePickerItems), len(style.Attrs)+len(style.Colors); got != want {
		t.Fatalf("picker has %d items, want %d", got, want)
	}
	for _, sp := range stylePickerItems {
		if stylePickerLabels[sp.value] == "" {
			t.Errorf("picker item %q has no label", sp.value)
		}
	}
	for _, want := range []struct{ value, label string }{
		{"lightgreen", "Light green"},
		{"lightpurple", "Light purple"},
		{"strike", "Strikethrough"},
		{"red", "Red"},
	} {
		if got := stylePickerLabels[want.value]; got != want.label {
			t.Errorf("label for %q = %q, want %q", want.value, got, want.label)
		}
	}
	// the light variant sits next to the hue it is a take on, not at the end
	for i, sp := range stylePickerItems {
		if sp.value != "lightgreen" {
			continue
		}
		if stylePickerItems[i-1].value != "green" {
			t.Errorf("lightgreen follows %q, want green", stylePickerItems[i-1].value)
		}
	}
}
