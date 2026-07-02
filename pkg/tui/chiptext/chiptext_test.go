package chiptext

import (
	"fmt"
	"strings"
	"testing"
)

func TestTagSpans(t *testing.T) {
	got := TagSpans("see #project and #to_do but not a#b or bare #")
	if len(got) != 2 {
		t.Fatalf("expected 2 tags, got %d (%v)", len(got), got)
	}
}

func TestDateSpans(t *testing.T) {
	if got := DateSpans("due 2026-07-01 sharp"); len(got) != 1 {
		t.Errorf("canonical date should match once, got %v", got)
	}
	if got := DateSpans("ship tomorrow"); len(got) != 0 {
		t.Errorf("natural language must not match, got %v", got)
	}
	if got := DateSpans("not 2026-13-40 valid"); len(got) != 0 {
		t.Errorf("impossible date must be rejected, got %v", got)
	}
}

func TestLinkSpans(t *testing.T) {
	got := LinkSpans("see [docs](https://x.com) here")
	if len(got) != 1 {
		t.Fatalf("expected 1 link, got %d", len(got))
	}
	if got[0].Label != "docs" || got[0].Target != "https://x.com" {
		t.Errorf("link parts wrong: %+v", got[0])
	}
}

// mk records each chip and returns a readable sentinel so assertions can see
// which spans were chipified, without a database.
func mk(seen *[]string) func(string, string, string) string {
	return func(kind, value, label string) string {
		if label != "" {
			*seen = append(*seen, fmt.Sprintf("%s:%s=%s", kind, label, value))
		} else {
			*seen = append(*seen, fmt.Sprintf("%s:%s", kind, value))
		}
		return "<" + kind + ">"
	}
}

func TestChipify(t *testing.T) {
	var seen []string
	out := Chipify("ship #project by 2026-07-01 see [docs](https://x.com)", mk(&seen))

	want := []string{"tag:project", "date:2026-07-01", "link:docs=https://x.com"}
	if strings.Join(seen, ",") != strings.Join(want, ",") {
		t.Errorf("chips seen = %v, want %v", seen, want)
	}
	if strings.Contains(out, "#project") || strings.Contains(out, "2026-07-01") || strings.Contains(out, "[docs]") {
		t.Errorf("inline forms should be replaced by anchors: %q", out)
	}
	if !strings.HasPrefix(out, "ship <tag> by <date> see <link>") {
		t.Errorf("unexpected rewrite: %q", out)
	}
}

func TestChipifyTagInsideLinkStaysLink(t *testing.T) {
	var seen []string
	Chipify("[#1234](https://bug/1234)", mk(&seen))
	if len(seen) != 1 || !strings.HasPrefix(seen[0], "link:") {
		t.Errorf("a tag inside a link label must not also chipify: %v", seen)
	}
}

func TestChipifyNoForms(t *testing.T) {
	in := "just plain text, nothing here"
	if out := Chipify(in, mk(new([]string))); out != in {
		t.Errorf("plain text should be untouched, got %q", out)
	}
}

func TestChipifyDeclineKeepsText(t *testing.T) {
	out := Chipify("a #tag here", func(string, string, string) string { return "" })
	if out != "a #tag here" {
		t.Errorf("declined chip should leave original text, got %q", out)
	}
}
