package editor

import (
	"strings"
	"testing"
	"time"
)

// logMuteFrom returns a RUNE index (render.go indexes the rune slice), so a
// multibyte body before the separator must not shift the mute point.
func TestLogMuteFrom(t *testing.T) {
	if i := logMuteFrom("did thing · 12m"); i != 9 {
		t.Fatalf("mute index = %d, want 9", i)
	}
	if i := logMuteFrom("çay ☕ · ok"); i != 5 {
		t.Fatalf("mute index on multibyte = %d, want 5", i)
	}
	if i := logMuteFrom("no separator"); i != -1 {
		t.Fatalf("mute index without separator = %d, want -1", i)
	}
}

func TestLogPrefixShowsCreationTime(t *testing.T) {
	at := time.Date(2026, 7, 11, 14, 2, 0, 0, time.Local)
	it := &item{addedOn: at.UnixNano()}
	if got := logPrefix(it); !strings.Contains(got, "(2026-07-11 14:02) ") {
		t.Fatalf("prefix = %q, want the (2026-07-11 14:02) time chip", got)
	}
}
