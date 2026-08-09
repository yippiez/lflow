package editor

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lflow/lflow/tui/database"
)

// ⌥e on a session chip opens the SESSION EDITOR — name and color together with
// a live pill preview, rendered as a band beneath the chip's own row (the same
// focusChip/focused surface a bash chip's run output uses), never a separate
// full-screen page. ⌥c still jumps straight to the color picker.
//
// Neither touches the row's text: a chip's name and color are its own, stored in
// LOCAL node_output beside the session id and never written back into the CLI's
// store. ⌥o copies the session's open command; that is the whole vocabulary.

// openAgentEdit focuses the editor for the chip at hand.
func (m *Model) openAgentEdit(c database.Chip) {
	if _, ok := agentVariantByID(c.Value); !ok {
		m.errorFlash("unknown agent: " + c.Value)
		return
	}
	s := m.agentLoad(c.ID)
	m.focusChip = c.ID
	m.focused = true
	m.agentEditID = c.ID
	// a session still wearing its imported name opens an EMPTY field: typing
	// replaces rather than appends, and empty already means "the imported title"
	m.agentEditName = s.Name
	m.agentEditCaret = len([]rune(s.Name))
	m.agentEditField = 0
	m.agentEditColor = 0
	for i, opt := range agentColorOptions() {
		if opt == s.Color {
			m.agentEditColor = i
		}
	}
}

// saveAgentEdit writes the page back to the chip's local session record.
func (m *Model) saveAgentEdit() {
	s := m.agentLoad(m.agentEditID)
	s.Name = strings.TrimSpace(m.agentEditName)
	opts := agentColorOptions()
	s.Color = ""
	if m.agentEditColor > 0 && m.agentEditColor < len(opts) {
		s.Color = opts[m.agentEditColor]
	}
	m.agentSave(m.agentEditID, s)
	m.refreshAgentChip(m.agentEditID)
	if v, ok := agentVariantByID(s.Variant); ok {
		m.publishAgentLook(m.agentEditID, v)
	}
	m.flash = "session updated"
}

// handleAgentEditKey: the name row edits like every text field; the color row
// cycles swatches with ←/→; tab and ↑/↓ switch rows; enter saves both.
func (m *Model) handleAgentEditKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "esc":
		m.focusChip = ""
		m.focused = false
		return m, nil
	case "enter":
		m.saveAgentEdit()
		m.focusChip = ""
		m.focused = false
		m.refreshRows()
		return m, nil
	case "tab", "shift+tab", "up", "down":
		m.agentEditField = 1 - m.agentEditField
		return m, nil
	}
	if m.agentEditField == 1 {
		n := len(agentColorOptions())
		switch k.String() {
		case "left":
			m.agentEditColor = (m.agentEditColor + n - 1) % n
		case "right":
			m.agentEditColor = (m.agentEditColor + 1) % n
		}
		return m, nil
	}
	f := textField{value: m.agentEditName, caret: m.agentEditCaret}
	if f.handleKey(k) {
		m.agentEditName = f.value
		m.agentEditCaret = f.caret
	}
	return m, nil
}

// agentEditView is the alt+e session editor's inline view: a live pill
// preview, name field and color swatches render as a band beneath the chip's
// own row — the same surface a bash chip's run output (bashChipView) uses,
// never a separate full-screen page. Stateless like every nodeView; the
// working copy lives in m.agentEdit*, focused via m.focusChip exactly like
// bashChipView and linkEditView.
type agentEditView struct{}

func (agentEditView) enter(m *Model, it *item) bool { return m.focusChip != "" }

// leave only drops focus — esc is a cancel, not a save (enter is the only save
// path; see handleAgentEditKey).
func (agentEditView) leave(m *Model, it *item) { m.focusChip = "" }

func (agentEditView) lines(m *Model, it *item, width int) int { return 5 }

// Key delegates to the field-editing vocabulary shared with every alt+e
// editor; it always swallows (the editor owns every key while it is up, same
// as before this became a band).
func (agentEditView) key(m *Model, it *item, k tea.KeyMsg) (tea.Cmd, bool) {
	_, cmd := m.handleAgentEditKey(k)
	return cmd, true
}

// Bands renders the preview pill, name field and swatch row, self-windowed to
// [scroll, scroll+winH) like every other band even though its 5 lines never
// scroll in practice.
func (agentEditView) bands(m *Model, it *item, rail string, width, scroll, winH int, focused bool) []string {
	pad := rail + cReset + "  "
	c, ok := m.chips[m.agentEditID]
	if !ok {
		m.focusChip = ""
		return []string{clip(pad+cDim+"session gone"+cReset, width)}
	}
	v, _ := agentVariantByID(c.Value)
	s := m.agentLoad(m.agentEditID)

	opts := agentColorOptions()
	col := v.colorSGR()
	if m.agentEditColor > 0 && m.agentEditColor < len(opts) {
		if sgr := agentColorSGR(opts[m.agentEditColor]); sgr != "" {
			col = sgr
		}
	}
	name := strings.TrimSpace(m.agentEditName)
	if name == "" {
		name = m.agentTitle(m.agentEditID, v, agentSession{Title: s.Title, SessionID: s.SessionID})
	}
	pill := bgOf(col) + contrastInk(col) + " " + v.glyph + " " + name + " " + cReset

	nameLbl, colorLbl := cDim, cDim
	field := m.agentEditName
	if m.agentEditField == 0 {
		field = withCaret(field, m.agentEditCaret)
		nameLbl = cAccent
	} else {
		colorLbl = cAccent
	}

	var sw strings.Builder
	for i, opt := range opts {
		dot := agentColorSGR(opt)
		if i == 0 {
			dot = v.colorSGR() // "default" wears the agent's own color
		}
		glyph := "·"
		if i == m.agentEditColor {
			glyph = "●"
		}
		sw.WriteString(dot + glyph + cReset + " ")
	}

	content := []string{
		clip(pad+cDim+"edit session"+cReset, width),
		clip(pad+pill, width),
		clip(pad+nameLbl+"name   "+cReset+cFG+field+cReset, width),
		clip(pad+colorLbl+"color  "+cReset+sw.String(), width),
		clip(pad+cDim+"tab switch · ←→ color · enter save · esc cancel"+cReset, width),
	}
	if scroll > len(content) {
		scroll = len(content)
	}
	end := scroll + winH
	if end > len(content) {
		end = len(content)
	}
	return content[scroll:end]
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
