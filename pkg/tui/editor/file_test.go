package editor

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNormalizeFilePath(t *testing.T) {
	home, _ := os.UserHomeDir()
	if got := normalizeFilePath("~/x"); got != filepath.Join(home, "x") {
		t.Fatalf("~ expand: got %q want %q", got, filepath.Join(home, "x"))
	}
	if got := normalizeFilePath("/a/b/../c"); got != "/a/c" {
		t.Fatalf("clean: got %q want /a/c", got)
	}
	if got := normalizeFilePath(""); got != "" {
		t.Fatalf("empty should stay empty, got %q", got)
	}
	// a relative path becomes absolute under the working dir
	wd, _ := os.Getwd()
	if got := normalizeFilePath("rel/path"); got != filepath.Join(wd, "rel/path") {
		t.Fatalf("relative→abs: got %q want %q", got, filepath.Join(wd, "rel/path"))
	}
}
