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
)

// glyphs (locked)
const (
	glyphOpen      = "○"
	glyphCollapsed = "●"
	glyphMirror    = "◆"
	glyphTodo      = "□"
	glyphTodoDone  = "■"
	glyphCursor    = "▌"
	glyphCaret     = "▌"
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

// glyphFor returns the bullet glyph and its color for an item.
func glyphFor(it *item) (string, string) {
	if it.mirrorOf != "" {
		return glyphMirror, cRed
	}
	if it.layout == database.LayoutTodo {
		if it.completedAt > 0 {
			return glyphTodoDone, cFG
		}
		return glyphTodo, cFG
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

// styledName styles a name according to the layout.
func styledName(it *item, name string) string {
	switch it.layout {
	case database.LayoutH1, database.LayoutH2, database.LayoutH3:
		return cBold + cYellow + name + cReset
	case database.LayoutCode:
		return cAccent + name + cReset
	case database.LayoutQuote:
		return cDim + "“" + name + "”" + cReset
	default:
		return cFG + name + cReset
	}
}

// layoutSuffix returns a dim suffix describing non-default state.
func (m *Model) layoutSuffix(it *item) string {
	var parts []string
	if it.mirrorOf != "" {
		parts = append(parts, "mirror")
	}
	if len(it.children) > 0 && it.collapsed {
		parts = append(parts, fmt.Sprintf("%d children", len(it.children)))
	}
	if it.note != "" {
		parts = append(parts, "note")
	}
	if len(parts) == 0 {
		return ""
	}
	return cDim + " (" + strings.Join(parts, " · ") + ")" + cReset
}
