package editor

import (
	"sort"
	"strings"

	"github.com/lflow/lflow/pkg/tui/database"
)

// The Line node type: ordinary dialogue text with a character attached, shown
// as a colored "[NAME] " prefix — never baked into the stored name (the
// WARNING invariant that no markup leaks into stored text holds here too). A
// character is shared across every Line node that names it, so its identity
// and color live in two small tables (see database/linecharacter.go) rather
// than on the node row, exactly like tag colors:
//
//   - lineCharacters: node uuid -> character name, one row per Line node.
//   - characterColors: character name -> style color name, one row per
//     character, so recoloring one line's character recolors every line that
//     character speaks.
//
// Both are package vars hydrated wholesale at load and on every aux livesync
// refresh (see editor.go and livesync.go), like tagColors, so the render path
// stays Model-free.

var lineCharacters = map[string]string{}
var characterColors = map[string]string{}

// normalizeCharacter canonicalizes a typed character name: trimmed and
// uppercased, so "Alice" and "ALICE" are the same character and the stored
// form is already the screenplay-style bracket display.
func normalizeCharacter(s string) string {
	return strings.ToUpper(strings.TrimSpace(s))
}

// lineCharacterOf returns the character assigned to a Line node, or "".
func lineCharacterOf(it *item) string {
	if it == nil {
		return ""
	}
	return lineCharacters[it.uuid]
}

// existingCharacters is every distinct character in the outline, sorted —
// derived straight from lineCharacters since it already holds exactly one
// entry per Line node that has a character.
func existingCharacters() []string {
	set := map[string]bool{}
	for _, name := range lineCharacters {
		if name != "" {
			set[name] = true
		}
	}
	out := make([]string, 0, len(set))
	for name := range set {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// characterColorSGR returns the SGR foreground for a character's bracket —
// its assigned color, or dim muted gray when none was chosen.
func characterColorSGR(name string) string {
	if color, ok := characterColors[normalizeCharacter(name)]; ok {
		if code, ok := styleColorCode[color]; ok {
			return code
		}
	}
	return cDim
}

// linePrefix renders the "[NAME] " bracket before the editable dialogue text;
// an unassigned Line prompts for one.
func linePrefix(it *item) string {
	name := lineCharacterOf(it)
	if name == "" {
		return cDim + "[+ character] " + cReset
	}
	return characterColorSGR(name) + "[" + name + "]" + cReset + " "
}

// lineToContext carries the speaking character, the one thing a Line's text
// alone cannot express.
func lineToContext(it *item) contextXML {
	x := contextXML{tag: "line"}
	if name := lineCharacterOf(it); name != "" {
		x.attrs = `character="` + name + `"`
	}
	return x
}

// setLineCharacter assigns (or clears, for "") a Line node's character,
// persists it immediately — like /star or /priority, not the debounced text
// flush — and updates the in-memory map the render path reads.
func setLineCharacter(m *Model, uuid, name string) {
	if name == "" {
		delete(lineCharacters, uuid)
	} else {
		lineCharacters[uuid] = name
	}
	if m.db != nil {
		_ = database.SetLineCharacter(m.db, uuid, name)
	}
}

// openCharacterPicker opens the alt+e / onType character picker for a Line
// node: pick an existing character or type a new one.
func (m *Model) openCharacterPicker(it *item) {
	m.mode = modeCharacterPick
	m.characterPickUUID = it.uuid
	m.list.open(m, characterSource{}, true)
}
