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
	lines := wrapLine("aaa bbb ccc ddd", 7, 0)
	want := []string{"aaa bbb", "ccc ddd"}
	if len(lines) != 2 || stripSGR(lines[0]) != want[0] || stripSGR(lines[1]) != want[1] {
		t.Errorf("wrap mismatch: %q", lines)
	}
}

func TestWrapLineHangingIndent(t *testing.T) {
	lines := wrapLine("word word word word", 12, 3)
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
	lines := wrapLine(styled, 9, 0)
	if len(lines) < 2 {
		t.Fatalf("expected a wrap: %q", lines)
	}
	if !strings.Contains(lines[1], cBold) {
		t.Errorf("continuation should re-open bold: %q", lines[1])
	}
}

func TestWrapLineHardBreaksUnbrokenRuns(t *testing.T) {
	lines := wrapLine(strings.Repeat("x", 25), 10, 2)
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
	lines := wrapLine(strings.Repeat("字", 12), 10, 0)
	for i, l := range lines {
		if w := visibleWidth(l); w > 10 {
			t.Errorf("line %d too wide: %d %q", i, w, l)
		}
	}
}

func TestWrapLineFillsBulletLineBeforeRun(t *testing.T) {
	// a bulleted node whose body is one long no-space run must fill the
	// bullet line first, not strand "○" alone with the run on line 2
	line := " " + cDim + glyphOpen + cReset + " " + strings.Repeat("x", 80)
	lines := wrapLine(line, 40, 3)
	if len(lines) < 2 {
		t.Fatalf("expected a wrap: %q", lines)
	}
	first := stripSGR(lines[0])
	if strings.Count(first, "x") < 30 {
		t.Errorf("bullet line should be filled with the run, got %q", first)
	}
}

func TestRenderBodyCompletedStrikethrough(t *testing.T) {
	it := &item{layout: database.LayoutTodo, completedAt: 1}

	rendered := renderBody(it, "done thing", -1, false)
	if !strings.Contains(rendered, cStrike) {
		t.Errorf("completed nodes should strike through: %q", rendered)
	}
}
