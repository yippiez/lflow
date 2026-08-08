package editor

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lflow/lflow/tui/database"
)

// The agentic coding SESSION has two handles: an inline chip that can be dropped
// into a sentence, and an Agent node whose expanded face gives the transcript a
// full row. Both read the same local pointer; neither owns conversation records.
// The chip reads as one token: the agent's glyph and the session's name on the agent's
// color, in whatever ink contrasts with that fill, plus a cloud mark when the
// session is a hosted one.
//
// ⌥r runs the session on the MULTIPLEXER: the CLI resumes on a background PTY
// in the session's pinned working directory and lflow's full-screen attach
// view opens on it (ctrl+q detaches, the agent keeps running). The chip
// shimmers while the agent works. ⌥o opens the session in a new HOST terminal
// window instead — the native resume, outside lflow (a live mux session is
// stopped and handed over). ⌥m sends it a quick message without attaching;
// the box shows the agent's last word. ⌥e opens the session edit page — name
// and color. /agents lists every session in the outline, live ones first.
//
// WARNING (invariant): lflow never copies a conversation into the outline. A chip
// or Agent node stores only {variant, cwd, session id} plus the name and color it
// imported, in LOCAL node_output — never in the chip/node row, never synced.
// Expanded trace rows are virtual and read-only; the messages come from the CLI's
// own store. Identity travels ONE WAY and ONCE: read at the moment a chip is
// pointed at a session, never re-read, and never written back.

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

	// dbPaths is where the CLI keeps its sessions when they live in a SQLite
	// database rather than record files (opencode v1: opencode.db). When it is
	// set and a store is found, the picker, the per-session lookups and the
	// trace read the database READ-ONLY; sessionRoots stays as the fallback
	// for releases that still write the record files.
	dbPaths func() []string

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

	// idents reads the CLI's OWN record of what its sessions are called and what
	// color it gave them, keyed by session id — for a CLI that keeps that
	// somewhere other than the transcript. Claude Code does: its job store holds
	// both, and a session read only from its transcript has no name at all, which
	// is why one used to be labelled with its first prompt. nil = this CLI writes
	// its name into the session record itself, where agentReadMeta finds it.
	idents func() map[string]agentIdent

	// hasColor declares that this CLI records a color OF ITS OWN for a session,
	// so the picker can offer a session in the color its CLI gives it and a chip
	// can import that color when it is picked. A CLI without one simply starts
	// its chips in the agent's default; ⌥c works the same either way.
	hasColor bool
}

// WARNING (invariant): lflow READS the CLIs' stores and never writes to them.
// Names and colors travel one way — imported once, at the moment a chip is
// pointed at a session (see agentAttach). There is deliberately no hook here for
// writing one back.

// agentMono is the fill for an agent whose brand names no color — a black mark on
// white. The pill is that mark: black fill, white ink, which contrastInk arrives
// at on its own. Being a literal it does not follow the active theme, which is the
// point: it is the agent's color, not one of ours. On a terminal whose background
// is already pure black the pill's edge will not be visible; the white ink is.
const agentMono = "#000000"

// agentVariants is the registry: the four CLIs lflow can hand the terminal to.
// Adding one is an entry here and nothing else.
//
// Each glyph is plain Unicode, never an emoji — a chip has to paint in one cell,
// and the CLIs' own marks are emoji or images. Claude Code's florette ✽, Pi in
// small caps, a squared O for opencode, and prime-agent wears its runtime's ≋
// watermark. ᴘ (U+1D18) needs the phonetic block: it is absent from
// DejaVu Sans Mono and present in Liberation/Free Mono and most modern terminal
// fonts.
//
// A variant wears its agent's OWN color and never an invented one: Claude's
// orange and Pi's purple are themed swatches, while opencode's mark is black on
// white and takes agentMono and prime-agent is painted in Prime Intellect's neon
// green. That is only the DEFAULT — ⌥c recolors any chip, and a session Claude
// Code gave a color of its own wears that instead.
var agentVariants = []agentVariant{
	{
		id: "claude", label: "Claude Code", bin: "claude",
		glyph: "✽", color: "orange",
		// --resume takes the id the CLI already minted; lflow never chooses one
		args:          func(id string) []string { return []string{"--resume", id} },
		sessionRoots:  func() []string { return homeStores(".claude/projects") },
		registryRoots: func() []string { return homeStores(".claude/sessions") },
		// name and color both live beside the transcript rather than inside it: the
		// job store holds each session's name AND its color — the only one of the
		// three CLIs to record a color — and the session registry names sessions
		// the job store never had a job for.
		idents: func() map[string]agentIdent {
			return mergeIdents(
				claudeIdents(homeStores(".claude/jobs")),
				registryIdents(homeStores(".claude/sessions")),
			)
		},
		hasColor: true,
		exts:     []string{".jsonl"},
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
		// opencode v1 keeps the whole store in a SQLite database, not the
		// record files newer layouts abandoned; the file paths below survive
		// as the fallback for releases that still write them. Only the v1
		// database (opencode.db) is read; v2's opencode-next.db has a
		// different schema and is deliberately not touched yet.
		dbPaths: func() []string { return agentDBPaths("<data>/opencode/opencode.db") },
		// opencode keeps sessions and their messages as sibling trees (and, in newer
		// layouts, one such pair per project), so discovery is pinned to the session
		// side.
		sessionRoots: func() []string { return homeStores("<data>/opencode/storage", "<data>/opencode/project") },
		exts:         []string{".json"},
		sessionPath:  "/session/",
	},
	{
		// prime-agent is the RLM runtime's own CLI; -r resumes a saved session by
		// id, resolved against its session dir. Its transcript schema is pi's —
		// session, message and session_info records — so the tolerant reader
		// parses it unchanged. It wears Prime Intellect's neon green with a dark
		// mark: the ≋ is the runtime's own watermark, painted black on the green.
		id: "prime-agent", label: "Prime Agent", bin: "prime-agent",
		glyph: "≋", color: "#85ed75",
		args:         func(id string) []string { return []string{"-r", id} },
		sessionRoots: func() []string { return homeStores(".prime/agent/sessions") },
		exts:         []string{".jsonl"},
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
	// Title is the name the CLI had for the session AT THE MOMENT it was picked —
	// a snapshot, not a subscription. The CLI is free to rename it afterwards; the
	// chip keeps reading what you filed.
	Title string `json:"title,omitempty"`
	// Name is the user's OWN name for this session, like a link chip's name: set
	// it and the chip reads that instead of the imported title.
	Name string `json:"name,omitempty"`
	// Color is the chip's color, a /style swatch name: imported from the CLI when
	// the session was picked, replaced by whatever you choose with ⌥c. Empty = the
	// variant's default.
	Color string `json:"color,omitempty"`
	// Snapshot records that the identity above was taken from the CLI already, so
	// it is never taken again. It is what makes "imported once" different from
	// "imported every frame": without it, a chip whose CLI records no color at all
	// would go looking for one forever.
	Snapshot bool `json:"snapshot,omitempty"`
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
	if m, ok := v.agentStoreSessionFor(s.SessionID); ok {
		meta = m
		meta.variant, meta.id = v.id, s.SessionID
	}
	meta.applyIdent(m.agentIdentOf(v, s.SessionID))
	store["agentMeta"], store["agentMetaAt"] = meta, time.Now()
	return meta
}

// agentIdentOf looks a session up in its CLI's own name/color index, cached
// briefly for the same reason the registry is: the index is a directory of small
// files, and the render path asks about a chip every frame.
func (m *Model) agentIdentOf(v agentVariant, sessionID string) agentIdent {
	if sessionID == "" || v.idents == nil {
		return agentIdent{}
	}
	store := m.nodeStore("agent.idents." + v.id)
	idx, _ := store["idents"].(map[string]agentIdent)
	at, _ := store["at"].(time.Time)
	if idx == nil || time.Since(at) > 3*time.Second {
		idx = v.idents()
		store["idents"], store["at"] = idx, time.Now()
	}
	return idx[sessionID]
}

// agentTitle is the session's display name, in priority order:
//
//	the user's own name for it (like a link chip's name)
//	→ the name imported when the session was picked
//	→ "session <id>".
//
// Nothing here reads the CLI. A chip's identity is a SNAPSHOT taken the moment
// you pointed it at a session, not a live view of one: renaming a session in pi
// or Claude Code afterwards leaves this outline exactly as you left it, the same
// way lflow never writes a name of yours back into their stores. Two tools that
// each keep editing the other's labels is a fight, not a feature.
//
// Pure: it is called from the render path, which never writes.
func (m *Model) agentTitle(id string, v agentVariant, s agentSession) string {
	if s.Name != "" {
		return s.Name
	}
	if s.Title != "" {
		return s.Title
	}
	if s.SessionID == "" {
		return "new session"
	}
	return "session " + agentShortID(s.SessionID)
}

// agentColorFor is the color a chip is drawn in, in priority order:
//
//	the chip's own color — the one imported when the session was picked, or the
//	one YOU picked with ⌥c since
//	→ the variant's own default.
//
// Like the name, it is a snapshot: the CLI's color is read once, at the moment
// you point a chip at a session, and never again. Pure — the render path calls
// it, and the render path never writes.
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

// agentOpen resumes the CLI on a background multiplexer session and opens the
// attach view on it — lflow keeps the screen; the agent runs behind it. A chip
// whose session is already live just re-attaches.
func (m *Model) agentOpen(v agentVariant, id, cwd string) tea.Cmd {
	if sess := m.mux().Get(id); sess != nil && sess.Live() {
		m.muxAttach(id)
		return nil
	}
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

	// flush pending edits before the agent starts: it may reach back into the
	// outline through the lflow CLI while it runs, and it must see what is on
	// screen here.
	_ = m.flushSync()

	argv := append([]string{v.bin}, v.args(s.SessionID)...)
	cols, rows := m.muxSize()
	sess := m.mux().Start(id, v.id, m.agentTitle(id, v, s), s.Cwd, argv, cols, rows)
	m.muxAttach(id)
	return m.startAnim(waitAgentMux(sess))
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
	// A session STARTED from lflow has no name until the CLI gives it one, so the
	// first landing is where its identity is taken — the same snapshot attaching
	// makes, just at the only moment this chip has one to take. Every landing
	// after that leaves it alone: the CLI does not get to keep relabelling a node.
	if !s.Snapshot {
		if meta, ok := v.agentStoreSessionFor(s.SessionID); ok && meta.title != "" {
			s.Title, s.Snapshot = meta.title, true
			if s.Color == "" {
				s.Color = meta.color
			}
		}
	}
	m.agentSave(msg.id, s)
	delete(m.nodeStore(msg.id), "agentMeta") // the session moved - re-read it
	delete(m.nodeStore(msg.id), "agentTrace")
	m.refreshAgentChip(msg.id)
	if msg.err != nil {
		m.errorFlash(v.label + ": " + msg.err.Error())
		return
	}
	m.flash = v.id + " · session " + agentShortID(s.SessionID)
}

// agentAttach points a chip at an EXISTING session from the CLI's own store —
// the "I already have this conversation, file it here" path.
//
// This is the one moment the CLI's idea of the session's identity crosses into
// lflow: its name and color are COPIED here and the link is cut. A rename you
// made in lflow belonged to whatever session used to be here, so it goes with
// it — attaching is picking a session, not editing the one already attached.
func (m *Model) agentAttach(id string, sess agentStoreSession) {
	v, ok := agentVariantByID(sess.variant)
	if !ok {
		return
	}
	s := m.agentLoad(id)
	s.Variant, s.SessionID, s.Title = v.id, sess.id, sess.title
	s.Name, s.Color, s.Snapshot = "", sess.color, true
	if sess.cwd != "" {
		s.Cwd = sess.cwd
	}
	delete(m.nodeStore(id), "agentMeta")
	// A picker row already carries what the store says, but a caller that knows
	// only the session id must not end up with a nameless, default-colored chip:
	// whatever the row did not bring, the store is asked for once, here, while
	// this is still the moment the snapshot is being taken.
	if s.SessionID != "" && (s.Title == "" || s.Color == "") {
		meta := m.agentMeta(id, v, s)
		if s.Title == "" {
			s.Title = meta.title
		}
		if s.Color == "" {
			s.Color = meta.color
		}
	}
	if s.Cwd == "" {
		s.Cwd = processCWD()
	}
	m.agentSave(id, s)
	m.refreshAgentChip(id)
}

// agentRename sets (or clears, with "") the user's own name for a session — the
// chip's name, exactly like a link chip's. Clearing it falls back to the title
// imported when the session was picked.
//
// The rename stays HERE. lflow does not write names or colors into pi's or
// Claude Code's stores: their transcripts are their record of their own work,
// and a note taker that edits them is a note taker you cannot trust with them.
func (m *Model) agentRename(id, name string) {
	s := m.agentLoad(id)
	s.Name = strings.TrimSpace(name)
	m.agentSave(id, s)

	m.flash = "renamed here"
	m.refreshAgentChip(id)
	if v, ok := agentVariantByID(s.Variant); ok {
		m.publishAgentLook(id, v)
	}
}

// ── the chip ───────────────────────────────────────────────────────────────

// agentChipAtCaret returns the session chip the caret sits on (its anchor begins
// at the caret or ends exactly at it), like cmdChipAtCaret.
func (m *Model) agentChipAtCaret(cur *item) (database.Chip, bool) {
	return m.chipAtCaret(cur, chipKindAgent)
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
	m.flash = v.label + " · ⌥r runs · ⌥m messages · ⌥e edits"
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
// the user's own name for the session when it has one, else the title imported
// when the session was picked. A hosted
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

// hydrateAgentChips refreshes session chips and publishes Agent node colors from
// the CLI stores. It never writes the local session identity into synced rows.
func (m *Model) hydrateAgentChips() {
	for id, c := range m.chips {
		if c.Kind != chipKindAgent {
			continue
		}
		if v, ok := agentVariantByID(c.Value); ok {
			m.importAgentIdent(id, v)
			m.publishAgentLook(id, v)
		}
		m.refreshAgentChip(id)
	}
}

// importAgentIdent takes a chip's identity snapshot if it never had one — the
// chips filed before identity was a snapshot, which read the CLI's color live
// on every frame. Without this they would all revert to their agent's default
// color at once, which is a chip changing appearance for no reason the user did.
// It runs a single time per chip: Snapshot is set whether or not the store had
// anything to give.
func (m *Model) importAgentIdent(id string, v agentVariant) {
	s := m.agentLoad(id)
	if s.Snapshot || s.SessionID == "" {
		return
	}
	meta := m.agentMeta(id, v, s)
	if s.Title == "" {
		s.Title = meta.title
	}
	if s.Color == "" {
		s.Color = meta.color
	}
	s.Snapshot = true
	m.agentSave(id, s)
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

// The Agent node is RETIRED (2026-08-04): a session had two surfaces and the
// whole-row one was the worse of them, so the inline chip is the only one
// left. The type survives as internal — never offered by /type — so a node
// somebody still has typed this way keeps its own name and its own row
// instead of reading as an unknown type.
func init() {
	registerType(nodeType{
		key:            database.TypeAgent,
		label:          "Agent",
		internal:       true,
		inlineEditable: true,
	})
}
