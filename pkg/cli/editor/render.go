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
	"fmt"
	"strings"

	"github.com/lflow/lflow/pkg/cli/database"
	"github.com/mattn/go-runewidth"
)

// the locked palette (design v4)
const (
	cReset  = "\x1b[0m"
	cFG     = "\x1b[38;2;212;212;212m" // #d4d4d4
	cDim    = "\x1b[38;2;122;122;122m" // #7a7a7a
	cAccent = "\x1b[38;2;86;156;214m"  // #569cd6
	cRed    = "\x1b[38;2;244;71;71m"   // #f44747
	cYellow = "\x1b[38;2;220;220;170m" // #dcdcaa
	cBold   = "\x1b[1m"
	cItalic = "\x1b[3m"
	cStrike = "\x1b[9m"
	bgCode  = "\x1b[48;2;31;31;31m"  // #1f1f1f block behind code rows
	bgPill  = "\x1b[48;2;38;79;120m" // #264f78 behind date pills
	cInvert = "\x1b[7m"              // the block cursor: inverts the cell beneath it
)

// glyphs (locked)
const (
	glyphOpen      = "○"
	glyphCollapsed = "●"
	glyphMirror    = "◆"
	glyphTodo      = "□"
	glyphTodoDone  = "■"
	glyphQuoteBar  = "▎"
)

// visibleWidth returns the display width of s ignoring SGR sequences.
func visibleWidth(s string) int {
	w := 0
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
		w += runewidth.RuneWidth(r)
	}
	return w
}

// clip truncates s (which may contain SGR sequences) to the given display width.
func clip(s string, width int) string {
	if visibleWidth(s) <= width {
		return s
	}
	var b strings.Builder
	w := 0
	inEsc := false
	for _, r := range s {
		if inEsc {
			b.WriteRune(r)
			if r == 'm' {
				inEsc = false
			}
			continue
		}
		if r == '\x1b' {
			b.WriteRune(r)
			inEsc = true
			continue
		}
		rw := runewidth.RuneWidth(r)
		if w+rw > width-1 {
			b.WriteString(cDim + "…")
			break
		}
		b.WriteRune(r)
		w += rw
	}
	return b.String()
}

// tabWidth is the terminal's hard tab stop: tabs advance to the next multiple.
const tabWidth = 8

// expandTabs replaces tabs with spaces up to the next tab stop, tracking the
// display column across the (possibly SGR-styled) line so each \t advances to
// the next multiple of tabWidth columns — exactly how the terminal expands
// them. runewidth measures '\t' as zero, so wrapLine must see expanded spaces
// to measure and wrap tab-laden lines correctly. SGR sequences pass through
// untouched and do not advance the column.
func expandTabs(s string) string {
	if !strings.ContainsRune(s, '\t') {
		return s
	}
	var b strings.Builder
	col := 0
	inEsc := false
	for _, r := range s {
		if inEsc {
			b.WriteRune(r)
			if r == 'm' {
				inEsc = false
			}
			continue
		}
		switch {
		case r == '\x1b':
			b.WriteRune(r)
			inEsc = true
		case r == '\t':
			n := tabWidth - col%tabWidth
			for k := 0; k < n; k++ {
				b.WriteByte(' ')
			}
			col += n
		default:
			b.WriteRune(r)
			col += runewidth.RuneWidth(r)
		}
	}
	return b.String()
}

// wrapLine soft-wraps an SGR-styled line to the given display width, breaking
// at spaces when one is available. Continuation lines carry the given prefix —
// a dim-styled hanging indent that keeps the tree rail continuous under the
// text column — and re-open the styles active at the break, so spans and the
// cursor cell survive the wrap.
func wrapLine(s string, width int, prefix string) []string {
	s = expandTabs(s)
	if width <= 0 || visibleWidth(s) <= width {
		return []string{s}
	}
	hang := visibleWidth(prefix)
	if hang >= width/2 {
		hang = 0 // pathological widths: give the text the whole line
		prefix = ""
	}
	runes := []rune(s)

	var lines []string
	var state []string // SGR sequences active since the last reset
	lineStart := 0
	var startState []string // state at lineStart
	curWidth := 0
	avail := width
	lastSpace := -1
	var lastSpaceState []string

	emitLine := func(end int) {
		seg := string(runes[lineStart:end])
		if len(lines) == 0 {
			lines = append(lines, seg+cReset)
		} else {
			// The cursor cell is a single cell and never spans a wrap, so the
			// reverse-video sequence must never appear in a continuation
			// prefix — drop it from the carried state.
			carried := startState[:0:0]
			for _, sgr := range startState {
				if sgr == cInvert {
					continue
				}
				carried = append(carried, sgr)
			}
			lines = append(lines, prefix+cReset+strings.Join(carried, "")+seg+cReset)
		}
	}

	i := 0
	for i < len(runes) {
		r := runes[i]
		if r == '\x1b' {
			j := i
			for j < len(runes) && runes[j] != 'm' {
				j++
			}
			if seq := string(runes[i:min(j+1, len(runes))]); seq == cReset {
				state = state[:0]
			} else {
				state = append(state, seq)
			}
			i = j + 1
			continue
		}

		rw := runewidth.RuneWidth(r)
		if curWidth+rw > avail {
			if r == ' ' {
				// the overflowing rune is itself a space: break right here
				emitLine(i)
				lineStart = i + 1
				startState = append([]string(nil), state...)
				curWidth = 0
				avail = width - hang
				lastSpace = -1
				i++
				continue
			}
			if lastSpace > lineStart {
				// break at the last space; what follows it moves down
				emitLine(lastSpace)
				lineStart = lastSpace + 1
				startState = append([]string(nil), lastSpaceState...)
				curWidth = visibleWidth(string(runes[lineStart:i]))
			} else {
				// no space on this line: hard break before the rune
				emitLine(i)
				lineStart = i
				startState = append([]string(nil), state...)
				curWidth = 0
			}
			avail = width - hang
			lastSpace = -1
			continue // re-check the same rune against the new line
		}

		if r == ' ' && curWidth >= hang {
			// only spaces past the prefix/indent column are wrap candidates:
			// the glyph's trailing space must not strand the bullet on its own
			// line when the body is one long unbroken run.
			lastSpace = i
			lastSpaceState = append([]string(nil), state...)
		}
		curWidth += rw
		i++
	}
	emitLine(len(runes))
	return lines
}

// glyphFor returns the bullet glyph and its color for an item. Bullets and
// todo boxes are muted gray — the selected row turns its glyph red. Glyphs
// with an identity keep their own color: ◆ mirrors red, heading digits
// yellow. Headings show their level digit instead of a circle: that is how
// h1/h2/h3 stay visible in a single-line wysiwyg row.
func glyphFor(it *item) (string, string) {
	if it.mirrorOf != "" {
		return glyphMirror, cRed
	}
	switch it.layout {
	case database.LayoutTodo:
		if it.completedAt > 0 {
			return glyphTodoDone, cDim
		}
		return glyphTodo, cDim
	case database.LayoutH1:
		return "1", cBold + cYellow
	case database.LayoutH2:
		return "2", cBold + cYellow
	case database.LayoutH3:
		return "3", cBold + cYellow
	}
	if len(it.children) > 0 && it.collapsed {
		return glyphCollapsed, cDim
	}
	return glyphOpen, cDim
}

// connector builds the tree-connector prefix for a row: │ continuation
// columns for ancestors with later siblings, then ├─ or ╰─ dropping from the
// parent's bullet column. Depth-0 bullets sit at column 0, so the depth-0
// ancestor contributes no continuation column.
func connector(r row) string {
	if r.depth == 0 {
		return ""
	}
	var b strings.Builder
	for i, hasMore := range r.branch {
		if i == 0 {
			continue // no column exists left of depth-0 bullets
		}
		if hasMore {
			b.WriteString("│  ")
		} else {
			b.WriteString("   ")
		}
	}
	if r.last {
		b.WriteString("╰─ ")
	} else {
		b.WriteString("├─ ")
	}
	return b.String()
}

// continuationPrefix builds the dim-styled hanging indent for a row's wrapped
// continuation lines. It keeps the tree rail continuous: a │ sits in every
// ancestor column that has a later sibling, in the node's own branch column
// when the node itself has a later sibling, and under the glyph when the
// subtree continues below (the node has a visible child). Columns are laid
// out as 1 margin + 3 per depth level + 2 for the glyph and its space.
func continuationPrefix(r row, subtreeBelow bool) string {
	width := 1 + 3*r.depth + 2
	cells := make([]rune, width)
	for i := range cells {
		cells[i] = ' '
	}
	// ancestor columns: branch[i] for i in 1..depth-1 (i==0 has no column,
	// depth-0 bullets sit at column 0).
	for i := 1; i < r.depth; i++ {
		if r.branch[i] {
			cells[1+3*(i-1)] = '│'
		}
	}
	// the node's own branch column, when it has a later sibling.
	if r.depth > 0 && !r.last {
		cells[1+3*(r.depth-1)] = '│'
	}
	// the glyph column, when the subtree continues below.
	if subtreeBelow {
		cells[1+3*r.depth] = '│'
	}
	return cDim + string(cells)
}

// visualRows splits a node's plain name into the rune offsets at which each
// soft-wrapped visual line begins, mirroring wrapLine's break logic so the
// caret can be mapped to the same visual layout the renderer shows. firstCol
// is the display column the body starts at (the glyph prefix width) and hang
// is the continuation indent width. The returned slice always starts with 0;
// its length is the number of visual lines the name occupies.
func visualRows(name string, width, firstCol, hang int) []int {
	starts := []int{0}
	runes := []rune(name)
	if width <= 0 {
		return starts
	}
	// match wrapLine's clamp: pathological prefixes give the text the line.
	if hang >= width/2 {
		hang = 0
	}
	if firstCol >= width {
		firstCol = 0
	}

	lineStart := 0
	curWidth := firstCol // the body begins after the glyph prefix
	avail := width
	lastSpace := -1

	emit := func(start int) {
		starts = append(starts, start)
		lineStart = start
		curWidth = 0
		avail = width - hang
		lastSpace = -1
	}

	i := 0
	for i < len(runes) {
		r := runes[i]
		rw := runewidth.RuneWidth(r)
		if curWidth+rw > avail {
			if r == ' ' {
				emit(i + 1)
				i++
				continue
			}
			if lastSpace > lineStart {
				next := lastSpace + 1
				emit(next)
				curWidth = visibleWidth(string(runes[next:i]))
			} else {
				emit(i)
			}
			continue // re-check the same rune on the new line
		}
		if r == ' ' && curWidth >= hang {
			lastSpace = i
		}
		curWidth += rw
		i++
	}
	return starts
}

// caretVisualLine returns which visual line of the wrapped name the caret sits
// on, given the line-start offsets from visualRows.
func caretVisualLine(starts []int, caret int) int {
	line := 0
	for j, s := range starts {
		if caret >= s {
			line = j
		}
	}
	return line
}

// withCaret marks the rune at the caret index with the inverting block
// cursor; past the end it paints a single trailing cell.
func withCaret(text string, caret int) string {
	runes := []rune(text)
	if caret < 0 {
		caret = 0
	}
	if caret >= len(runes) {
		return string(runes) + cInvert + " " + cReset + cFG
	}
	return string(runes[:caret]) + cInvert + string(runes[caret]) + cReset + cFG + string(runes[caret+1:])
}

// spanFlags is the per-rune style mask the inline parser produces.
type spanFlags struct {
	marker bool // part of ** * [[ ]] syntax, hidden unless the row is selected
	bold   bool
	italic bool
	pill   bool
}

// inlineSpans marks inline markdown spans over the raw runes: **bold**,
// *italic* and [[date pills]]. Markers only toggle when a closing marker
// exists, so a lone asterisk stays plain text.
func inlineSpans(runes []rune) []spanFlags {
	flags := make([]spanFlags, len(runes))

	// date pills: [[ ... ]]
	for i := 0; i+1 < len(runes); i++ {
		if flags[i].pill || runes[i] != '[' || runes[i+1] != '[' {
			continue
		}
		for j := i + 2; j+1 < len(runes); j++ {
			if runes[j] == ']' && runes[j+1] == ']' {
				for k := i; k <= j+1; k++ {
					flags[k].pill = true
				}
				flags[i].marker, flags[i+1].marker = true, true
				flags[j].marker, flags[j+1].marker = true, true
				i = j + 1
				break
			}
		}
	}

	// bold: ** ... **
	for i := 0; i+1 < len(runes); i++ {
		if flags[i].pill || flags[i].marker || runes[i] != '*' || runes[i+1] != '*' {
			continue
		}
		for j := i + 2; j+1 < len(runes); j++ {
			if runes[j] == '*' && runes[j+1] == '*' && !flags[j].pill && j > i+2 {
				for k := i; k <= j+1; k++ {
					flags[k].bold = true
				}
				flags[i].marker, flags[i+1].marker = true, true
				flags[j].marker, flags[j+1].marker = true, true
				i = j + 1
				break
			}
		}
	}

	// italic: * ... *
	for i := 0; i < len(runes); i++ {
		if flags[i].pill || flags[i].bold || runes[i] != '*' {
			continue
		}
		for j := i + 1; j < len(runes); j++ {
			if runes[j] == '*' && !flags[j].pill && !flags[j].bold && j > i+1 {
				for k := i; k <= j; k++ {
					flags[k].italic = true
				}
				flags[i].marker, flags[j].marker = true, true
				i = j
				break
			}
		}
	}

	return flags
}

// renderBody renders a node name wysiwyg. Text keeps its normal color on
// every row — selection is carried by the red glyph alone. Unselected rows
// hide the markdown markers; the selected row shows them and the block
// cursor inverts the cell under the rune at the caret index (-1 for none).
func renderBody(it *item, name string, caret int, selected bool) string {
	base := cFG

	attrs := ""
	prefix := ""
	switch it.layout {
	case database.LayoutH1, database.LayoutH2, database.LayoutH3:
		attrs += cBold
	case database.LayoutQuote:
		attrs += cItalic
		prefix = cAccent + glyphQuoteBar + cReset + " "
	case database.LayoutCode:
		attrs += bgCode
	}
	if it.completedAt > 0 {
		attrs += cStrike
	}

	runes := []rune(name)
	flags := inlineSpans(runes)

	sgr := func(f spanFlags) string {
		s := cReset + base + attrs
		if f.marker {
			s = cReset + cDim + attrs
		}
		if f.pill && !f.marker {
			s += bgPill + cFG
		}
		if f.bold {
			s += cBold
		}
		if f.italic {
			s += cItalic
		}
		return s
	}

	var b strings.Builder
	b.WriteString(prefix)
	cur := ""
	if it.layout == database.LayoutCode {
		b.WriteString(cReset + attrs + " ") // pad the code block
	}
	for i, r := range runes {
		f := flags[i]
		if f.marker && !selected {
			continue
		}
		if i == caret {
			// the block cursor sits ON the rune: same colors, dark red cell
			b.WriteString(sgr(f) + cInvert)
			b.WriteRune(r)
			cur = "" // force a state re-emit after the caret cell
			continue
		}
		if s := sgr(f); s != cur {
			b.WriteString(s)
			cur = s
		}
		b.WriteRune(r)
	}
	if caret >= len(runes) && caret >= 0 {
		// past the last rune: paint one trailing cell
		b.WriteString(cReset + cFG + cInvert + " ")
	}
	if it.layout == database.LayoutCode {
		b.WriteString(cReset + attrs + " ")
	}
	b.WriteString(cReset)
	return b.String()
}

// layoutSuffix returns a dim suffix describing non-default state.
func (m *Model) layoutSuffix(it *item) string {
	var parts []string
	if it.mirrorOf != "" {
		parts = append(parts, "mirror")
	}
	if len(it.children) > 0 && it.collapsed {
		noun := "children"
		if len(it.children) == 1 {
			noun = "child"
		}
		parts = append(parts, fmt.Sprintf("%d %s", len(it.children), noun))
	}
	if it.note != "" {
		parts = append(parts, "note")
	}
	if len(parts) == 0 {
		return ""
	}
	return cDim + " · " + strings.Join(parts, " · ") + cReset
}
