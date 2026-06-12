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
	bgCode  = "\x1b[48;2;31;31;31m" // #1f1f1f block behind code rows
	bgPill  = "\x1b[48;2;38;79;120m" // #264f78 behind date pills
)

// glyphs (locked)
const (
	glyphOpen      = "○"
	glyphCollapsed = "●"
	glyphMirror    = "◆"
	glyphTodo      = "□"
	glyphTodoDone  = "■"
	glyphCaret     = "▌"
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

// glyphFor returns the bullet glyph and its color for an item. Headings show
// their level digit instead of a circle: that is how h1/h2/h3 stay visible
// in a single-line wysiwyg row.
func glyphFor(it *item) (string, string) {
	if it.mirrorOf != "" {
		return glyphMirror, cRed
	}
	switch it.layout {
	case database.LayoutTodo:
		if it.completedAt > 0 {
			return glyphTodoDone, cFG
		}
		return glyphTodo, cFG
	case database.LayoutH1:
		return "1", cBold + cYellow
	case database.LayoutH2:
		return "2", cBold + cYellow
	case database.LayoutH3:
		return "3", cBold + cYellow
	}
	if len(it.children) > 0 && it.collapsed {
		return glyphCollapsed, cAccent
	}
	return glyphOpen, cAccent
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

// withCaret inserts the caret glyph into text at the given rune index.
func withCaret(text string, caret int) string {
	runes := []rune(text)
	if caret < 0 {
		caret = 0
	}
	if caret > len(runes) {
		caret = len(runes)
	}
	return string(runes[:caret]) + cAccent + glyphCaret + cFG + string(runes[caret:])
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

// renderBody renders a node name wysiwyg. Unselected rows are muted gray
// with the markdown markers hidden; the selected row turns red, shows the
// markers and carries the red caret at the given rune index (-1 for none).
func renderBody(it *item, name string, caret int, selected bool) string {
	base := cDim
	if selected {
		base = cRed
	}

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
	emitCaret := func() {
		b.WriteString(cReset + cRed + glyphCaret)
		cur = "" // force a state re-emit after the caret
	}
	if it.layout == database.LayoutCode {
		b.WriteString(cReset + attrs + " ") // pad the code block
	}
	for i, r := range runes {
		f := flags[i]
		if i == caret {
			emitCaret()
		}
		if f.marker && !selected {
			continue
		}
		if s := sgr(f); s != cur {
			b.WriteString(s)
			cur = s
		}
		b.WriteRune(r)
	}
	if caret == len(runes) {
		emitCaret()
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
