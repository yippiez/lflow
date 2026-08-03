package editor

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lflow/lflow/pkg/tui/database"
)

// The agentic coding SESSION: an inline CHIP — and only a chip, so a session can
// be dropped into whatever note it belongs to instead of owning a row of its own.
// It reads as one token: the agent's glyph and the session's name on the agent's
// color, in whatever ink contrasts with that fill, plus a cloud mark when the
// session is a hosted one.
//
// ⌥r hands the terminal over: lflow suspends itself, the CLI takes the screen in
// the session's pinned working directory, and closing it drops straight back into
// the outline with the session's id adopted. The first run CREATES the session and
// every later one RESUMES it, so a chip is a durable handle on one conversation.
// ⌥e opens a small panel beneath the row — the session's name, where it runs, what
// it is doing — and nothing else; the conversation itself belongs to the CLI. ⌥o
// opens a hosted session in the browser, since there is no local process to attach
// to. /agents lists every session in the outline, live ones first.
//
// WARNING (invariant): lflow never copies a conversation into the outline. A chip
// stores only {variant, cwd, session id} in LOCAL node_output — never in the chip
// row, never synced — and the session's name, color and state are always read back
// out of the CLI's own store (agentstore.go) on demand.

// agentVariant declares one agentic coding CLI lflow can host a session for.
// Adding a CLI is one entry in this table and nothing else.
type agentVariant struct {
	id    string // the id stored in a chip's value, and what the picker calls it
	label string // picker label
	bin   string // the CLI binary lflow suspends into
	glyph string // the agent's mark, worn by every chip of this variant
	// color is the variant's own theming: a /style swatch name, which follows the
	// active theme, or a #rrggbb literal for an agent whose mark the eight-swatch
	// palette cannot name.
	color string

	// args builds the CLI's argv to RESUME the given session. lflow never starts a
	// session of its own, so there is no create form: a chip always points at a
	// conversation its CLI already made.
	args func(sessionID string) []string

	// sessionRoots is where the CLI keeps its sessions: what the "attach an
	// existing session" picker lists and where a session's name is read from.
	sessionRoots func() []string
	exts         []string // record file extensions, e.g. ".jsonl"
	// sessionPath narrows discovery to records under this path fragment, for a
	// store that keeps sessions and their messages as sibling trees.
	sessionPath string
	// registryRoots is where the CLI records its RUNNING sessions (Claude Code
	// writes one file per live session). It is what tells lflow a session's own
	// name and whether it is live.
	registryRoots func() []string

	// rename writes a NEW NAME into the CLI's own store, for a store whose format
	// makes that safe. nil = this CLI's name is read-only to lflow, and a rename
	// stays local to the chip. See agentRename.
	rename func(path, name string) error
}

// agentMono is the fill for an agent whose brand names no color — a black mark on
// white. The pill is that mark: black fill, white ink, which contrastInk arrives
// at on its own. Being a literal it does not follow the active theme, which is the
// point: it is the agent's color, not one of ours. On a terminal whose background
// is already pure black the pill's edge will not be visible; the white ink is.
const agentMono = "#000000"

// agentVariants is the registry: the three CLIs lflow can hand the terminal to.
// Adding one is an entry here and nothing else.
//
// Each glyph is plain Unicode, never an emoji — a chip has to paint in one cell,
// and the CLIs' own marks are emoji or images. Claude Code's florette ✽, Pi in
// small caps, a squared O for opencode. ᴘ (U+1D18) needs the phonetic block: it is
// absent from DejaVu Sans Mono and present in Liberation/Free Mono and most modern
// terminal fonts.
//
// A variant wears its agent's OWN color and never an invented one: Claude's
// orange and Pi's purple are themed swatches, while opencode's mark is black on
// white and takes agentMono. That is only the DEFAULT — ⌥c recolors any chip, and
// a session Claude Code gave a color of its own wears that instead.
var agentVariants = []agentVariant{
	{
		id: "claude", label: "Claude Code", bin: "claude",
		glyph: "✽", color: "orange",
		// --resume takes the id the CLI already minted; lflow never chooses one
		args:          func(id string) []string { return []string{"--resume", id} },
		sessionRoots:  func() []string { return homeStores(".claude/projects") },
		registryRoots: func() []string { return homeStores(".claude/sessions") },
		exts:          []string{".jsonl"},
	},
	{
		id: "pi", label: "Pi", bin: "pi",
		glyph: "ᴘɪ", color: "purple",
		args:         func(id string) []string { return []string{"--session-id", id} },
		sessionRoots: func() []string { return homeStores(".pi/agent/sessions", ".pi/sessions", "<data>/pi/sessions") },
		exts:         []string{".jsonl", ".json"},
	},
	{
		id: "opencode", label: "opencode", bin: "opencode",
		glyph: "▣", color: agentMono,
		args: func(id string) []string { return []string{"--session", id} },
		// opencode keeps sessions and their messages as sibling trees (and, in newer
		// layouts, one such pair per project), so discovery is pinned to the session
		// side.
		sessionRoots: func() []string { return homeStores("<data>/opencode/storage", "<data>/opencode/project") },
		exts:         []string{".json"},
		sessionPath:  "/session/",
	},
}

// agentVariantByID returns the variant for a chip's value ("claude").
func agentVariantByID(id string) (agentVariant, bool) {
	for _, v := range agentVariants {
		if v.id == id {
			return v, true
		}
	}
	return agentVariant{}, false
}

// agentBins lists every CLI the session chips shell out to, so deps.go probes
// them the way node types declare cliDeps.
func agentBins() []string {
	out := make([]string, 0, len(agentVariants))
	for _, v := range agentVariants {
		out = append(out, v.bin)
	}
	return out
}

// colorSGR is the variant's fill. It goes through the same reader a session's
// own recorded color does, so an agent whose mark the palette cannot name may
// declare a literal instead of a swatch.
func (v agentVariant) colorSGR() string { return agentColorSGR(v.color) }

func (v agentVariant) sessionDirs() []string {
	if v.sessionRoots != nil {
		return v.sessionRoots()
	}
	return nil
}

// ── session data (local, node_output) ──────────────────────────────────────

// agentSession is what lflow stores for one chip — the pointer, never the
// conversation. It lives in node_output keyed by the CHIP id, which is LOCAL and
// never synced: a session id only means something on the machine whose CLI store
// holds it.
type agentSession struct {
	Variant   string `json:"variant"`
	Cwd       string `json:"cwd,omitempty"`
	SessionID string `json:"session,omitempty"`
	Title     string `json:"title,omitempty"` // last name read from the CLI's store
	// Name is the user's OWN name for this session, like a link chip's name: set
	// it and the chip reads that instead of whatever the CLI calls the session.
	// Empty = follow the CLI's live name.
	Name string `json:"name,omitempty"`
	// Color is the user's OWN color for this chip (⌥c), a /style swatch name.
	// Empty = the variant's default. Local like the rest of this record.
	Color string `json:"color,omitempty"`
}

// agentLoad reads a chip's session data, memory-cached in the node store: the
// render path asks for it every frame and it changes only when lflow writes it.
func (m *Model) agentLoad(id string) agentSession {
	store := m.nodeStore(id)
	if s, ok := store["agentSession"].(agentSession); ok {
		return s
	}
	var s agentSession
	if m.db != nil {
		if raw, err := database.LoadNodeOutput(m.db, id); err == nil && raw != "" {
			_ = json.Unmarshal([]byte(raw), &s)
		}
	}
	store["agentSession"] = s
	return s
}

func (m *Model) agentSave(id string, s agentSession) {
	m.nodeStore(id)["agentSession"] = s
	if m.db == nil {
		return
	}
	if raw, err := json.Marshal(s); err == nil {
		_ = database.SaveNodeOutput(m.db, id, string(raw))
	}
}

// agentMeta reads the session's identity out of the CLI's store: its name, and
// any color the CLI gave it. The read is gated twice — a short TTL, then the
// record file's mtime — so a render loop stats a file at most every few seconds
// and only re-parses a session that actually moved.
func (m *Model) agentMeta(id string, v agentVariant, s agentSession) agentStoreSession {
	if s.SessionID == "" {
		return agentStoreSession{variant: v.id}
	}
	store := m.nodeStore(id)
	cached, hasCached := store["agentMeta"].(agentStoreSession)
	if hasCached && cached.id == s.SessionID {
		// a session whose records were NOT found costs a whole store walk to look
		// for again, so it is retried far less often than one being watched
		ttl := 3 * time.Second
		if cached.path == "" {
			ttl = 30 * time.Second
		}
		if at, ok := store["agentMetaAt"].(time.Time); ok && time.Since(at) < ttl {
			return cached
		}
		if st, err := os.Stat(cached.path); err == nil && st.ModTime().Equal(cached.modAt) {
			store["agentMetaAt"] = time.Now()
			return cached
		}
	}
	meta := agentStoreSession{variant: v.id, id: s.SessionID}
	if path := agentSessionPath(v.sessionDirs(), v.exts, s.SessionID); path != "" {
		meta = agentReadMeta(v.id, path)
		meta.id = s.SessionID
	}
	store["agentMeta"], store["agentMetaAt"] = meta, time.Now()
	return meta
}

// agentTitle is the session's display name, in priority order:
//
//	the user's own name for it (like a link chip's name)
//	→ the name the CLI gave the running session (its registry)
//	→ the name in the CLI's store — so a session renamed inside the CLI reads
//	  renamed here too
//	→ the last name seen → "session <id>".
//
// Pure: it is called from the render path, which never writes.
func (m *Model) agentTitle(id string, v agentVariant, s agentSession) string {
	if s.Name != "" {
		return s.Name
	}
	if reg, ok := m.agentRegOf(v, s.SessionID); ok && reg.name != "" {
		return reg.name
	}
	if meta := m.agentMeta(id, v, s); meta.title != "" {
		return meta.title
	}
	if s.Title != "" {
		return s.Title
	}
	if s.SessionID == "" {
		return "new session"
	}
	return "session " + agentShortID(s.SessionID)
}

// agentRegOf looks a session up in its CLI's live registry (cached briefly —
// the registry is a handful of small files, but the render path asks per frame).
func (m *Model) agentRegOf(v agentVariant, sessionID string) (agentReg, bool) {
	if sessionID == "" || v.registryRoots == nil {
		return agentReg{}, false
	}
	store := m.nodeStore("agent.registry." + v.id)
	regs, _ := store["regs"].(map[string]agentReg)
	at, _ := store["at"].(time.Time)
	if regs == nil || time.Since(at) > 3*time.Second {
		regs = agentRegistry(v.registryRoots())
		store["regs"], store["at"] = regs, time.Now()
	}
	reg, ok := regs[sessionID]
	return reg, ok
}

// agentColorFor is the color a chip is drawn in: the one YOU picked with ⌥c, else
// the variant's default. No CLI of the three records a color for a session, so
// there is nothing else to read. Pure — it is called from the render path.
func (m *Model) agentColorFor(id string, v agentVariant, s agentSession) string {
	if s.Color != "" {
		if c := agentColorSGR(s.Color); c != "" {
			return c
		}
	}
	return v.colorSGR()
}

// agentShortID trims a session id to a readable stub.
func agentShortID(id string) string {
	if len(id) <= 8 {
		return id
	}
	return id[:8]
}

// tildePath shortens a path under the home directory to "~/…".
func tildePath(p string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return p
	}
	if rest, ok := strings.CutPrefix(p, home); ok {
		return "~" + rest
	}
	return p
}

// ── the chip's look ────────────────────────────────────────────────────────

// agentLooks is the render-time color of each session, published by the Model.
// The inline render path is deliberately Model-less (renderBody takes an item and
// a chip record), while a session's color comes from its CLI's store — so the
// Model publishes it here and renderAgentChip reads it, exactly like the
// tag-color map the render path already reads.
var agentLooks = map[string]string{}

// publishAgentLook recomputes one chip's color. Cheap: the session data is
// memory-cached and the CLI store is only re-read when its records moved.
func (m *Model) publishAgentLook(id string, v agentVariant) string {
	col := m.agentColorFor(id, v, m.agentLoad(id))
	agentLooks[id] = col
	return col
}

// refreshAgentLooks republishes the color and label of every session chip on
// screen. Called at the end of refreshRows, which is what keeps a chip's text in
// step with a session renamed inside its CLI.
func (m *Model) refreshAgentLooks() {
	for _, r := range m.rows {
		if r.it == nil || !hasAnchor(r.it.name) {
			continue
		}
		for _, sp := range anchorSpans([]rune(r.it.name)) {
			c, ok := m.chips[sp.id]
			if !ok || c.Kind != chipKindAgent {
				continue
			}
			if v, ok := agentVariantByID(c.Value); ok {
				m.publishAgentLook(c.ID, v)
				m.refreshAgentChip(c.ID)
			}
		}
	}
}

// ── opening a session ──────────────────────────────────────────────────────

// agentClosedMsg lands when the CLI lflow suspended for has exited.
type agentClosedMsg struct {
	id      string // the chip id — the session handle's storage key
	variant string
	err     error
}

// agentOpen suspends lflow and hands the terminal to the CLI, creating the
// session on the first open and resuming it on every later one. The editor is
// released for the whole run and redraws when the CLI exits.
func (m *Model) agentOpen(v agentVariant, id, cwd string) tea.Cmd {
	if !m.depOK(v.bin) {
		m.errorFlash("Missing dependency: " + v.bin)
		return nil
	}
	s := m.agentLoad(id)
	s.Variant = v.id
	if s.Cwd == "" {
		s.Cwd = cwd
	}
	// lflow does not start conversations: a chip is a handle on one the CLI
	// already has, so an empty id means the chip was never pointed at anything.
	if s.SessionID == "" {
		m.flash = v.label + " · no session attached · /agent finds one"
		return nil
	}
	// a session pinned to a directory that is gone (an attached session from
	// another machine, a moved repo) refuses to open rather than silently running
	// the agent somewhere else — which repo it works in is the whole point
	if s.Cwd != "" {
		if st, err := os.Stat(s.Cwd); err != nil || !st.IsDir() {
			m.errorFlash(v.id + ": " + tildePath(s.Cwd) + " is gone · the session is pinned to it")
			return nil
		}
	}
	m.agentSave(id, s)

	// flush pending edits before handing the terminal over: the agent may reach
	// back into the outline through the lflow CLI while it runs, and it must see
	// what is on screen here.
	_ = m.flushSync()

	c := exec.Command(v.bin, v.args(s.SessionID)...)
	if s.Cwd != "" {
		c.Dir = s.Cwd
	}
	// tea.ExecProcess releases the terminal for the whole run: the CLI owns the
	// screen, and the editor repaints from scratch on return.
	return tea.ExecProcess(c, func(err error) tea.Msg {
		return agentClosedMsg{id: id, variant: v.id, err: err}
	})
}

// handleAgentClosed lands the CLI's exit: run bookkeeping, and the session's
// current name re-read from the store the CLI may have just renamed it in.
func (m *Model) handleAgentClosed(msg agentClosedMsg) {
	v, ok := agentVariantByID(msg.variant)
	if !ok {
		return
	}
	s := m.agentLoad(msg.id)
	s.Variant = v.id
	if meta := agentReadMeta(v.id, agentSessionPath(v.sessionDirs(), v.exts, s.SessionID)); meta.title != "" {
		s.Title = meta.title
	}
	m.agentSave(msg.id, s)
	delete(m.nodeStore(msg.id), "agentMeta") // the session moved - re-read it
	m.refreshAgentChip(msg.id)

	if msg.err != nil {
		m.errorFlash(v.label + ": " + msg.err.Error())
		return
	}
	m.flash = v.id + " · session " + agentShortID(s.SessionID)
}

// agentAttach points a chip at an EXISTING session from the CLI's own store —
// the "I already have this conversation, file it here" path.
func (m *Model) agentAttach(id string, sess agentStoreSession) {
	v, ok := agentVariantByID(sess.variant)
	if !ok {
		return
	}
	s := m.agentLoad(id)
	s.Variant, s.SessionID, s.Title = v.id, sess.id, sess.title
	if sess.cwd != "" {
		s.Cwd = sess.cwd
	}
	if s.Cwd == "" {
		s.Cwd = processCWD()
	}
	m.agentSave(id, s)
	delete(m.nodeStore(id), "agentMeta")
	m.refreshAgentChip(id)
}

// agentRename sets (or clears, with "") the user's own name for a session — the
// chip's name, exactly like a link chip's. Clearing it hands the name back to
// the CLI's live name.
func (m *Model) agentRename(id, name string) {
	s := m.agentLoad(id)
	s.Name = strings.TrimSpace(name)
	m.agentSave(id, s)
	delete(m.nodeStore(id), "agentMeta")

	v, ok := agentVariantByID(s.Variant)
	if !ok {
		m.refreshAgentChip(id)
		return
	}
	// then push it down into the CLI's own store, for the one whose format makes
	// that safe. A store lflow cannot write keeps the name here and nowhere else,
	// which is the whole reason the local name exists and wins.
	m.flash = "renamed here"
	if v.rename != nil && s.Name != "" && s.SessionID != "" {
		if path := agentSessionPath(v.sessionDirs(), v.exts, s.SessionID); path != "" {
			if err := v.rename(path, s.Name); err != nil {
				m.errorFlash("renamed here · " + v.label + ": " + err.Error())
			} else {
				m.flash = "renamed here and in " + v.label
			}
		}
	}
	m.refreshAgentChip(id)
	m.publishAgentLook(id, v)
}

// ── the chip ───────────────────────────────────────────────────────────────

// agentChipAtCaret returns the session chip the caret sits on (its anchor begins
// at the caret or ends exactly at it), like cmdChipAtCaret.
func (m *Model) agentChipAtCaret(cur *item) (database.Chip, bool) {
	if cur == nil {
		return database.Chip{}, false
	}
	spans := anchorSpans([]rune(cur.name))
	for _, sp := range []*anchorSpan{spanStartingAt(spans, m.caret), spanEndingAt(spans, m.caret)} {
		if sp == nil {
			continue
		}
		if c, ok := m.chips[sp.id]; ok && c.Kind == chipKindAgent {
			return c, true
		}
	}
	return database.Chip{}, false
}

// insertAgentChip splices a chip pointed at an EXISTING session at the caret.
// There is no create path: lflow files conversations, it does not start them.
func (m *Model) insertAgentChip(cur *item, v agentVariant, attach agentStoreSession) {
	if cur == nil {
		return
	}
	anchor := m.createChip(chipKindAgent, v.id)
	if anchor == "" {
		return
	}
	id := anchorChipID(anchor)
	if attach.id != "" {
		m.agentAttach(id, attach)
	} else {
		m.agentSave(id, agentSession{Variant: v.id, Cwd: processCWD()})
	}
	m.insertLiteralAt(cur, m.caret, anchor)
	m.publishAgentLook(id, v)
	m.refreshAgentChip(id)
	m.flash = v.label + " · ⌥r opens · ⌥n renames · ⌥c recolors"
}

// agentChipForKeys is what ⌥r / ⌥e / ⌥n / ⌥c act on. It prefers the chip the
// caret is exactly on, and falls back to the row's chip when the row has only
// one — a row wide enough to WRAP puts End on the end of a visual line rather
// than after the chip, and a key that silently does nothing there reads as a
// broken key rather than a missed target.
//
// A row with several sessions and an ambiguous caret is left alone on purpose:
// guessing which one you meant is worse than saying so.
func (m *Model) agentChipForKeys(cur *item) (database.Chip, bool) {
	if c, ok := m.agentChipAtCaret(cur); ok {
		return c, true
	}
	if cur == nil {
		return database.Chip{}, false
	}
	var only database.Chip
	n := 0
	for _, sp := range anchorSpans([]rune(cur.name)) {
		if c, ok := m.chips[sp.id]; ok && c.Kind == chipKindAgent {
			only, n = c, n+1
		}
	}
	if n == 1 {
		return only, true
	}
	if n > 1 {
		m.flash = fmt.Sprintf("put the caret on a session chip · this row has %d", n)
	}
	return database.Chip{}, false
}

// anchorChipID recovers the chip id from an anchor built by createChip.
func anchorChipID(anchor string) string {
	return strings.Trim(anchor, string(chipSentinel))
}

// runAgentChip is ⌥r on a session chip: open, or resume, the session.
func (m *Model) runAgentChip(c database.Chip) tea.Cmd {
	v, ok := agentVariantByID(c.Value)
	if !ok {
		m.errorFlash("unknown agent: " + c.Value)
		return nil
	}
	s := m.agentLoad(c.ID)
	cwd := s.Cwd
	if cwd == "" {
		cwd = processCWD()
	}
	return m.agentOpen(v, c.ID, cwd)
}

// refreshAgentChip refreshes a chip's inline label — the name the pill shows:
// the user's own name for the session when it has one, else the session's live
// name, so a session renamed inside its CLI reads renamed here too. A hosted
// session's label carries the cloud mark. Like the cmd chip's run preview the
// label is mutated IN MEMORY only and never written to the chips table: the
// variant persists, the session's chrome does not (it is local to this machine).
func (m *Model) refreshAgentChip(id string) {
	c, ok := m.chips[id]
	if !ok || c.Kind != chipKindAgent {
		return
	}
	v, ok := agentVariantByID(c.Value)
	if !ok {
		return
	}
	s := m.agentLoad(id)
	label := ""
	if s.Name != "" || s.SessionID != "" {
		label = clipStr(m.agentTitle(id, v, s), 34)
	}
	c.Label = label
	m.chips[id] = c
}

// hydrateAgentChips refreshes every session chip's label from the CLI stores.
// Called wherever hydrateCmdPreviews is — on open and after an aux reload — so a
// reopened editor shows current session names without touching the chip rows.
func (m *Model) hydrateAgentChips() {
	for id, c := range m.chips {
		if c.Kind != chipKindAgent {
			continue
		}
		if v, ok := agentVariantByID(c.Value); ok {
			m.publishAgentLook(id, v)
		}
		m.refreshAgentChip(id)
	}
}

// agentChipDisplay is the chip's compact form — what every surface OUTSIDE the
// styled editor (the CLI's list/grep, exports) reads: the agent's glyph and the
// session's name. The editor draws the same thing as a filled pill; see
// renderAgentChip.
func agentChipDisplay(c database.Chip) string {
	glyph := "◈"
	if v, ok := agentVariantByID(c.Value); ok {
		glyph = v.glyph
	}
	name := c.Label
	if name == "" {
		name = c.Value
	}
	return glyph + " " + name
}
