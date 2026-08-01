package editor

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lflow/lflow/pkg/tui/database"
)

// The Line node's character picker (a Group-A list, see picker_list.go):
// alt+e (or landing on a fresh Line via /type) lists every character already
// used in the outline, swatched in its assigned color, fuzzy-filtered as you
// type; typing a name nothing matches offers to create it. alt+c while
// browsing opens the nested recolor picker (characterColorSource) for the
// highlighted character.

// containsFold reports whether names contains q, case-insensitively.
func containsFold(names []string, q string) bool {
	for _, n := range names {
		if strings.EqualFold(n, q) {
			return true
		}
	}
	return false
}

// characterSwatch is the small color dot shown before a character's name.
func characterSwatch(name string) string {
	color, ok := characterColors[normalizeCharacter(name)]
	if !ok || color == "" {
		return cDim + "●" + cReset
	}
	return styleColorCode[color] + "●" + cReset
}

type characterSource struct{}

func (characterSource) items(m *Model, q string) []pickerItem {
	q = strings.TrimSpace(q)
	names := existingCharacters()
	ql := strings.ToLower(q)
	var out []pickerItem
	for _, name := range names {
		name := name
		if ql != "" && !fuzzyMatch(strings.ToLower(name), ql) {
			continue
		}
		out = append(out, pickerItem{value: name, render: func(bool) string {
			return characterSwatch(name) + " " + cFG + name + cReset
		}})
	}
	if q != "" && !containsFold(names, q) {
		newName := normalizeCharacter(q)
		out = append(out, pickerItem{value: newName, render: func(bool) string {
			return cDim + "+ create " + cReset + cFG + newName + cReset + cDim + " (new)" + cReset
		}})
	}
	return out
}

func (characterSource) header(m *Model, p *listPicker) string {
	query := p.query
	if query == "" {
		query = cDim + "type to search or write a new character" + cReset
	} else {
		query = cFG + query + cReset
	}
	return " " + cDim + "character: " + cReset + query
}

func (characterSource) initialSel(m *Model) int {
	cur := lineCharacters[m.characterPickUUID]
	if cur == "" {
		return 0
	}
	for i, name := range existingCharacters() {
		if name == cur {
			return i
		}
	}
	return 0
}

func (characterSource) onSelect(m *Model, it pickerItem) (tea.Model, tea.Cmd) {
	m.mode = modeOutline
	if it.value == "" {
		return m, nil
	}
	name := normalizeCharacter(it.value)
	setLineCharacter(m, m.characterPickUUID, name)
	m.flash = "character · " + name
	return m, nil
}

// onKey claims alt+c to recolor the highlighted character without leaving the
// picker for the top-level outline first.
func (characterSource) onKey(m *Model, p *listPicker, key string, items []pickerItem) bool {
	if key != "alt+c" || p.sel < 0 || p.sel >= len(items) || items[p.sel].value == "" {
		return false
	}
	m.characterColorName = normalizeCharacter(items[p.sel].value)
	m.mode = modeCharacterColor
	m.list.open(m, characterColorSource{}, false)
	return true
}

// --- the nested recolor picker (shares the Group-A listPicker) -------------

type characterColorSource struct{}

// characterColorOptions is the picker order: none first (the default), then
// the shared style palette — the same 8 colors /style and tag colors use.
func characterColorOptions() []string {
	return append([]string{"none"}, styleColorOrder...)
}

func (characterColorSource) items(m *Model, q string) []pickerItem {
	var out []pickerItem
	for _, name := range characterColorOptions() {
		name := name
		if q != "" && !strings.Contains(name, strings.ToLower(q)) {
			continue
		}
		out = append(out, pickerItem{value: name, render: func(bool) string {
			if name == "none" {
				return cDim + "none · muted gray (default)" + cReset
			}
			pill := strings.Replace(styleColorCode[name], "[38;", "[48;", 1) + "\x1b[38;2;16;16;16m"
			return pill + " [" + m.characterColorName + "] " + cReset + " " + styleColorCode[name] + name + cReset
		}})
	}
	return out
}

func (characterColorSource) header(m *Model, p *listPicker) string {
	return " " + cDim + "color for " + cReset + cFG + "[" + m.characterColorName + "]" + cReset
}

func (characterColorSource) initialSel(m *Model) int {
	cur, ok := characterColors[m.characterColorName]
	if !ok {
		return 0
	}
	for i, name := range characterColorOptions() {
		if name == cur {
			return i
		}
	}
	return 0
}

func (characterColorSource) onSelect(m *Model, it pickerItem) (tea.Model, tea.Cmd) {
	if it.value != "" {
		color := it.value
		if color == "none" {
			color = ""
		}
		if color == "" {
			delete(characterColors, m.characterColorName)
		} else {
			characterColors[m.characterColorName] = color
		}
		if m.db != nil {
			_ = database.SetCharacterColor(m.db, m.characterColorName, color)
		}
		m.flash = "[" + m.characterColorName + "] · " + it.value
	}
	m.mode = modeOutline
	return m, nil
}
