/* Copyright 2025 Lflow Authors
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package editor

import (
	"strings"
	"testing"

	"github.com/lflow/lflow/pkg/cli/database"
	"github.com/mattn/go-runewidth"
)

// stripSGR removes ANSI style sequences, leaving the visible text.
func stripSGR(s string) string {
	var b strings.Builder
	inEsc := false
	for _, r := range s {
		if inEsc {
			if r == 'm' {
				inEsc = false
			}
			continue
		}
		if r == '\x1b' {
			inEsc = true
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func TestSourceUUIDFollowsMirrorChain(t *testing.T) {
	orig := &item{uuid: "a", name: "alpha"}
	mir1 := &item{uuid: "b", mirrorOf: "a"} // mirror of the original
	mir2 := &item{uuid: "c", mirrorOf: "b"} // mirror of a mirror
	tr := &tree{byUUID: map[string]*item{"a": orig, "b": mir1, "c": mir2}}

	if got := tr.sourceUUID(orig); got != "a" {
		t.Errorf("non-mirror resolves to itself, got %q", got)
	}
	if got := tr.sourceUUID(mir1); got != "a" {
		t.Errorf("mirror resolves to original, got %q", got)
	}
	// the regression: a mirror of a mirror must resolve to the original,
	// not the intermediate mirror whose name is empty.
	if got := tr.sourceUUID(mir2); got != "a" {
		t.Errorf("mirror of mirror should resolve to original, got %q", got)
	}

	// a new mirror pointed at the resolved source shows the original name.
	newMir := &item{uuid: "d", mirrorOf: tr.sourceUUID(mir2)}
	tr.byUUID["d"] = newMir
	if got := tr.displayName(newMir); got != "alpha" {
		t.Errorf("mirror-of-mirror should display original name, got %q", got)
	}
}

func TestFinderRowNameResolvesMirror(t *testing.T) {
	nodes := map[string]database.Node{
		"a": {UUID: "a", Name: "alpha"},
		"b": {UUID: "b", MirrorOf: "a"}, // mirror of the original
		"c": {UUID: "c", MirrorOf: "b"}, // mirror of a mirror
	}
	resolve := func(uuid string) (database.Node, bool) {
		n, ok := nodes[uuid]
		return n, ok
	}

	if got := finderRowName(nodes["a"], resolve); got != "alpha" {
		t.Errorf("non-mirror keeps its name, got %q", got)
	}
	// the regression: a mirror's name is empty in the database, so the
	// finder must resolve it to the source name rather than show a blank.
	if got := finderRowName(nodes["b"], resolve); got != "alpha · mirror" {
		t.Errorf("mirror should resolve to source name, got %q", got)
	}
	if got := finderRowName(nodes["c"], resolve); got != "alpha · mirror" {
		t.Errorf("mirror of mirror should resolve to original, got %q", got)
	}
}

func TestFinderRowNameMissingSource(t *testing.T) {
	resolve := func(string) (database.Node, bool) { return database.Node{}, false }
	if got := finderRowName(database.Node{UUID: "b", MirrorOf: "gone"}, resolve); got != "(missing) · mirror" {
		t.Errorf("missing source falls back to placeholder, got %q", got)
	}
}

func TestSourceUUIDStopsOnCycle(t *testing.T) {
	a := &item{uuid: "a", mirrorOf: "b"}
	b := &item{uuid: "b", mirrorOf: "a"}
	tr := &tree{byUUID: map[string]*item{"a": a, "b": b}}
	// must terminate rather than loop forever.
	_ = tr.sourceUUID(a)
}

func TestRenderBodyHidesMarkersWhenUnselected(t *testing.T) {
	it := &item{layout: database.LayoutBullets}

	got := stripSGR(renderBody(it, "say **hello** to *world*", -1, false))
	if got != "say hello to world" {
		t.Errorf("markers should be hidden: %q", got)
	}
}

func TestRenderBodyShowsMarkersWhenSelected(t *testing.T) {
	it := &item{layout: database.LayoutBullets}

	got := stripSGR(renderBody(it, "say **hello**", -1, true))
	if got != "say **hello**" {
		t.Errorf("markers should be visible on the selected row: %q", got)
	}
}

func TestRenderBodyBlockCursor(t *testing.T) {
	it := &item{layout: database.LayoutBullets}

	// the cursor is a painted cell, never an inserted character
	rendered := renderBody(it, "abc", 1, true)
	if got := stripSGR(rendered); got != "abc" {
		t.Errorf("cursor must not insert characters: %q", got)
	}
	if !strings.Contains(rendered, cInvert+"b") {
		t.Errorf("rune under the caret should carry the cursor cell: %q", rendered)
	}

	// past the end it paints one trailing cell
	rendered = renderBody(it, "abc", 3, true)
	if got := stripSGR(rendered); got != "abc " {
		t.Errorf("caret at end should paint a trailing cell: %q", got)
	}
	if !strings.Contains(rendered, cInvert+" ") {
		t.Errorf("trailing cursor cell missing: %q", rendered)
	}
}

func TestGlyphForMutedBullets(t *testing.T) {
	_, color := glyphFor(&item{layout: database.LayoutBullets})
	if color != cDim {
		t.Errorf("plain bullets should be muted gray, got %q", color)
	}
	_, color = glyphFor(&item{mirrorOf: "x"})
	if color != cRed {
		t.Errorf("mirrors keep their red identity, got %q", color)
	}
}

func TestRenderBodyLoneAsteriskStaysPlain(t *testing.T) {
	it := &item{layout: database.LayoutBullets}

	got := stripSGR(renderBody(it, "2 * 3 yields 6x", -1, false))
	if got != "2 * 3 yields 6x" {
		t.Errorf("unpaired asterisk must not be eaten: %q", got)
	}
}

func TestRenderBodyDatePillBracketsHidden(t *testing.T) {
	it := &item{layout: database.LayoutBullets}

	rendered := renderBody(it, "ship on [[2025-02-11 15:20]] sharp", -1, false)
	got := stripSGR(rendered)
	if got != "ship on 2025-02-11 15:20 sharp" {
		t.Errorf("pill brackets should be hidden: %q", got)
	}
	if !strings.Contains(rendered, bgPill) {
		t.Errorf("pill background missing: %q", rendered)
	}
}

func TestRenderBodyCodeBlock(t *testing.T) {
	it := &item{layout: database.LayoutCode}

	rendered := renderBody(it, "rm -rf ./dist", -1, false)
	if !strings.Contains(rendered, bgCode) {
		t.Errorf("code background missing: %q", rendered)
	}
	if got := stripSGR(rendered); got != " rm -rf ./dist " {
		t.Errorf("code block should be padded: %q", got)
	}
}

func TestRenderBodyQuoteBar(t *testing.T) {
	it := &item{layout: database.LayoutQuote}

	rendered := renderBody(it, "less is more", -1, false)
	if got := stripSGR(rendered); got != glyphQuoteBar+" less is more" {
		t.Errorf("quote bar missing: %q", got)
	}
}

func TestGlyphForHeadingDigits(t *testing.T) {
	cases := []struct {
		layout string
		want   string
	}{
		{database.LayoutH1, "1"},
		{database.LayoutH2, "2"},
		{database.LayoutH3, "3"},
		{database.LayoutBullets, glyphOpen},
	}
	for _, tc := range cases {
		glyph, _ := glyphFor(&item{layout: tc.layout})
		if glyph != tc.want {
			t.Errorf("layout %s: glyph %q, want %q", tc.layout, glyph, tc.want)
		}
	}
}

func TestWrapLinePlain(t *testing.T) {
	lines := wrapLine("aaa bbb ccc ddd", 7, "")
	want := []string{"aaa bbb", "ccc ddd"}
	if len(lines) != 2 || stripSGR(lines[0]) != want[0] || stripSGR(lines[1]) != want[1] {
		t.Errorf("wrap mismatch: %q", lines)
	}
}

func TestWrapLineHangingIndent(t *testing.T) {
	lines := wrapLine("word word word word", 12, cDim+"   ")
	if len(lines) < 2 {
		t.Fatalf("expected a wrap: %q", lines)
	}
	for i, l := range lines[1:] {
		if !strings.HasPrefix(stripSGR(l), "   ") {
			t.Errorf("continuation %d missing hanging indent: %q", i+1, l)
		}
	}
	for i, l := range lines {
		if w := visibleWidth(l); w > 12 {
			t.Errorf("line %d too wide: %d %q", i, w, l)
		}
	}
}

func TestWrapLineCarriesStyle(t *testing.T) {
	styled := cBold + "one two three four" + cReset
	lines := wrapLine(styled, 9, "")
	if len(lines) < 2 {
		t.Fatalf("expected a wrap: %q", lines)
	}
	if !strings.Contains(lines[1], cBold) {
		t.Errorf("continuation should re-open bold: %q", lines[1])
	}
}

func TestWrapLineCursorDoesNotBleedAcrossWrap(t *testing.T) {
	// the block cursor (reverse-video) lands on the wrap-break space: it must
	// invert exactly that one cell at the trailing edge of the first visual
	// line and never re-open on the continuation line, where it would invert
	// the whole hanging indent.
	styled := "one two three" + cInvert + " " + cReset + "four five six"
	lines := wrapLine(styled, 14, cDim+"   ")
	if len(lines) < 2 {
		t.Fatalf("expected a wrap: %q", lines)
	}
	// exactly one inverted cell survives, on the first visual line.
	if n := strings.Count(lines[0], cInvert); n != 1 {
		t.Errorf("line 0 should carry exactly one block cursor cell, got %d: %q", n, lines[0])
	}
	for i, l := range lines[1:] {
		if strings.Contains(l, cInvert) {
			t.Errorf("continuation %d leaks the block cursor: %q", i+1, l)
		}
	}
	// every emitted segment must close its styling so nothing leaks past a
	// line end.
	if !strings.HasSuffix(lines[0], cReset) {
		t.Errorf("line 0 should end reset: %q", lines[0])
	}
}

func TestWrapLineHardBreaksUnbrokenRuns(t *testing.T) {
	lines := wrapLine(strings.Repeat("x", 25), 10, cDim+"  ")
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines: %q", lines)
	}
	for i, l := range lines {
		if w := visibleWidth(l); w > 10 {
			t.Errorf("line %d too wide: %d", i, w)
		}
	}
}

func TestWrapLineWideRunes(t *testing.T) {
	// CJK runes are two cells wide; no line may exceed the width
	lines := wrapLine(strings.Repeat("字", 12), 10, "")
	for i, l := range lines {
		if w := visibleWidth(l); w > 10 {
			t.Errorf("line %d too wide: %d %q", i, w, l)
		}
	}
}

func TestWrapLineGraphemeCluster(t *testing.T) {
	// A ZWJ family emoji is a single grapheme cluster two cells wide. Summing
	// its component rune widths (2+0+2+0+2=6) would overcount and wrap early.
	// The cluster plus trailing text fits in width 10, so it must not wrap.
	family := "\U0001F468‍\U0001F469‍\U0001F467" // 👨‍👩‍👧
	if w := runewidth.StringWidth(family); w != 2 {
		t.Fatalf("test premise: family cluster should be 2 cells, got %d", w)
	}
	line := family + " abc" // 2 + 1 + 3 = 6 cells, fits in 10
	lines := wrapLine(line, 10, "")
	if len(lines) != 1 {
		t.Fatalf("ZWJ cluster plus text fits but wrapped: %q", lines)
	}
	if stripSGR(lines[0]) != line {
		t.Errorf("cluster mangled by wrap: got %q want %q", stripSGR(lines[0]), line)
	}
}

func TestVisibleWidthGraphemeCluster(t *testing.T) {
	// visibleWidth must fold a ZWJ cluster into its true terminal width, not
	// sum its component widths.
	family := "\U0001F468‍\U0001F469‍\U0001F467"
	if w := visibleWidth(family); w != 2 {
		t.Errorf("visibleWidth of ZWJ family emoji = %d, want 2", w)
	}
}

func TestWrapLineFillsBulletLineBeforeRun(t *testing.T) {
	// a bulleted node whose body is one long no-space run must fill the
	// bullet line first, not strand "○" alone with the run on line 2
	line := " " + cDim + glyphOpen + cReset + " " + strings.Repeat("x", 80)
	lines := wrapLine(line, 40, cDim+"   ")
	if len(lines) < 2 {
		t.Fatalf("expected a wrap: %q", lines)
	}
	first := stripSGR(lines[0])
	if strings.Count(first, "x") < 30 {
		t.Errorf("bullet line should be filled with the run, got %q", first)
	}
}

func TestWrapLineExpandsTabs(t *testing.T) {
	// runewidth measures '\t' as zero, so without tab expansion wrapLine never
	// sees the width a tab adds and the terminal hard-wraps with no hang
	// indent. A tab-laden line that overflows must wrap, and the continuation
	// must carry the hanging indent.
	line := "ab\tcd\tef\tgh\tij\tkl\tmn\top"
	lines := wrapLine(line, 20, cDim+"   ")
	if len(lines) < 2 {
		t.Fatalf("expected a wrap of the tab-laden line: %q", lines)
	}
	for i, l := range lines {
		if w := visibleWidth(l); w > 20 {
			t.Errorf("line %d too wide: %d %q", i, w, l)
		}
	}
	for i, l := range lines[1:] {
		if !strings.HasPrefix(stripSGR(l), "   ") {
			t.Errorf("continuation %d missing hanging indent: %q", i+1, l)
		}
	}
	// tabs must be gone from the rendered output, expanded to spaces
	for i, l := range lines {
		if strings.ContainsRune(l, '\t') {
			t.Errorf("line %d still contains a raw tab: %q", i, l)
		}
	}
}

func TestExpandTabsToTabStops(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"a\tb", "a       b"},                // col 1 -> next stop is 8
		{"\tx", "        x"},                 // col 0 -> stop at 8
		{"ab\tcd", "ab      cd"},             // col 2 -> stop at 8
		{"12345678\tz", "12345678        z"}, // col 8 -> stop at 16
		{"no tabs", "no tabs"},
	}
	for _, tc := range cases {
		if got := expandTabs(tc.in); got != tc.want {
			t.Errorf("expandTabs(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
	// SGR sequences must not advance the tab-stop column
	if got := expandTabs(cBold + "a" + cReset + "\tb"); !strings.HasSuffix(stripSGR(got), "a       b") {
		t.Errorf("SGR should not shift tab stops: %q", stripSGR(got))
	}
}

func TestContinuationPrefixKeepsRail(t *testing.T) {
	// a depth-1 node with a later sibling and a child: its wrapped
	// continuation must carry │ in its own branch column and under the
	// glyph so the rail stays continuous down to the child.
	r := row{depth: 1, last: false, branch: []bool{true}}
	prefix := continuationPrefix(r, true)
	if got := stripSGR(prefix); got != " │  │ " {
		t.Fatalf("prefix rail mismatch: %q", got)
	}

	line := " " + cDim + connector(r) + cDim + glyphOpen + cReset + " " + strings.Repeat("word ", 12)
	lines := wrapLine(line, 20, prefix)
	if len(lines) < 2 {
		t.Fatalf("expected a wrap: %q", lines)
	}
	if !strings.Contains(lines[1], "│") {
		t.Errorf("continuation should carry the tree rail: %q", lines[1])
	}
}

func TestRenderBodyCompletedStrikethrough(t *testing.T) {
	it := &item{layout: database.LayoutTodo, completedAt: 1}

	rendered := renderBody(it, "done thing", -1, false)
	if !strings.Contains(rendered, cStrike) {
		t.Errorf("completed nodes should strike through: %q", rendered)
	}
}
