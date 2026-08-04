package nodes

import (
	"os"
	"path/filepath"
	"testing"
)

// TestExamplesRoundTrip keeps examples/ honest: every example file must be
// in normal form already (parse→render reproduces it byte-identically), so
// opening one in the editor and saving changes nothing.
func TestExamplesRoundTrip(t *testing.T) {
	dir := filepath.Join("..", "..", "examples")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Skipf("examples dir not found: %v", err)
	}
	tested := 0
	for _, e := range entries {
		if e.IsDir() || e.Name() == "README.md" {
			continue // the README documents the matrix, it is not a demo doc
		}
		c, ok := CodecForPath(e.Name())
		if !ok {
			continue
		}
		src, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		roundTrip(t, c, string(src), string(src))
		tested++
	}
	if tested < 5 {
		t.Fatalf("expected all 5 example files to be tested, got %d", tested)
	}
}
