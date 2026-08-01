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
// path segments (…/parent/leaf), without a redundant "cwd" label.
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
	want := cwdShort(pwd)
	if !strings.Contains(bar, " · "+want) {
		t.Fatalf("status bar missing path %q:\n%s", want, bar)
	}
	if strings.Contains(bar, "cwd ") {
		t.Fatalf("status bar should show only the path, not a cwd label:\n%s", bar)
	}
	// must not dump the full path when deeper than two segments
	if segs := strings.Split(filepath.Clean(pwd), string(filepath.Separator)); len(segs) > 3 {
		if strings.Contains(bar, pwd) {
			t.Fatalf("status bar should truncate deep paths, still has full %q:\n%s", pwd, bar)
		}
	}
}

// TestBottomBarKeepsMainWhenInTemp: entering the Temporary Domain must not
// rewrite the status bar to "Temp · 1/1" — breadcrumb and cursor stay on the
// main outline the user left behind.
func TestBottomBarKeepsMainWhenInTemp(t *testing.T) {
	m := newTestModel(120, "alpha", "beta", "gamma")
	m.cursor = 1 // on beta → "2/3"
	before := stripSGR(strings.Join(m.bottomBar(120), "\n"))
	if !strings.Contains(before, "2/3") {
		t.Fatalf("precondition: bar should show 2/3:\n%s", before)
	}
	m.enterTemp()
	after := stripSGR(strings.Join(m.bottomBar(120), "\n"))
	if strings.Contains(after, "Temp") {
		t.Fatalf("bar must not say Temp while focused in temp:\n%s", after)
	}
	if !strings.Contains(after, "2/3") {
		t.Fatalf("bar must keep main cursor 2/3 while in temp:\n%s", after)
	}
	// still no "1/1" from the temp panel's single empty node
	if strings.Contains(after, "1/1") {
		t.Fatalf("bar must not show temp position 1/1:\n%s", after)
	}
}
