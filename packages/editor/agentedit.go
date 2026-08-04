package editor

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lflow/lflow/packages/database"
)

// The two edits you can make to a session chip or Agent node:
//
//	⌥n  rename it   — the panel's own name field, opened without the panel
//	⌥c  recolor it  — the shared swatch picker, like a tag chip's
//
// Neither touches the row's text: a chip's name and color are its own, stored in
// LOCAL node_output beside the session id and never written back into the CLI's
// store. ⌥r opens the session; those three are the whole vocabulary.

// openAgentRename focuses the chip and opens its name field straight away, so
// ⌥n is one keystroke rather than ⌥e then n.
func (m *Model) openAgentRename(c database.Chip) {
	m.focusAgentChip(c)
	s := m.agentLoad(c.ID)
	// a session still wearing its imported name opens an EMPTY field: typing
	// replaces rather than appends, and empty already means "the imported title"
	f := &textField{value: s.Name}
	f.caret = len([]rune(f.value))
	m.nodeStore(c.ID)["agentRename"] = f
}

// --- ⌥c: the session's color -----------------------------------------------

// openAgentColor opens the swatch picker for the chip under the caret.
func (m *Model) openAgentColor(c database.Chip) {
	if c.Kind == chipKindAgent {
		m.openAgentColorID(c.ID)
	}
}

// openAgentColorID serves both a chip id and an Agent node uuid; both store the
// same local agentSession record in node_output.
func (m *Model) openAgentColorID(id string) {
	if _, ok := agentVariantByID(m.agentLoad(id).Variant); !ok {
		return
	}
	m.agentColorChip = id
	m.mode = modeAgentColor
	m.list.open(m, agentColorSource{}, true)
	m.list.sel = agentColorSource{}.initialSel(m)
}

type agentColorSource struct{}

// agentColorOptions is the picker order: the agent's own default first, then the
// shared style palette — the same list a tag chip offers.
func agentColorOptions() []string {
	return append([]string{"default"}, styleColorOrder...)
}

// items are the same swatch rows /style draws — a dot in the color and its
// name — so picking a color reads the same everywhere in the editor.
func (agentColorSource) items(m *Model, q string) []pickerItem {
	v, _ := agentVariantByID(m.agentLoad(m.agentColorChip).Variant)
	var out []pickerItem
	for _, name := range agentColorOptions() {
		name := name
		if q != "" && !strings.Contains(name, strings.ToLower(q)) {
			continue
		}
		out = append(out, pickerItem{value: name, render: func(bool) string {
			if name == "default" {
				return v.colorSGR() + "●" + cReset + cDim + " default · " + v.label + "'s own" + cReset
			}
			swatch := styleColorCode[name] + "●" + cReset
			return swatch + " " + styleColorCode[name] + name + cReset
		}})
	}
	return out
}

func (agentColorSource) header(m *Model, p *listPicker) string {
	if p.query != "" {
		return " " + cDim + "color: " + cReset + cFG + p.query + cReset
	}
	return " " + cDim + "color for this session · enter picks" + cReset
}

func (agentColorSource) initialSel(m *Model) int {
	cur := m.agentLoad(m.agentColorChip).Color
	if cur == "" {
		return 0
	}
	for i, name := range agentColorOptions() {
		if name == cur {
			return i
		}
	}
	return 0
}

func (agentColorSource) onSelect(m *Model, it pickerItem) (tea.Model, tea.Cmd) {
	m.mode = modeOutline
	id := m.agentColorChip
	m.agentColorChip = ""
	if it.value == "" || id == "" {
		return m, nil
	}
	color := it.value
	if color == "default" {
		color = "" // hand the color back to the agent
	}
	m.agentSetColor(id, color)
	return m, nil
}

// agentSetColor records YOUR color for a chip ("" = back to the variant's own)
// and republishes its look. It stays here: the chip's color is lflow's record of
// the session, never written into the CLI's store.
func (m *Model) agentSetColor(id, color string) {
	s := m.agentLoad(id)
	s.Color = color
	m.agentSave(id, s)
	v, ok := agentVariantByID(s.Variant)
	if ok {
		m.publishAgentLook(id, v)
	}
	m.refreshAgentChip(id)
	if color == "" {
		m.flash = "color · the agent's own"
		return
	}
	if !ok {
		m.flash = "color · " + color
		return
	}
	m.flash = color + " · recolored here"
}
