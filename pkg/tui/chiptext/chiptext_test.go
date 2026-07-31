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

func TestTagSpansNumberLiteral(t *testing.T) {
	// "#1" means "number one": a tag word must start with a letter or underscore.
	got := TagSpans("episode #1 has tag #ep")
	if len(got) != 1 || got[0] != [2]int{19, 22} {
		t.Fatalf("expected only the #ep tag at [19,22), got %v", got)
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

func TestURLSpans(t *testing.T) {
	got := URLSpans("see https://example.com/x and www.foo.com, ok? bare word http")
	if len(got) != 2 {
		t.Fatalf("expected 2 urls, got %d (%v)", len(got), got)
	}
	if got := URLSpans("visit https://x.com."); len(got) != 1 {
		t.Fatalf("expected 1 url with trailing period trimmed, got %v", got)
	} else if end := got[0][1]; end != len("visit https://x.com") {
		t.Errorf("trailing period should be trimmed, span end = %d", end)
	}
	if got := URLSpans("(https://en.wikipedia.org/wiki/Go_(language))"); len(got) != 1 {
		t.Fatalf("expected 1 url, got %v", got)
	} else if end := got[0][1]; end != len("(https://en.wikipedia.org/wiki/Go_(language)") {
		t.Errorf("balanced paren should survive, unbalanced outer paren trimmed, span end = %d", end)
	}
	if got := URLSpans("nothttps://not-a-url xhttps://also-not"); len(got) != 0 {
		t.Errorf("scheme glued to a preceding word must not match, got %v", got)
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

func TestChipifyBareURL(t *testing.T) {
	var seen []string
	out := Chipify("see https://example.com/x for details", mk(&seen))
	if len(seen) != 1 || seen[0] != "link:example.com=https://example.com/x" {
		t.Errorf("chips seen = %v, want [link:example.com=https://example.com/x]", seen)
	}
	if !strings.HasPrefix(out, "see <link> for") {
		t.Errorf("unexpected rewrite: %q", out)
	}
}

func TestChipifyBareURLKnownService(t *testing.T) {
	var seen []string
	Chipify("budget https://docs.google.com/spreadsheets/d/1abc", mk(&seen))
	if len(seen) != 1 || seen[0] != "link:Sheets=https://docs.google.com/spreadsheets/d/1abc" {
		t.Errorf("chips seen = %v, want a Sheets-labeled link", seen)
	}
}

func TestChipifyWWWURLGetsScheme(t *testing.T) {
	var seen []string
	Chipify("see www.example.com now", mk(&seen))
	if len(seen) != 1 || seen[0] != "link:www.example.com=https://www.example.com" {
		t.Errorf("chips seen = %v, want a normalized https link", seen)
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
