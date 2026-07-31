package editor

import (
	"strings"
	"testing"
	"time"

	"github.com/lflow/lflow/pkg/tui/database"
)

func TestAnchorRoundTrip(t *testing.T) {
	a := chipAnchor("abc123")
	spans := anchorSpans([]rune("see " + a + " now"))
	if len(spans) != 1 {
		t.Fatalf("want 1 anchor, got %d", len(spans))
	}
	if spans[0].id != "abc123" {
		t.Fatalf("id = %q, want abc123", spans[0].id)
	}
	// the span covers exactly the anchor text
	runes := []rune("see " + a + " now")
	if string(runes[spans[0].start:spans[0].end]) != a {
		t.Fatalf("span text mismatch")
	}
}

func TestExpandAndDisplayAnchors(t *testing.T) {
	chips := map[string]database.Chip{
		"p1": {ID: "p1", Kind: "path", Value: "/home/eren/work/readme.txt"},
	}
	name := "open " + chipAnchor("p1") + " please"

	if got, want := expandAnchors(name, chips), "open /home/eren/work/readme.txt please"; got != want {
		t.Errorf("expand = %q, want %q", got, want)
	}
	if got, want := displayAnchors(name, chips), "open ›readme.txt please"; got != want {
		t.Errorf("display = %q, want %q", got, want)
	}
	// a missing record degrades to a placeholder, never a raw anchor
	if got := displayAnchors("x"+chipAnchor("gone"), nil); got != "x@?" {
		t.Errorf("orphan display = %q, want x@?", got)
	}
}

// Regression: quitting a zoomed-in / loaded-node editor prints a "→ saved"
// summary that must resolve the root name's chip anchors. Before the fix the
// summary printed the raw name, leaking the sentinel-wrapped chip ids.
func TestSavedSummaryResolvesChipAnchors(t *testing.T) {
	chips := map[string]database.Chip{
		"c1": {ID: "c1", Kind: "date", Value: "2026-07-02"},
	}
	name := "Günü Notları " + chipAnchor("c1")

	out := savedSummary(name, chips, 12, 3)

	if strings.ContainsRune(out, chipSentinel) {
		t.Errorf("summary leaks a chip sentinel: %q", out)
	}
	if strings.Contains(out, "c1") {
		t.Errorf("summary leaks the raw chip id: %q", out)
	}
	if !strings.Contains(out, "2026-07-02") {
		t.Errorf("summary dropped the resolved chip display: %q", out)
	}
	if !strings.Contains(out, "Günü Notları") {
		t.Errorf("summary dropped the node name: %q", out)
	}
}

// TestChipVisualRowsOversizedChipTerminates pins the freeze fix: a chip whose
// display is wider than the whole line is atomic and can never fit — the wrap
// walk must consume it (clipped by the renderer), not re-emit the same line
// start forever. Cursor-on-node hung the editor on long cmd chips before.
func TestChipVisualRowsOversizedChipTerminates(t *testing.T) {
	chips := map[string]database.Chip{
		"c1": {ID: "c1", Kind: "cmd", Value: "rg '^\\s*processing_' " + strings.Repeat("/long/path", 12)},
	}
	name := "reply " + chipAnchor("c1") + " tail"
	done := make(chan []int, 1)
	go func() { done <- chipVisualRows(name, 40, 4, 4, chips) }()
	select {
	case starts := <-done:
		if len(starts) < 2 || len(starts) > 10 {
			t.Fatalf("suspicious wrap: %d visual rows", len(starts))
		}
	case <-time.After(2 * time.Second):
		t.Fatal("chipVisualRows hung on an oversized chip")
	}
}

// TestChipifyBeforeCaretURL: a bare URL ending at the caret commits into a link
// chip exactly like a canonical date does — same trigger (chipifyBeforeCaret,
// called on a committed space/enter), same anchor mechanics.
func TestChipifyBeforeCaretURL(t *testing.T) {
	m := newTestModel(80, "see https://example.com/x")
	cur := m.cursorItem()
	m.caret = len([]rune(cur.name))

	if !m.chipifyBeforeCaret(cur) {
		t.Fatalf("expected chipifyBeforeCaret to convert the URL")
	}
	spans := anchorSpans([]rune(cur.name))
	if len(spans) != 1 {
		t.Fatalf("want 1 anchor, got %d: %q", len(spans), cur.name)
	}
	c, ok := m.chips[spans[0].id]
	if !ok {
		t.Fatalf("chip record missing for id %q", spans[0].id)
	}
	if c.Kind != chipKindLink || c.Value != "https://example.com/x" || c.Label != "example.com" {
		t.Fatalf("chip = %+v, want link https://example.com/x labeled example.com", c)
	}
	if !strings.HasPrefix(cur.name, "see ") {
		t.Fatalf("unexpected rewrite: %q", cur.name)
	}
}

// TestVisualRowsWideRuneNarrowLineTerminates: the plain wrapper has the same
// hazard — a 2-cell rune on a 1-cell line must be consumed, not retried.
func TestVisualRowsWideRuneNarrowLineTerminates(t *testing.T) {
	done := make(chan []int, 1)
	go func() { done <- visualRows(strings.Repeat("界", 5), 1, 0, 0) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("visualRows hung on a wide rune in a 1-cell line")
	}
}
