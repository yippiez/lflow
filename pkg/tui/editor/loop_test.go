package editor

import (
	"testing"
	"time"
)

func TestUltraloopParse(t *testing.T) {
	cases := []struct {
		in   string
		want time.Duration
		ok   bool
	}{
		{"just a note", 0, false},
		{"ultraloop check the build", time.Minute, true},      // default 1m
		{"ultraloop 10m summarize", 10 * time.Minute, true},   // minutes
		{"ultraloop 30s ping", 30 * time.Second, true},        // seconds
		{"ultraloop 2h digest", 2 * time.Hour, true},          // hours
		{"watch it: ULTRALOOP 5m", 5 * time.Minute, true},     // case-insensitive, mid-text
	}
	for _, c := range cases {
		got, ok := ultraloopParse(c.in)
		if ok != c.ok || got != c.want {
			t.Errorf("ultraloopParse(%q) = %v,%v; want %v,%v", c.in, got, ok, c.want, c.ok)
		}
	}
}

func TestUltraloopStrip(t *testing.T) {
	if got := ultraloopStrip("ultraloop 10m check the build"); got != "check the build" {
		t.Errorf("strip = %q", got)
	}
	if got := ultraloopStrip("ultraloop summarize"); got != "summarize" {
		t.Errorf("strip default = %q", got)
	}
	if got := ultraloopStrip("plain task"); got != "plain task" {
		t.Errorf("strip none = %q", got)
	}
}
