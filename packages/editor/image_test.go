package editor

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
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

// TestIsWSLFromEnv: the interop env vars WSL sets are enough on their own, so a
// scrubbed /proc/version cannot hide a WSL host from alt+o.
func TestIsWSLFromEnv(t *testing.T) {
	t.Setenv("WSL_DISTRO_NAME", "")
	t.Setenv("WSL_INTEROP", "")
	baseline := isWSL() // whatever /proc/version says on this machine

	t.Setenv("WSL_DISTRO_NAME", "Ubuntu")
	if !isWSL() {
		t.Error("WSL_DISTRO_NAME set: isWSL() = false, want true")
	}
	t.Setenv("WSL_DISTRO_NAME", "")
	t.Setenv("WSL_INTEROP", "/run/WSL/8_interop")
	if !isWSL() {
		t.Error("WSL_INTEROP set: isWSL() = false, want true")
	}
	t.Setenv("WSL_INTEROP", "")
	if isWSL() != baseline {
		t.Error("clearing the env changed the /proc/version verdict")
	}
}

// TestHostOpenerPicksAnOpener: on a machine with a desktop opener on PATH the
// command is built with the file path as its last argument (never shell-quoted,
// never a string), so a path with spaces survives.
func TestHostOpenerPicksAnOpener(t *testing.T) {
	cmd, via, ok := hostOpener("/tmp/a b/pic.png")
	if !ok {
		t.Skip("no host opener on PATH — nothing to assert")
	}
	if via == "" {
		t.Error("opener name empty")
	}
	args := cmd.Args
	if len(args) < 2 {
		t.Fatalf("opener args = %v, want the path", args)
	}
	last := args[len(args)-1]
	if !strings.Contains(last, "pic.png") {
		t.Errorf("opener last arg = %q, want the image path", last)
	}
}

// TestImageCachePathIsStable: one file per node, under a real directory, so
// repeated alt+o overwrites instead of littering.
func TestImageCachePathIsStable(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	a, err := imageCachePath("abc-123")
	if err != nil {
		t.Fatal(err)
	}
	b, err := imageCachePath("abc-123")
	if err != nil || a != b {
		t.Errorf("cache path not stable: %q vs %q (%v)", a, b, err)
	}
	if filepath.Ext(a) != ".png" || !strings.Contains(a, "abc-123") {
		t.Errorf("cache path = %q, want <cache>/lflow/images/abc-123.png", a)
	}
	if st, err := os.Stat(filepath.Dir(a)); err != nil || !st.IsDir() {
		t.Errorf("cache dir not created: %v", err)
	}
	if other, _ := imageCachePath("zzz-999"); other == a {
		t.Error("two nodes share one cache file")
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
