package editor

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lflow/lflow/pkg/tui/database"
)

// alt+e on a session: the conversation itself, as bands beneath the node — the
// prompts, the assistant's replies and, line by line, the TOOL CALLS the agent
// made ("→ Bash  go test ./…"). It is read straight out of the CLI's own store
// (agentstore.go) and is strictly read-only: lflow shows a session, it never
// edits one.
//
// The same view serves the session node and the session chip; only where the
// handle's id comes from differs (the node's uuid, or the chip id in
// m.focusChip), so agentViewFor resolves the pair once and both entry points
// share every line below.

// agentView is the node's inline expanded view (registry `view` hook).
type agentView struct{}

// agentChipView is the chip's flavor, reached through m.activeView like the cmd
// chip's output band.
type agentChipView struct{}

// agentHandle is one session handle being viewed: its storage id, its variant
// and its data.
type agentHandle struct {
	id   string
	v    agentVariant
	sess agentSession
}

// agentViewFor resolves the node handle (uuid + type variant).
func agentViewFor(m *Model, it *item) (agentHandle, bool) {
	if it == nil {
		return agentHandle{}, false
	}
	v, ok := agentVariantOf(it.typ)
	if !ok {
		return agentHandle{}, false
	}
	return agentHandle{id: it.uuid, v: v, sess: m.agentLoad(it.uuid)}, true
}

// agentChipHandle resolves the focused chip's handle.
func agentChipHandle(m *Model) (agentHandle, bool) {
	c, ok := m.chips[m.focusChip]
	if !ok || c.Kind != chipKindAgent {
		return agentHandle{}, false
	}
	v, ok := agentVariantByID(c.Value)
	if !ok {
		return agentHandle{}, false
	}
	return agentHandle{id: c.ID, v: v, sess: m.agentLoad(c.ID)}, true
}

// focusAgentChip focuses a session chip's transcript band (alt+e on the chip).
func (m *Model) focusAgentChip(c database.Chip) {
	m.focusChip = c.ID
	m.focused = true
	m.focusScroll = 0
	if v, ok := agentVariantByID(c.Value); ok {
		m.loadAgentEntries(c.ID, v, m.agentLoad(c.ID))
	}
}

// loadAgentEntries reads (and caches) a session's transcript. The cache is
// keyed in the node store and dropped whenever the session is opened again, so
// scrolling a long conversation never re-reads it per frame.
func (m *Model) loadAgentEntries(id string, v agentVariant, s agentSession) []agentEntry {
	store := m.nodeStore(id)
	if e, ok := store["agentEntries"].([]agentEntry); ok {
		if got, _ := store["agentEntriesID"].(string); got == s.SessionID {
			return e
		}
	}
	entries, path := agentTranscript(v.transcriptDirs(), v.exts, s.SessionID)
	store["agentEntries"] = entries
	store["agentEntriesID"] = s.SessionID
	store["agentEntriesPath"] = path
	return entries
}

// dropAgentEntries forces the next read to hit the store again (after a run, or
// on the view's "r" key).
func (m *Model) dropAgentEntries(id string) {
	store := m.nodeStore(id)
	delete(store, "agentEntries")
	delete(store, "agentEntriesID")
	delete(store, "agentMeta")
}

// ── the node view ──────────────────────────────────────────────────────────

func (agentView) Enter(m *Model, it *item) bool {
	h, ok := agentViewFor(m, it)
	if !ok {
		return false
	}
	m.focusScroll = 0
	m.loadAgentEntries(h.id, h.v, h.sess)
	return true // focus even with no transcript: the header explains how to start one
}

func (agentView) Leave(m *Model, it *item) {}

func (agentView) Lines(m *Model, it *item, width int) int {
	h, ok := agentViewFor(m, it)
	if !ok {
		return 0
	}
	return len(m.agentBandContent(h, "", width))
}

func (agentView) Key(m *Model, it *item, k tea.KeyMsg) (tea.Cmd, bool) {
	h, ok := agentViewFor(m, it)
	if !ok {
		return nil, false
	}
	return m.agentViewKey(h, k)
}

func (agentView) Bands(m *Model, it *item, rail string, width, scroll, winH int, focused bool) []string {
	h, ok := agentViewFor(m, it)
	if !ok {
		return nil
	}
	return NodeWindowBands(m.agentBandContent(h, rail, width), scroll, winH)
}

// ── the chip view ──────────────────────────────────────────────────────────

func (agentChipView) Enter(m *Model, it *item) bool { return m.focusChip != "" }

func (agentChipView) Leave(m *Model, it *item) { m.focusChip = "" }

func (agentChipView) Lines(m *Model, it *item, width int) int {
	h, ok := agentChipHandle(m)
	if !ok {
		return 0
	}
	return len(m.agentBandContent(h, "", width))
}

func (agentChipView) Key(m *Model, it *item, k tea.KeyMsg) (tea.Cmd, bool) {
	h, ok := agentChipHandle(m)
	if !ok {
		return nil, false
	}
	return m.agentViewKey(h, k)
}

func (agentChipView) Bands(m *Model, it *item, rail string, width, scroll, winH int, focused bool) []string {
	h, ok := agentChipHandle(m)
	if !ok {
		return nil
	}
	return NodeWindowBands(m.agentBandContent(h, rail, width), scroll, winH)
}

// ── shared keys and lines ──────────────────────────────────────────────────

// agentViewKey scrolls the transcript, reopens the session (alt+r), rereads it
// (r) and opens a hosted session in the browser (alt+o). esc falls through to the
// central defocus.
func (m *Model) agentViewKey(h agentHandle, k tea.KeyMsg) (tea.Cmd, bool) {
	switch k.String() {
	case "alt+r":
		m.dropAgentEntries(h.id)
		cwd := h.sess.Cwd
		if cwd == "" {
			cwd = processCWD()
		}
		return m.agentOpen(h.v, h.id, cwd), true
	case "alt+o":
		if url := h.sess.Remote; url != "" {
			return m.agentOpenURL(h.id, url), true
		}
		if h.v.webURL != nil && h.sess.SessionID != "" {
			return m.agentOpenURL(h.id, h.v.webURL(h.sess.SessionID)), true
		}
		m.flash = "local session · no browser view"
		return nil, true
	case "r":
		m.dropAgentEntries(h.id)
		m.flash = "transcript reloaded"
		return nil, true
	case "down", "j", "pgdown":
		step := 1
		if k.String() == "pgdown" {
			step = 10
		}
		m.focusScroll += step
		return nil, true
	case "up", "k", "pgup":
		step := 1
		if k.String() == "pgup" {
			step = 10
		}
		m.focusScroll -= step
		if m.focusScroll < 0 {
			m.focusScroll = 0
		}
		return nil, true
	case "home", "g":
		m.focusScroll = 0
		return nil, true
	case "end", "G":
		m.focusScroll = 1 << 30 // the central clamp pins it to the last page
		return nil, true
	}
	return nil, false
}

// agentBandContent renders the whole view: two header lines, then one line per
// transcript entry. Callers window it; rail is the tree-rail prefix.
func (m *Model) agentBandContent(h agentHandle, rail string, width int) []string {
	if width <= 0 {
		width = 1 << 20
	}
	color := m.agentColorFor(h.id, h.v, h.sess)
	line := func(s string) string { return clip(rail+cReset+s, width) }

	var head []string
	switch {
	case h.sess.Remote != "":
		head = append(head, "remote session · "+h.sess.Remote)
	case h.sess.SessionID != "":
		head = append(head, "session "+agentShortID(h.sess.SessionID))
	default:
		head = append(head, "no session yet")
	}
	if t := m.agentTitle(h.id, h.v, h.sess); t != "" && h.sess.SessionID != "" {
		head = append(head, t)
	}
	if h.sess.Cwd != "" {
		head = append(head, tildePath(h.sess.Cwd))
	}
	if h.sess.OpenedAt > 0 {
		head = append(head, "opened "+relTime(h.sess.OpenedAt))
	}
	content := []string{
		line("  " + color + h.v.glyph + " " + h.v.label + cReset + cDim + " · " + strings.Join(head, " · ") + cReset),
	}

	keys := "  ⌥r open · r reload · ↑↓ scroll · esc close"
	if h.v.webURL != nil || h.sess.Remote != "" {
		keys = "  ⌥r open · ⌥o browser · r reload · ↑↓ scroll · esc close"
	}
	content = append(content, line(cDim+keys+cReset))

	entries := m.loadAgentEntries(h.id, h.v, h.sess)
	if len(entries) == 0 {
		hint := "  no transcript yet · ⌥r opens the session"
		if h.sess.SessionID != "" {
			hint = "  no transcript found for this session · ⌥r opens it"
		}
		if h.sess.Remote != "" {
			hint = "  hosted session · ⌥o opens it in the browser"
		}
		return append(content, line(cDim+hint+cReset))
	}
	for _, e := range entries {
		content = append(content, line("  "+agentEntryLine(e, color)))
	}
	return content
}

// agentEntryLine renders one transcript entry: a dim clock, a colored speaker or
// tool arrow, then the text. Tool calls are what a session is skimmed for, so
// they carry the tool's name in the accent color and the call's own detail after
// it.
func agentEntryLine(e agentEntry, color string) string {
	stamp := "     "
	if !e.at.IsZero() {
		stamp = e.at.Local().Format("15:04")
	}
	head := cDim + stamp + " " + cReset
	body := oneLine(e.text)
	switch e.kind {
	case "user":
		return head + cFG + clipStr(body, 400) + cReset
	case "assistant":
		return head + color + clipStr(body, 400) + cReset
	case "thinking":
		return head + cDim + "thinking · " + clipStr(body, 200) + cReset
	case "tool":
		name := e.name
		if name == "" {
			name = "tool"
		}
		return head + cCyan + "→ " + name + cReset + " " + cDim + clipStr(body, 200) + cReset
	case "result":
		return head + cDim + "  ← " + clipStr(body, 160) + cReset
	}
	return head + cDim + clipStr(body, 200) + cReset
}
