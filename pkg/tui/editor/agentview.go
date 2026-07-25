package editor

import (
	"fmt"
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

// loadAgentEntries reads (and caches) a session's transcript and what it adds
// up to. The cache is keyed in the node store and dropped whenever the session
// is opened again, so scrolling a long conversation never re-reads it per frame.
func (m *Model) loadAgentEntries(id string, v agentVariant, s agentSession) ([]agentEntry, agentStats) {
	store := m.nodeStore(id)
	if e, ok := store["agentEntries"].([]agentEntry); ok {
		if got, _ := store["agentEntriesID"].(string); got == s.SessionID {
			st, _ := store["agentStats"].(agentStats)
			return e, st
		}
	}
	entries, stats, path := agentTranscript(v.transcriptDirs(), v.exts, s.SessionID)
	store["agentEntries"] = entries
	store["agentStats"] = stats
	store["agentEntriesID"] = s.SessionID
	store["agentEntriesPath"] = path
	return entries, stats
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

// agentRenameState returns the open rename field for a handle, or nil. The name
// is edited IN the panel — the session's own line becomes the field — so
// renaming never leaves the outline.
func (m *Model) agentRenameState(id string) *textField {
	f, _ := m.nodeStore(id)["agentRename"].(*textField)
	return f
}

// agentViewKey drives the panel: n renames the session, alt+r reopens it, r
// rereads the transcript, alt+o opens a hosted one in the browser, the rest
// scrolls. esc falls through to the central defocus — unless a rename is open,
// which takes esc for itself.
func (m *Model) agentViewKey(h agentHandle, k tea.KeyMsg) (tea.Cmd, bool) {
	if f := m.agentRenameState(h.id); f != nil {
		switch k.String() {
		case "enter":
			m.agentRename(h.id, f.value)
			delete(m.nodeStore(h.id), "agentRename")
			m.flash = "session renamed"
			return nil, true
		case "esc":
			delete(m.nodeStore(h.id), "agentRename")
			return nil, true
		}
		f.handleKey(k) // the shared field vocabulary: runes, backspace, caret moves
		return nil, true
	}
	switch k.String() {
	case "n":
		// seed with the name in force, so a rename starts from what is on screen
		f := &textField{value: h.sess.Name}
		if f.value == "" {
			f.value = m.agentTitle(h.id, h.v, h.sess)
		}
		f.caret = len([]rune(f.value))
		m.nodeStore(h.id)["agentRename"] = f
		return nil, true
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

// agentBandContent renders the whole panel: a labelled field block (what this
// session IS — where it runs, on what branch, which model, what it has spent),
// a rule, then the log of everything it did. The node row above carries only the
// pill; all of this shows up only while the row is expanded.
func (m *Model) agentBandContent(h agentHandle, rail string, width int) []string {
	if width <= 0 {
		width = 1 << 20
	}
	color := m.agentColorFor(h.id, h.v, h.sess)
	line := func(s string) string { return clip(rail+cReset+s, width) }
	field := func(k, v string) string {
		return line("  " + cDim + fmt.Sprintf("%-8s", k) + cReset + v + cReset)
	}

	entries, stats := m.loadAgentEntries(h.id, h.v, h.sess)

	// the rename field takes the top line while it is open
	var content []string
	if st := m.agentRenameState(h.id); st != nil {
		content = append(content, line("  "+cDim+"name    "+cReset+
			withCaret(st.value, st.caret)+cDim+"   enter save · esc cancel · empty = the CLI's own name"+cReset))
	} else {
		content = append(content, line("  "+color+h.v.glyph+" "+m.agentTitle(h.id, h.v, h.sess)+cReset+
			cDim+"  n rename"+cReset))
	}

	if h.sess.Cwd != "" || stats.cwd != "" {
		content = append(content, field("dir", cFG+tildePath(NodeFirstNonEmpty(h.sess.Cwd, stats.cwd))))
	}
	if stats.branch != "" {
		content = append(content, field("branch", cFG+stats.branch))
	}
	if stats.model != "" {
		model := stats.model
		if stats.effort != "" {
			model += cDim + " · effort " + stats.effort
		}
		content = append(content, field("model", cFG+model))
	}
	if tok := agentTokenLine(stats); tok != "" {
		content = append(content, field("tokens", cFG+tok))
	}
	if h.sess.SessionID != "" {
		id := agentShortID(h.sess.SessionID)
		if stats.turns > 0 {
			id += cDim + " · " + fmt.Sprintf("%d turns", stats.turns)
		}
		if h.sess.OpenedAt > 0 {
			id += cDim + " · opened " + relTime(h.sess.OpenedAt)
		}
		content = append(content, field("session", cFG+id))
	}
	content = append(content, field("state", m.agentStateLine(h)))

	keys := "  ⌥r open · n rename · r reload · ↑↓ scroll · esc close"
	if h.v.webURL != nil || h.sess.Remote != "" {
		keys = "  ⌥r open · ⌥o browser · n rename · r reload · ↑↓ scroll · esc close"
	}
	content = append(content, line(cDim+keys+cReset))
	content = append(content, line("  "+cDim+strings.Repeat("─", 58)+cReset))

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

// agentStateLine says what the session is doing right now: running in its CLI,
// hosted on the web, finished, or waiting to be opened.
func (m *Model) agentStateLine(h agentHandle) string {
	switch {
	case m.agentLive(h.v, h.sess):
		return cGreen + "live" + cReset + cDim + " · the CLI is running it now" + cReset
	case m.agentCloud(h.v, h.sess):
		return cAccent + glyphCloud + " hosted" + cReset + cDim + " · ⌥o opens it in the browser" + cReset
	case h.sess.SessionID == "":
		return cDim + "not started · ⌥r opens it" + cReset
	default:
		return cDim + "idle · ⌥r resumes it" + cReset
	}
}

// agentTokenLine renders a session's spend: prompt, completion and the cache
// reads that dominate a long conversation. "" when the store reports no usage.
func agentTokenLine(s agentStats) string {
	if s.inTok == 0 && s.outTok == 0 && s.cacheTok == 0 {
		return ""
	}
	parts := []string{agentThousands(s.inTok) + " in", agentThousands(s.outTok) + " out"}
	if s.cacheTok > 0 {
		parts = append(parts, agentThousands(s.cacheTok)+" cached")
	}
	return strings.Join(parts, cDim+" · "+cFG)
}

// agentThousands renders a token count compactly (46.8k, 1.2M).
func agentThousands(n int) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1e6)
	case n >= 1_000:
		return fmt.Sprintf("%.1fk", float64(n)/1e3)
	}
	return fmt.Sprintf("%d", n)
}

// agentEntryLine renders one transcript entry: a dim clock, then the speaker or
// the tool call. A TOOL CALL is what a session is skimmed for, so it reads as
// the tool's name in its own color followed by the call's arguments as compact
// JSON — the call exactly as the agent made it.
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
		args := e.args
		if args == "" {
			args = body
		}
		return head + agentToolColor(name) + fmt.Sprintf("%-7s", name) + cReset +
			cDim + clipStr(args, 220) + cReset
	case "result":
		return head + cDim + "  ← " + clipStr(body, 160) + cReset
	}
	return head + cDim + clipStr(body, 200) + cReset
}

// agentToolColor gives each kind of tool call its own color, so a log reads at a
// glance: reads/searches cool, writes warm, shell green.
func agentToolColor(name string) string {
	switch strings.ToLower(name) {
	case "bash", "shell", "run", "exec", "terminal":
		return cGreen
	case "edit", "write", "apply_patch", "multiedit", "notebookedit", "create":
		return cYellow
	case "task", "agent", "todowrite", "plan":
		return cMagenta
	case "websearch", "webfetch", "fetch":
		return cAccent
	}
	return cCyan // read, grep, glob, ls and everything unrecognized
}
