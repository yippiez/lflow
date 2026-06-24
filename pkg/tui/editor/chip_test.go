package editor

import (
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
	if got, want := displayAnchors(name, chips), "open @readme.txt please"; got != want {
		t.Errorf("display = %q, want %q", got, want)
	}
	// a missing record degrades to a placeholder, never a raw anchor
	if got := displayAnchors("x"+chipAnchor("gone"), nil); got != "x@?" {
		t.Errorf("orphan display = %q, want x@?", got)
	}
}

func TestChipTokenAt(t *testing.T) {
	runes := []rune("cat @~/work/r")
	start, end, ok := chipTokenAt(runes, len(runes))
	if !ok || string(runes[start:end]) != "@~/work/r" {
		t.Fatalf("token = %q ok=%v, want @~/work/r", string(runes[start:end]), ok)
	}
	// an email @ is not a token boundary
	if _, _, ok := chipTokenAt([]rune("mail a@b.com"), len("mail a@b.com")); ok {
		t.Errorf("email @ should not be a path token")
	}
}
