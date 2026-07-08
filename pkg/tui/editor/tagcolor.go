package editor

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lflow/lflow/pkg/tui/database"
)

// Manual per-tag colors: alt+e with the caret on a tag opens a color picker;
// the choice applies to that tag everywhere it appears, as a colored pill.
// Default is no color — the familiar muted gray. Assignments live in the
// tag_colors table and, like linkColorMode, hydrate into a package var so the
// hot render path stays Model-free.

// tagColors is tag word (lowercase, no '#') → style color name.
var tagColors = map[string]string{}

// tagColorSGR returns the SGR prefix for a colored tag pill — the tag's color
// as the background with near-black text — or "" for an unassigned tag. Built
// from styleColorCode at call time so /theme switches carry over.
func tagColorSGR(word string) string {
	name, ok := tagColors[strings.ToLower(strings.TrimPrefix(word, "#"))]
	if !ok {
		return ""
	}
	fg, ok := styleColorCode[name]
	if !ok {
		return ""
	}
	// turn the foreground SGR into the same color as background
	bg := strings.Replace(fg, "[38;", "[48;", 1)
	return bg + "\x1b[38;2;16;16;16m"
}

// tagWordAtCaret returns the tag under the caret: a tag chip whose anchor
// touches the caret, or a plain-text "#word" span containing it.
func (m *Model) tagWordAtCaret(cur *item) (string, bool) {
	if cur == nil {
		return "", false
	}
	runes := []rune(cur.name)
	spans := anchorSpans(runes)
	for _, sp := range []*anchorSpan{spanStartingAt(spans, m.caret), spanEndingAt(spans, m.caret)} {
		if sp == nil {
			continue
		}
		if c, ok := m.chips[sp.id]; ok && c.Kind == chipKindTag {
			return strings.ToLower(c.Value), true
		}
	}
	for _, sp := range detectTagSpans(cur.name) {
		if m.caret >= sp[0] && m.caret <= sp[1] {
			word := strings.TrimPrefix(string(runes[sp[0]:sp[1]]), "#")
			return strings.ToLower(word), true
		}
	}
	return "", false
}

// openTagColor enters the tag color picker for a tag word.
func (m *Model) openTagColor(word string) {
	m.mode = modeTagColor
	m.tagColorWord = word
	m.list = listPicker{}
	m.list.sel = tagColorSource{}.initialSel(m)
}

// --- the picker source (shares the Group-A listPicker) -----------------------

type tagColorSource struct{}

// tagColorOptions is the picker order: none first (the default), then the
// shared style palette.
func tagColorOptions() []string {
	return append([]string{"none"}, styleColorOrder...)
}

func (tagColorSource) items(m *Model, q string) []pickerItem {
	var out []pickerItem
	for _, name := range tagColorOptions() {
		name := name
		if q != "" && !strings.Contains(name, strings.ToLower(q)) {
			continue
		}
		out = append(out, pickerItem{value: name, render: func(bool) string {
			if name == "none" {
				return cDim + "none · muted gray (default)" + cReset
			}
			pill := strings.Replace(styleColorCode[name], "[38;", "[48;", 1) + "\x1b[38;2;16;16;16m"
			return pill + " #" + m.tagColorWord + " " + cReset + " " + styleColorCode[name] + name + cReset
		}})
	}
	return out
}

func (tagColorSource) header(m *Model, p *listPicker) string {
	return " " + cDim + "color for " + cReset + cFG + "#" + m.tagColorWord + cReset
}

func (tagColorSource) initialSel(m *Model) int {
	cur, ok := tagColors[m.tagColorWord]
	if !ok {
		return 0
	}
	for i, name := range tagColorOptions() {
		if name == cur {
			return i
		}
	}
	return 0
}

func (tagColorSource) onSelect(m *Model, it pickerItem) (tea.Model, tea.Cmd) {
	if it.value != "" {
		color := it.value
		if color == "none" {
			color = ""
		}
		if color == "" {
			delete(tagColors, m.tagColorWord)
		} else {
			tagColors[m.tagColorWord] = color
		}
		if m.db != nil {
			_ = database.SetTagColor(m.db, m.tagColorWord, color)
		}
		m.flash = "#" + m.tagColorWord + " · " + it.value
	}
	m.mode = modeOutline
	return m, nil
}
