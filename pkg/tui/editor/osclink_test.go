package editor

import (
	"strings"
	"testing"

	"github.com/lflow/lflow/pkg/tui/database"
)

// A URL link chip wraps itself in an OSC 8 hyperlink whose target is a URL —
// and a URL almost always contains an 'm' (".com"). These guard every ANSI
// scanner against ending that escape inside its own target, which measured the
// rest of the URL as visible text: rows wrapped early, continuation lines
// re-emitted a half-eaten opener, and the terminal ate the line after it.

const colabURL = "https://colab.research.google.com/drive/1QwErTyUiOp"

// linkChips is two Colab links and the text between them — the shape that first
// showed the breakage.
func linkChips() (map[string]database.Chip, string, string) {
	chips := map[string]database.Chip{
		"a": {ID: "a", Kind: chipKindLink, Value: colabURL, Label: "EXA_VoiceToReportBatch1.ipynb"},
		"b": {ID: "b", Kind: chipKindLink, Value: colabURL + "2", Label: "EXA_VoiceToReportBatch2.ipynb"},
	}
	name := chipAnchor("a") + " and " + chipAnchor("b") + " one to repeat after"
	return chips, name, displayAnchors(name, chips)
}

// spaced turns a rendered chip's NBSP back into a plain space, so a test can
// compare what the screen shows against the chip's display text.
func spaced(s string) string { return strings.ReplaceAll(s, "\u00a0", " ") }

// TestVisibleWidthSkipsHyperlink: a hyperlink target occupies no columns.
func TestVisibleWidthSkipsHyperlink(t *testing.T) {
	chips, name, plain := linkChips()
	body := renderBody(&item{typ: database.TypeBullets}, name, -1, false, chips, false)
	if got, want := visibleWidth(body), visibleWidth(plain); got != want {
		t.Errorf("width = %d, want %d (the OSC 8 targets are not text)", got, want)
	}
	// the chip's own spaces render as NBSP so a wrap cannot split it (nonBreaking)
	if got := spaced(stripSGR(body)); got != plain {
		t.Errorf("stripped = %q, want %q", got, plain)
	}
	// clip measures in the same coordinates: 20 columns of a linked row is 20
	// columns of its text (plus the ellipsis marker's own cell)
	if got := visibleWidth(clip(body, 20)); got > 20 {
		t.Errorf("clip left %d columns, want ≤ 20", got)
	}
}

// TestWrapLinkedRowFillsItsLines: a row of link chips wraps like the same row of
// plain text — every line filled and inside the width, nothing dropped, nothing
// repeated, no blank line where a chip used to be.
func TestWrapLinkedRowFillsItsLines(t *testing.T) {
	chips, name, plain := linkChips()
	body := renderBody(&item{typ: database.TypeBullets}, name, -1, false, chips, false)

	const width = 44
	prefix := cDim + " │ " + cReset
	lines := wrapLine(" ○ "+body, width, prefix)

	var text []string
	for i, l := range lines {
		if w := visibleWidth(l); w > width {
			t.Errorf("line %d is %d columns wide, want ≤ %d: %q", i, w, width, l)
		}
		bare := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(spaced(stripSGR(l))), "│"))
		if bare == "" {
			t.Errorf("line %d is blank: %q", i, l)
		}
		text = append(text, bare)
	}
	if got, want := strings.Join(text, " "), "○ "+plain; got != want {
		t.Errorf("wrapped text = %q, want %q", got, want)
	}
	if want := len(wrapLine(" ○ "+plain, width, prefix)); len(lines) != want {
		t.Errorf("linked row took %d lines, the same text takes %d", len(lines), want)
	}
}

// TestWrapClosesHyperlinkAtTheBreak: a hyperlink never spans a wrap — it closes
// at the break and reopens past the rail prefix, so the indent of the
// continuation line is not swallowed into the link.
func TestWrapClosesHyperlinkAtTheBreak(t *testing.T) {
	chips := map[string]database.Chip{
		"a": {ID: "a", Kind: chipKindLink, Value: colabURL, Label: "a long notebook name that must wrap"},
	}
	body := renderBody(&item{typ: database.TypeBullets}, chipAnchor("a"), -1, false, chips, false)
	lines := wrapLine(" ○ "+body, 24, cDim+" │ "+cReset)
	if len(lines) < 2 {
		t.Fatalf("expected the chip to wrap, got %d line(s)", len(lines))
	}
	for i, l := range lines {
		if openLinkTarget(l) != "" {
			t.Errorf("line %d leaves its hyperlink open: %q", i, l)
		}
	}
	if !strings.Contains(lines[1], oscLink(colabURL)) {
		t.Errorf("the continuation did not reopen the hyperlink: %q", lines[1])
	}
	if strings.Index(lines[1], oscLinkPrefix) < strings.Index(lines[1], "│") {
		t.Errorf("the rail prefix sits inside the hyperlink: %q", lines[1])
	}
}

// openLinkTarget returns the OSC 8 target still open at the end of s.
func openLinkTarget(s string) string {
	open := ""
	for i := 0; i < len(s); {
		if s[i] != '\x1b' {
			i++
			continue
		}
		e := ansiEscapeEnd(s, i)
		if target, ok := linkTargetOf(s[i:e]); ok {
			open = target
		}
		i = e
	}
	return open
}

// TestAnsiEscapeEnd covers the escape shapes a rendered line can carry.
func TestAnsiEscapeEnd(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{cReset, len(cReset)},
		{cDim + "x", len(cDim)},
		{oscLink(colabURL), len(oscLink(colabURL))},       // not the 'm' of ".com"
		{oscLink(""), len(oscLink(""))},                   // the closer
		{"\x1b]8;;" + colabURL + "\a", len(colabURL) + 6}, // BEL-terminated
		{"\x1b]8;;unterminated", len("\x1b]8;;unterminated")},
	}
	for _, c := range cases {
		if got := ansiEscapeEnd(c.in, 0); got != c.want {
			t.Errorf("ansiEscapeEnd(%q) = %d, want %d", c.in, got, c.want)
		}
		if got := escapeEndRunes([]rune(c.in), 0); got != len([]rune(c.in[:c.want])) {
			t.Errorf("escapeEndRunes(%q) = %d, want %d", c.in, got, len([]rune(c.in[:c.want])))
		}
	}
}
