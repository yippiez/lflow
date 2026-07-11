package editor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCwdShort(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"/home/eren/work2/lflow", "…/work2/lflow"},
		{"/home/eren", "/home/eren"},
		{"/tmp", "/tmp"},
		{"/", "/"},
		{"", ""},
		{".", ""},
		{"/a/b/c/d", "…/c/d"},
	}
	for _, tc := range cases {
		if got := cwdShort(tc.in); got != tc.want {
			t.Errorf("cwdShort(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestBottomBarShowsCwd: the status bar paints process Getwd as the last two
// path segments (cwd …/parent/leaf), not a stored path.
func TestBottomBarShowsCwd(t *testing.T) {
	m := newTestModel(120, "alpha")
	lines := m.bottomBar(120)
	if len(lines) == 0 {
		t.Fatal("empty status bar")
	}
	bar := strings.Join(lines, "\n")
	pwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	want := "cwd " + cwdShort(pwd)
	if !strings.Contains(bar, want) {
		t.Fatalf("status bar missing %q:\n%s", want, bar)
	}
	// must not dump the full path when deeper than two segments
	if segs := strings.Split(filepath.Clean(pwd), string(filepath.Separator)); len(segs) > 3 {
		if strings.Contains(bar, pwd) {
			t.Fatalf("status bar should truncate deep paths, still has full %q:\n%s", pwd, bar)
		}
	}
}
