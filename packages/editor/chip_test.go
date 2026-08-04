package editor

import (
	"strings"
	"testing"
	"time"

	"github.com/lflow/lflow/packages/database"
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
		"l1": {ID: "l1", Kind: "link", Value: "https://example.com/readme", Label: "readme"},
	}
	name := "open " + chipAnchor("l1") + " please"

	if got, want := expandAnchors(name, chips), "open [readme](https://example.com/readme) please"; got != want {
		t.Errorf("expand = %q, want %q", got, want)
	}
	if got, want := displayAnchors(name, chips), "open →readme please"; got != want {
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

// TestChipMovesWholeAcrossAWrap: a chip is atomic on screen too — when it does
// not fit on the current visual line it moves down whole instead of splitting
// at the space inside its own name, which is what the caret model (chipVisualRows,
// which walks anchors as one cluster) already assumes.
func TestUndoRestoresDeletedChipRecord(t *testing.T) {
	m, _ := dbModel(t, database.Node{UUID: "n", Name: ""})
	c := database.Chip{ID: "undo-chip", Kind: chipKindTag, Value: "bug"}
	if err := database.UpsertChip(m.db, c); err != nil {
		t.Fatal(err)
	}
	m.chips[c.ID] = c
	n := m.tree.byUUID["n"]
	n.name = database.ChipAnchor(c.ID)
	cursorOn(m, "n")
	clipCapture(t)

	m.caret = 0
	m.press("shift+right") // chip anchors are atomic: selects the whole chip
	m.press("x")
	if got := displayAnchors(n.name, m.chips); got != "" {
		t.Fatalf("cut left %q", got)
	}
	if _, err := database.GetChip(m.db, c.ID); err == nil {
		t.Fatal("cut left the chip record in the database")
	}

	m.undo()
	n = m.tree.byUUID["n"]
	if got := displayAnchors(n.name, m.chips); got != "#bug" {
		t.Fatalf("undo restored %q, want #bug rather than @?", got)
	}
	if got, err := database.GetChip(m.db, c.ID); err != nil || got.Value != "bug" {
		t.Fatalf("undo did not restore persisted chip: %+v err=%v", got, err)
	}
}

func TestChipMovesWholeAcrossAWrap(t *testing.T) {
	chips := map[string]database.Chip{
		"c1": {ID: "c1", Kind: chipKindLink, Value: "https://example.com/x", Label: "Q3 planning notes"},
	}
	name := "some text ahead of it " + chipAnchor("c1")
	body := renderBody(&item{typ: database.TypeBullets}, name, -1, false, chips)
	lines := wrapLine(" ○ "+body, 40, cDim+" │ "+cReset)

	if len(lines) != 2 {
		t.Fatalf("want 2 visual lines, got %d: %q", len(lines), lines)
	}
	const disp = "→Q3 planning notes"
	if got := stripSGR(lines[1]); !strings.Contains(got, nonBreaking(disp)) {
		t.Errorf("the chip did not move down whole: %q", got)
	}
	if got := stripSGR(lines[0]); strings.Contains(got, "Q3") {
		t.Errorf("the chip was split across the wrap: %q", got)
	}
}
