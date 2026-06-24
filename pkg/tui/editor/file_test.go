package editor

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFileCandidatesAndPrefix(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"work", "work2", "worktrees", "notes", ".hidden"} {
		if err := os.Mkdir(filepath.Join(dir, n), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	got := fileCandidates(filepath.Join(dir, "wor"))
	want := []string{
		filepath.Join(dir, "work") + "/",
		filepath.Join(dir, "work2") + "/",
		filepath.Join(dir, "worktrees") + "/",
	}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("candidate %d: got %q want %q", i, got[i], want[i])
		}
	}

	// longest common prefix advances "…/wor" → "…/work"
	if cp := commonPrefix(got); cp != filepath.Join(dir, "work") {
		t.Fatalf("commonPrefix = %q, want %q", cp, filepath.Join(dir, "work"))
	}

	// hidden entries stay hidden until the user types a dot
	if c := fileCandidates(filepath.Join(dir, "")); contains(c, filepath.Join(dir, ".hidden")+"/") {
		t.Fatalf("hidden dir leaked into %v", c)
	}
	if c := fileCandidates(dir + "/."); !contains(c, dir+"/.hidden/") {
		t.Fatalf("dot prefix should reveal hidden, got %v", c)
	}
}

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

func contains(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}
