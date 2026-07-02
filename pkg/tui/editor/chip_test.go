package editor

import (
	"strings"
	"testing"

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
