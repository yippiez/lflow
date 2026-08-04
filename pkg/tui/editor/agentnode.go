package editor

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/lflow/lflow/pkg/tui/database"
)

// An Agent node is the full-row sibling of an agent chip. Both point at the
// same LOCAL session data in node_output. The transcript shown by alt+e is a
// virtual read-only projection: no message or tool payload is written to nodes,
// chips, or sync state.

type agentNodeView struct{}

func agentNodeHandle(m *Model, it *item) (agentHandle, bool) {
	if it == nil || it.typ != database.TypeAgent {
		return agentHandle{}, false
	}
	s := m.agentLoad(it.uuid)
	v, ok := agentVariantByID(s.Variant)
	if !ok || s.SessionID == "" {
		return agentHandle{}, false
	}
	return agentHandle{id: it.uuid, v: v, sess: s, node: true}, true
}

func (agentNodeView) Enter(m *Model, it *item) bool {
	h, ok := agentNodeHandle(m, it)
	if !ok {
		m.errorFlash("Agent · no session attached · alt+r picks one")
		return false
	}
	m.refreshAgentTrace(h)
	return true
}

func (agentNodeView) Leave(m *Model, it *item) {
	if it != nil {
		delete(m.nodeStore(it.uuid), "agentRename")
	}
}

func (agentNodeView) Lines(m *Model, it *item, width int) int {
	h, ok := agentNodeHandle(m, it)
	if !ok {
		return 0
	}
	return len(m.agentBandContent(h, "", width))
}

func (agentNodeView) Key(m *Model, it *item, k tea.KeyMsg) (tea.Cmd, bool) {
	h, ok := agentNodeHandle(m, it)
	if !ok {
		return nil, false
	}
	return m.agentViewKey(h, k)
}

func (agentNodeView) Bands(m *Model, it *item, rail string, width, scroll, winH int, focused bool) []string {
	h, ok := agentNodeHandle(m, it)
	if !ok {
		return nil
	}
	return NodeWindowBands(m.agentBandContent(h, rail, width), scroll, winH)
}

func agentNodeGlyph(it *item) (string, string) {
	color := agentLooks[it.uuid]
	if color == "" {
		color = cDim
	}
	return "◆", color
}

func runAgentNode(m *Model, it *item) tea.Cmd {
	h, ok := agentNodeHandle(m, it)
	if !ok {
		return m.openAgentPickerForNode(it.uuid)
	}
	cwd := h.sess.Cwd
	if cwd == "" {
		cwd = processCWD()
	}
	return m.agentOpen(h.v, h.id, cwd)
}

func (m *Model) bindAgentNode(uuid string, v agentVariant, attach agentStoreSession) {
	it := m.tree.byUUID[uuid]
	if it == nil {
		return
	}
	it.typ = database.TypeAgent
	m.agentAttach(uuid, attach)
	s := m.agentLoad(uuid)
	if s.Variant == "" {
		s.Variant = v.id
		m.agentSave(uuid, s)
	}
	it.name = m.agentTitle(uuid, v, m.agentLoad(uuid))
	m.publishAgentLook(uuid, v)
	m.unsaved = true
	m.focused = true
	m.focusScroll = 0
	if h, ok := agentNodeHandle(m, it); ok {
		m.refreshAgentTrace(h)
	}
	m.flash = v.label + " · alt+r opens · alt+e traces"
}

func agentNodeToContext(m *Model, it *item) contextXML {
	s := m.agentLoad(it.uuid)
	v, ok := agentVariantByID(s.Variant)
	if !ok {
		return contextXML{tag: "agent"}
	}
	return contextXML{tag: "agent", attrs: `variant="` + v.id + `"`, body: m.agentTitle(it.uuid, v, s)}
}
