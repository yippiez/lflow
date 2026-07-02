package editor

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"os"
	"strings"
	"testing"
)

// encodeTestPNG renders an image to PNG bytes for tests that need a decodable blob.
func encodeTestPNG(t *testing.T, img image.Image) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// solidImage builds a w×h image filled with one color, for deterministic
// half-block sampling.
func solidImage(w, h int, c color.Color) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, c)
		}
	}
	return img
}

// TestHalfBlockRenderShape checks the render is renderer-safe: exactly `rows`
// lines, each with `cols` visible cells (the ▀ glyphs), so visibleWidth — which
// only skips SGR sequences ending in 'm' — measures it correctly.
func TestHalfBlockRenderShape(t *testing.T) {
	img := solidImage(10, 10, color.RGBA{R: 200, G: 100, B: 50, A: 255})
	lines := halfBlockRender(img, 8, 3)
	if len(lines) != 3 {
		t.Fatalf("rows = %d, want 3", len(lines))
	}
	for i, l := range lines {
		if got := visibleWidth(l); got != 8 {
			t.Errorf("line %d visibleWidth = %d, want 8", i, got)
		}
		if strings.Count(l, halfBlock) != 8 {
			t.Errorf("line %d has %d blocks, want 8", i, strings.Count(l, halfBlock))
		}
	}
}

// TestHalfBlockRenderDegenerate guards the zero/empty cases from panicking.
func TestHalfBlockRenderDegenerate(t *testing.T) {
	img := solidImage(4, 4, color.White)
	if got := halfBlockRender(img, 0, 3); got != nil {
		t.Errorf("cols=0 → %v, want nil", got)
	}
	if got := halfBlockRender(solidImage(1, 1, color.Black), 5, 0); got != nil {
		t.Errorf("rows=0 → %v, want nil", got)
	}
}

// TestHalfBlockFit preserves aspect: a wide image gets few rows, a tall one many.
func TestHalfBlockFit(t *testing.T) {
	wide := solidImage(100, 10, color.White) // 10:1 → cols/20 rows
	if r := halfBlockFit(wide, 40); r != 2 {
		t.Errorf("wide fit = %d, want 2", r)
	}
	tall := solidImage(10, 100, color.White) // 1:10
	if r := halfBlockFit(tall, 40); r != 200 {
		t.Errorf("tall fit = %d, want 200", r)
	}
	// never zero, even for a pathologically wide image
	if r := halfBlockFit(solidImage(1000, 1, color.White), 4); r < 1 {
		t.Errorf("fit = %d, want >= 1", r)
	}
}

// clearGraphicsEnv blanks every var detectGraphicsProto reads, so a test starts
// from a known "no protocol" baseline regardless of the host terminal.
func clearGraphicsEnv(t *testing.T) {
	for _, k := range []string{"TERM", "TERM_PROGRAM", "KITTY_WINDOW_ID",
		"GHOSTTY_RESOURCES_DIR", "WEZTERM_PANE", "KONSOLE_VERSION", "LC_TERMINAL"} {
		t.Setenv(k, "")
	}
}

func TestDetectGraphicsProto(t *testing.T) {
	cases := []struct {
		name, key, val string
		want           graphicsProto
	}{
		{"kitty", "KITTY_WINDOW_ID", "1", protoKitty},
		{"kitty-term", "TERM", "xterm-kitty", protoKitty},
		{"ghostty", "TERM", "xterm-ghostty", protoKitty},
		{"wezterm", "TERM_PROGRAM", "WezTerm", protoKitty},
		{"konsole", "KONSOLE_VERSION", "220400", protoKitty},
		{"iterm", "TERM_PROGRAM", "iTerm.app", protoITerm},
		{"lc-iterm", "LC_TERMINAL", "iTerm2", protoITerm},
		{"foot", "TERM", "foot", protoSixel},
		{"mlterm", "TERM", "mlterm", protoSixel},
		{"none", "TERM", "xterm-256color", protoNone},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			clearGraphicsEnv(t)
			t.Setenv(c.key, c.val)
			if got := detectGraphicsProto(); got != c.want {
				t.Errorf("detectGraphicsProto() = %d, want %d", got, c.want)
			}
		})
	}
}

// TestGraphicsProtoOverride: LFLOW_IMAGE_PROTO forces the protocol regardless of
// the terminal (kitty here, which would otherwise be xterm→none).
func TestGraphicsProtoOverride(t *testing.T) {
	for val, want := range map[string]graphicsProto{
		"kitty": protoKitty, "iterm": protoITerm, "sixel": protoSixel, "none": protoNone,
	} {
		clearGraphicsEnv(t)
		t.Setenv("TERM", "xterm-256color")
		t.Setenv("LFLOW_IMAGE_PROTO", val)
		if got := detectGraphicsProto(); got != want {
			t.Errorf("override %q = %d, want %d", val, got, want)
		}
	}
}

func TestBuildGraphicsSequence(t *testing.T) {
	png := []byte{0x89, 'P', 'N', 'G', 1, 2, 3, 4}

	// kitty: APC _G … ST, transmits the PNG by a temp file path we must clean up.
	seq, paths, err := buildGraphicsSequence(protoKitty, png, 80)
	if err != nil {
		t.Fatal(err)
	}
	s := string(seq)
	if !strings.HasPrefix(s, "\x1b_Ga=T,f=100,t=f,c=80;") || !strings.HasSuffix(s, "\x1b\\") {
		t.Errorf("kitty sequence malformed: %q", s)
	}
	if len(paths) != 1 {
		t.Fatalf("kitty temp paths = %v, want 1", paths)
	}
	if got, err := os.ReadFile(paths[0]); err != nil || string(got) != string(png) {
		t.Errorf("kitty temp file mismatch: %v %v", got, err)
	}
	os.Remove(paths[0])

	// iTerm2: OSC 1337 inline, base64 payload, BEL-terminated, no temp files.
	seq, paths, err = buildGraphicsSequence(protoITerm, png, 80)
	if err != nil {
		t.Fatal(err)
	}
	s = string(seq)
	if !strings.HasPrefix(s, "\x1b]1337;File=inline=1;preserveAspectRatio=1;width=80;size=8:") || !strings.HasSuffix(s, "\a") {
		t.Errorf("iterm sequence malformed: %q", s)
	}
	if len(paths) != 0 {
		t.Errorf("iterm temp paths = %v, want none", paths)
	}

	// sixel: needs a real (decodable) PNG, so encode a small gradient first.
	pngBytes := encodeTestPNG(t, solidImage(12, 12, color.RGBA{R: 200, G: 40, B: 90, A: 255}))
	seq, paths, err = buildGraphicsSequence(protoSixel, pngBytes, 20)
	if err != nil {
		t.Fatal(err)
	}
	s = string(seq)
	if !strings.HasPrefix(s, "\x1bPq") || !strings.HasSuffix(s, "\x1b\\") {
		t.Errorf("sixel sequence malformed (prefix/suffix): %q…", s[:min(40, len(s))])
	}
	if !strings.Contains(s, `"1;1;`) {
		t.Error("sixel sequence missing raster attributes")
	}
	if len(paths) != 0 {
		t.Errorf("sixel temp paths = %v, want none", paths)
	}
}

// TestSixelEncodeStructure checks the encoder frames the stream correctly and is
// deterministic (sorted colors per band).
func TestSixelEncodeStructure(t *testing.T) {
	img := solidImage(18, 13, color.RGBA{R: 10, G: 220, B: 120, A: 255})
	a := sixelEncode(img, 0) // 0 = no downscale
	b := sixelEncode(img, 0)
	if string(a) != string(b) {
		t.Error("sixel encode not deterministic")
	}
	s := string(a)
	if !strings.HasPrefix(s, "\x1bPq") || !strings.HasSuffix(s, "\x1b\\") {
		t.Errorf("sixel framing wrong: %q…", s[:min(30, len(s))])
	}
	if !strings.Contains(s, "#") || !strings.ContainsRune(s, '-') {
		t.Error("sixel missing color select or band newline")
	}
}

func TestHumanSize(t *testing.T) {
	cases := []struct {
		n    int64
		want string
	}{
		{512, "512B"},
		{2048, "2KB"},
		{5 * 1024 * 1024, "5.0MB"},
	}
	for _, c := range cases {
		if got := humanSize(c.n); got != c.want {
			t.Errorf("humanSize(%d) = %q, want %q", c.n, got, c.want)
		}
	}
}
