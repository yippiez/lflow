package editor

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lflow/lflow/pkg/tui/database"
	"github.com/lflow/lflow/pkg/utils"
	"github.com/lflow/lflow/pkg/utils/browser"
)

// The agentic coding SESSION: an outline node (one type per CLI — Pi, Claude
// Code, opencode) and an inline chip of the same thing, so a coding session can
// be organized ALONGSIDE the notes it belongs to instead of in a shell history
// somewhere.
//
// alt+r hands the terminal over: lflow suspends itself, the CLI takes the screen
// in the session's pinned working directory, and closing it drops straight back
// into the outline with the session's id adopted. A session is created on the
// first run and RESUMED on every later one, so a node is a durable handle on one
// conversation. alt+e shows that conversation — its tool calls, prompts and
// replies — read live out of the CLI's own store (agentstore.go). alt+o opens a
// REMOTE Claude Code session (a claude.ai/code link in the node's text) in the
// browser, since there is no local process to attach to.
//
// Each variant is themed as itself (its own glyph and color), and a session that
// carries its own color in the CLI's store wears that instead. alt+enter (or
// /complete) marks a session done: it goes muted gray and sinks below the live
// ones in /agents.
//
// This is a CORE woven type rather than a plugin (see AGENTS.md) because it is
// two surfaces at once: the node lives in the type registry, and the chip lives
// in the chip registry, /insert and the caret gestures — both core.
//
// WARNING (invariant): lflow never copies a conversation into the outline. The
// node stores only {variant, cwd, session id} in LOCAL node_output — never in the
// node row, never synced — and every title, transcript and color is read back out
// of the CLI's own store on demand.

// agentVariant declares one agentic coding CLI lflow can host a session for.
// Adding a CLI is one entry in agentVariants plus its registry line.
type agentVariant struct {
	id    string // short id stored in session data and chip values ("claude")
	key   string // the node type key (database.TypeAgent*)
	label string // /type + picker label
	bin   string // the CLI binary — also its cliDeps entry, so /type greys it when missing
	glyph string
	color string // /style color name; the variant's own theming

	// assignsID reports whether the CLI accepts a session id lflow chooses. When
	// it does, the first launch pins the id and every later launch resumes exactly
	// that conversation; when it does not, lflow ADOPTS the id the CLI minted by
	// looking for the session its run touched (see agentNewestSince).
	assignsID bool

	// args builds the CLI's argv for a launch. resume is true once the node has
	// opened the session at least once.
	args func(s agentSession, resume bool) []string

	// sessionRoots / transcriptRoots are the CLI's own store directories: where
	// sessions are discovered ("attach an existing session") and where a session's
	// records are read from. transcriptRoots defaults to sessionRoots.
	sessionRoots    func() []string
	transcriptRoots func() []string
	exts            []string // record file extensions, e.g. ".jsonl"
	// sessionPath narrows discovery to records under this path fragment, for a
	// store that keeps sessions and their messages as sibling trees.
	sessionPath string
	// registryRoots is where the CLI records its RUNNING sessions (Claude Code
	// writes one file per live session). It is what tells lflow a session's own
	// name, whether it is live, and whether it is hosted rather than local.
	registryRoots func() []string

	// webURL renders the browser URL for a session hosted on the web; "" for a
	// variant that has no remote surface.
	webURL func(id string) string
}

// agentVariants is the registry, in picker order.
var agentVariants = []agentVariant{
	{
		id: "claude", key: database.TypeAgentClaude, label: "Claude Code session",
		bin: "claude", glyph: "✳", color: "orange", assignsID: true,
		args: func(s agentSession, resume bool) []string {
			if resume {
				return []string{"--resume", s.SessionID}
			}
			// --session-id pins the conversation lflow will resume later
			return []string{"--session-id", s.SessionID}
		},
		sessionRoots:  func() []string { return homeStores(".claude/projects") },
		registryRoots: func() []string { return homeStores(".claude/sessions") },
		exts:          []string{".jsonl"},
		webURL:        func(id string) string { return "https://claude.ai/code/" + id },
	},
	{
		id: "pi", key: database.TypeAgentPi, label: "Pi session",
		bin: "pi", glyph: "π", color: "purple", assignsID: true,
		args: func(s agentSession, _ bool) []string {
			// pi's --session-id resumes an existing conversation and creates it when
			// missing (see pkg/agent/pi.go), so new and resume are one command line.
			return []string{"--session-id", s.SessionID}
		},
		sessionRoots: func() []string { return homeStores(".pi/agent/sessions", ".pi/sessions", "<data>/pi/sessions") },
		exts:         []string{".jsonl", ".json"},
	},
	{
		// ❯ is the closest plain-Unicode stand-in for opencode's terminal mark —
		// the shell prompt it lives at. One table entry to change if a better one
		// turns up.
		id: "opencode", key: database.TypeAgentOpencode, label: "Opencode session",
		bin: "opencode", glyph: "❯", color: "cyan", assignsID: false,
		args: func(s agentSession, resume bool) []string {
			switch {
			case resume && s.SessionID != "":
				return []string{"--session", s.SessionID}
			case resume:
				// the id was never adopted (an unreadable store): continue the most
				// recent session in this directory rather than starting a stranger
				return []string{"--continue"}
			}
			return nil
		},
		// opencode keeps sessions and their messages as sibling trees (and, in newer
		// layouts, one such pair per project), so discovery is pinned to the session
		// side while the transcript is read from the message side.
		sessionRoots:    func() []string { return homeStores("<data>/opencode/storage", "<data>/opencode/project") },
		transcriptRoots: func() []string { return homeStores("<data>/opencode/storage/message", "<data>/opencode/project") },
		exts:            []string{".json"},
		sessionPath:     "/session/",
	},
}

// agentVariantOf returns the variant for a node type key.
func agentVariantOf(typeKey string) (agentVariant, bool) {
	for _, v := range agentVariants {
		if v.key == typeKey {
			return v, true
		}
	}
	return agentVariant{}, false
}

// agentVariantByID returns the variant for a short id ("claude") — how a chip
// names its CLI.
func agentVariantByID(id string) (agentVariant, bool) {
	for _, v := range agentVariants {
		if v.id == id {
			return v, true
		}
	}
	return agentVariant{}, false
}

// isAgentType reports whether a node type is one of the session types.
func isAgentType(typeKey string) bool {
	_, ok := agentVariantOf(typeKey)
	return ok
}

func (v agentVariant) colorSGR() string { return styleColorCode[v.color] }

func (v agentVariant) transcriptDirs() []string {
	if v.transcriptRoots != nil {
		return v.transcriptRoots()
	}
	if v.sessionRoots != nil {
		return v.sessionRoots()
	}
	return nil
}

func (v agentVariant) sessionDirs() []string {
	if v.sessionRoots != nil {
		return v.sessionRoots()
	}
	return nil
}

// ── session data (local, node_output) ──────────────────────────────────────

// agentSession is what lflow stores for one session handle — the pointer, never
// the conversation. It lives in node_output keyed by the node uuid (or, for a
// chip, the chip id), which is LOCAL and never synced: a session id only means
// something on the machine whose CLI store holds it.
type agentSession struct {
	Variant   string `json:"variant"`
	Cwd       string `json:"cwd,omitempty"`
	SessionID string `json:"session,omitempty"`
	Runs      int    `json:"runs,omitempty"`
	OpenedAt  int64  `json:"opened,omitempty"` // unix seconds of the last open
	Title     string `json:"title,omitempty"`  // last title read from the CLI's store
	Remote    string `json:"remote,omitempty"` // a web session URL — opened in the browser
	// Name is the user's OWN name for this session, like a link chip's name: set
	// it and the row/chip reads that instead of whatever the CLI calls the
	// session. Empty = follow the CLI's live title.
	Name string `json:"name,omitempty"`
}

// agentLoad reads a handle's session data, memory-cached in the node store: the
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

// agentMeta reads the session's live identity out of the CLI's store: its title,
// and any color the CLI gave it. The read is gated twice — a short TTL, then the
// transcript's mtime — so a render loop stats a file at most every few seconds
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
	if path := agentTranscriptPath(v.transcriptDirs(), v.exts, s.SessionID); path != "" {
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
//	→ the title in the CLI's store — so a session renamed inside the CLI reads
//	  renamed here, in the row and in the chip
//	→ the last title seen → "session <id>".
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

// agentCloud reports whether a session is HOSTED rather than local: a
// claude.ai/code link on the node, or a registry entry whose entrypoint says the
// session was started from the web or a phone. A hosted session wears the cloud
// mark on its chip and opens in the browser.
func (m *Model) agentCloud(v agentVariant, s agentSession) bool {
	if s.Remote != "" {
		return true
	}
	reg, ok := m.agentRegOf(v, s.SessionID)
	return ok && reg.cloud
}

// agentLive reports whether the session's CLI is running right now.
func (m *Model) agentLive(v agentVariant, s agentSession) bool {
	reg, ok := m.agentRegOf(v, s.SessionID)
	return ok && reg.live
}

// agentColorFor is the color a session is drawn in: the one the CLI stamped on
// the session when it has one, else the variant's own.
func (m *Model) agentColorFor(id string, v agentVariant, s agentSession) string {
	if meta := m.agentMeta(id, v, s); meta.color != "" {
		return meta.color
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

// ── the node type ──────────────────────────────────────────────────────────

// agentNodeType builds the registry descriptor for one variant. All three
// session types are the same node wearing different colors, so they are built
// from the variant table instead of hand-written three times.
func agentNodeType(v agentVariant) nodeType {
	return nodeType{
		key: v.key, label: v.label, inlineEditable: true,
		cliDeps:      []string{v.bin},
		glyph:        func(it *item) (string, string) { return v.glyph, agentGlyphColor(v, it) },
		fill:         func(it *item) string { return agentFillOf(v, it) },
		bodyTail:     func(it *item) string { return agentLookOf(v, it).tail },
		run:          func(m *Model, it *item) tea.Cmd { return m.agentRunNode(v, it) },
		view:         agentView{},
		openHost:     func(m *Model, it *item) tea.Cmd { return m.agentOpenRemote(v, it) },
		flashActions: func(m *Model, it *item) []flashAction { return agentFlashActions(v) },
		toContextM:   func(m *Model, it *item) contextXML { return m.agentToContext(v, it) },
	}
}

// agentLook is a session's render-time look: the color it draws in and the dim
// status that trails its title. The inline render path is deliberately Model-less
// (renderBody takes an item, see registry.go), while a session's look comes from
// its LOCAL data and its CLI's store — so the Model PUBLISHES each handle's look
// here and the hooks above read it, exactly like the tag-color and paint maps the
// render path already reads. A handle with nothing published falls back to the
// variant's own theming.
type agentLook struct{ color, tail string }

var agentLooks = map[string]agentLook{}

// agentLookOf returns the published look for an item, defaulting to the plain
// variant color (a session that has never been refreshed still reads as itself).
func agentLookOf(v agentVariant, it *item) agentLook {
	if it != nil {
		if l, ok := agentLooks[it.uuid]; ok {
			if l.color == "" {
				l.color = v.colorSGR()
			}
			return l
		}
	}
	return agentLook{color: v.colorSGR()}
}

// publishAgentLook recomputes one handle's look from its session data. Cheap: the
// session data is memory-cached and the CLI store is only re-read when the
// transcript's mtime moved (see agentMeta).
func (m *Model) publishAgentLook(id string, v agentVariant) agentLook {
	s := m.agentLoad(id)
	l := agentLook{color: m.agentColorFor(id, v, s)}

	// A session ROW is the chip's pill plus MUTED GRAY info to its right: what
	// this session is at a glance. The rest — the branch, the model, the tokens,
	// every tool call — belongs to the expanded panel (alt+e). See
	// agentBandContent.
	var parts []string
	if name := m.agentUnnamedRow(id, v, s); name != "" {
		parts = append(parts, name)
	}
	if m.agentCloud(v, s) {
		parts = append(parts, glyphCloud+" hosted")
	}
	if m.agentLive(v, s) {
		parts = append(parts, "live")
	}
	if s.Cwd != "" {
		parts = append(parts, tildePath(s.Cwd))
	}
	switch {
	case s.OpenedAt > 0:
		parts = append(parts, relTime(s.OpenedAt))
	case s.SessionID == "" && s.Remote == "":
		parts = append(parts, "⌥r starts it")
	}
	if len(parts) > 0 {
		l.tail = cDim + strings.Join(parts, " · ") + cReset
	}
	agentLooks[id] = l
	return l
}

// agentUnnamedRow returns the session's own name when the NODE has no text of
// its own — so a row a user never titled still reads as the session it holds,
// and a titled row is left alone.
func (m *Model) agentUnnamedRow(id string, v agentVariant, s agentSession) string {
	if s.SessionID == "" && s.Name == "" {
		return ""
	}
	if it, ok := m.tree.byUUID[id]; ok && strings.TrimSpace(it.name) != "" {
		return ""
	}
	return m.agentTitle(id, v, s)
}

// refreshAgentLooks republishes the look of every session on screen — nodes and
// inline chips alike. Called at the end of refreshRows, so a row always draws
// from current data without the render path ever reaching for a Model. Chip
// labels ride along, which is what keeps a chip's text in step with a session
// renamed inside its CLI.
func (m *Model) refreshAgentLooks() {
	for _, r := range m.rows {
		if r.it == nil {
			continue
		}
		if v, ok := agentVariantOf(r.it.typ); ok {
			m.publishAgentLook(r.it.uuid, v)
		}
		if !hasAnchor(r.it.name) {
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

// agentFillOf is the color a session ROW is filled with — the same pill the
// chip wears, so one session reads the same wherever it appears. A done session
// fills muted gray instead, which is what makes finished work read as settled.
func agentFillOf(v agentVariant, it *item) string {
	if it != nil && (it.completedAt > 0 || underCompleted(it)) {
		return cDim
	}
	return agentLookOf(v, it).color
}

// agentGlyphColor keeps a done session's glyph as quiet as its row.
func agentGlyphColor(v agentVariant, it *item) string {
	if it != nil && (it.completedAt > 0 || underCompleted(it)) {
		return cDim
	}
	return agentLookOf(v, it).color
}

func agentFlashActions(v agentVariant) []flashAction {
	return []flashAction{
		{verb: "open", color: cGreen, do: func(m *Model, it *item) tea.Cmd { return m.agentRunNode(v, it) }},
		{verb: "session", color: cCyan, do: flashExpandDo},
	}
}

// agentToContext gives a session node a typed element: the CLI, whether the work
// is done, and the directory it runs in. The conversation itself is never
// context — it lives in the CLI's store, not the outline.
func (m *Model) agentToContext(v agentVariant, it *item) contextXML {
	attrs := `agent="` + v.id + `"`
	if it != nil && it.completedAt > 0 {
		attrs += ` done="true"`
	}
	if it != nil {
		if s := m.agentLoad(it.uuid); s.Cwd != "" {
			attrs += ` cwd="` + s.Cwd + `"`
		}
	}
	return contextXML{tag: "agent-session", attrs: attrs}
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

// ── opening a session ──────────────────────────────────────────────────────

// agentClosedMsg lands when the CLI lflow suspended for has exited.
type agentClosedMsg struct {
	id      string // node uuid or chip id — the session handle's storage key
	variant string
	since   int64 // unix seconds the launch started at (adopting a minted id)
	err     error
}

// agentRunNode is alt+r on a session node: open (or resume) the session.
func (m *Model) agentRunNode(v agentVariant, it *item) tea.Cmd {
	if it == nil {
		return nil
	}
	// a claude.ai/code link in the node's text makes this a REMOTE session: there
	// is no local process to attach to, so opening it means the browser
	if url := agentRemoteURL(expandAnchors(it.name, m.chips)); url != "" {
		return m.agentOpenURL(it.uuid, url)
	}
	return m.agentOpen(v, it.uuid, m.agentNodeCwd(it))
}

// agentNodeCwd is the directory a node's session runs in: its path chip when it
// carries one (pin a session to a repo by dropping the folder in the row), else
// the editor's own working directory.
func (m *Model) agentNodeCwd(it *item) string {
	if p := (nodeRef{m: m, it: it}).PathChip(); p != "" {
		if st, err := os.Stat(p); err == nil {
			if st.IsDir() {
				return p
			}
			return filepath.Dir(p)
		}
	}
	return processCWD()
}

// agentOpen suspends lflow and hands the terminal to the CLI, creating the
// session on the first open and resuming it on every later one. The editor is
// released for the whole run and redraws when the CLI exits.
func (m *Model) agentOpen(v agentVariant, id, cwd string) tea.Cmd {
	if !m.depOK(v.bin) {
		m.flash = "Missing dependency: " + v.bin
		return nil
	}
	s := m.agentLoad(id)
	s.Variant = v.id
	if s.Cwd == "" {
		s.Cwd = cwd
	}
	if s.Remote != "" {
		return m.agentOpenURL(id, s.Remote)
	}
	// a session pinned to a directory that is gone (an attached session from
	// another machine, a moved repo) refuses to open rather than silently running
	// the agent somewhere else — which repo it works in is the whole point
	if s.Cwd != "" {
		if st, err := os.Stat(s.Cwd); err != nil || !st.IsDir() {
			m.flash = v.id + ": " + tildePath(s.Cwd) + " is gone · the session is pinned to it"
			return nil
		}
	}
	resume := s.Runs > 0 || s.SessionID != ""
	if v.assignsID && s.SessionID == "" {
		uuid, err := utils.GenerateUUID()
		if err != nil {
			m.flash = "agent: " + err.Error()
			return nil
		}
		s.SessionID, resume = uuid, false
	}
	m.agentSave(id, s)

	// flush pending edits before handing the terminal over: the agent may reach
	// back into the outline through the lflow CLI while it runs, and it must see
	// what is on screen here.
	_ = m.flushSync()

	c := exec.Command(v.bin, v.args(s, resume)...)
	if s.Cwd != "" {
		c.Dir = s.Cwd
	}
	since := time.Now().Unix()
	// tea.ExecProcess releases the terminal for the whole run: the CLI owns the
	// screen, and the editor repaints from scratch on return.
	return tea.ExecProcess(c, func(err error) tea.Msg {
		return agentClosedMsg{id: id, variant: v.id, since: since, err: err}
	})
}

// handleAgentClosed lands the CLI's exit: the session's run bookkeeping, and —
// for a CLI that names its own sessions — the id it just minted.
func (m *Model) handleAgentClosed(msg agentClosedMsg) {
	v, ok := agentVariantByID(msg.variant)
	if !ok {
		return
	}
	s := m.agentLoad(msg.id)
	s.Variant = v.id
	s.Runs++
	s.OpenedAt = time.Now().Unix()
	if s.SessionID == "" {
		if id := agentNewestSince(v.transcriptDirs(), v.exts, time.Unix(msg.since, 0)); id != "" {
			s.SessionID = id
		}
	}
	if s.SessionID != "" {
		if meta := agentReadMeta(v.id, agentTranscriptPath(v.transcriptDirs(), v.exts, s.SessionID)); meta.title != "" {
			s.Title = meta.title
		}
	}
	m.agentSave(msg.id, s)
	delete(m.nodeStore(msg.id), "agentMeta") // the transcript grew — re-read it
	delete(m.nodeStore(msg.id), "agentEntries")
	m.refreshAgentChip(msg.id)

	switch {
	case msg.err != nil:
		m.flash = v.label + ": " + msg.err.Error()
	case s.SessionID != "":
		m.flash = v.id + " · session " + agentShortID(s.SessionID) + " · ⌥e shows it"
	default:
		m.flash = v.id + " · session closed"
	}
}

// agentAttach points a session handle at an EXISTING session from the CLI's own
// store — the "I already have this conversation, file it here" path.
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
	if !sess.updated.IsZero() {
		s.OpenedAt = sess.updated.Unix()
	}
	s.Runs++ // an attached session already exists: the next open resumes it
	m.agentSave(id, s)
	delete(m.nodeStore(id), "agentMeta")
	delete(m.nodeStore(id), "agentEntries")
	m.refreshAgentChip(id)
}

// ── remote (browser) sessions ──────────────────────────────────────────────

// agentRemoteURL finds a hosted Claude Code session link in a node's text. A
// session opened on the web has no local process to attach to, so lflow treats
// such a node as a handle on the BROWSER: alt+r and alt+o both open the link.
func agentRemoteURL(text string) string {
	for _, f := range strings.FieldsFunc(text, func(r rune) bool {
		return r == ' ' || r == '\t' || r == '\n' || r == '(' || r == ')' || r == '[' || r == ']' || r == '<' || r == '>'
	}) {
		f = strings.TrimRight(f, ".,;")
		low := strings.ToLower(f)
		if !strings.Contains(low, "/code/") {
			continue
		}
		if strings.Contains(low, "claude.ai") || strings.Contains(low, "claude.com") {
			return f
		}
	}
	return ""
}

// agentOpenRemote is alt+o on a session node: open its hosted session in the
// browser. A purely local session says so rather than opening nothing.
func (m *Model) agentOpenRemote(v agentVariant, it *item) tea.Cmd {
	if it == nil {
		return nil
	}
	url := agentRemoteURL(expandAnchors(it.name, m.chips))
	if url == "" {
		s := m.agentLoad(it.uuid)
		url = s.Remote
		if url == "" && v.webURL != nil && s.SessionID != "" && s.Runs == 0 {
			// never opened locally: the id can only be a hosted one
			url = v.webURL(s.SessionID)
		}
	}
	if url == "" {
		m.flash = "local session · ⌥r opens it in " + v.bin
		return nil
	}
	return m.agentOpenURL(it.uuid, url)
}

// agentOpenURL opens a hosted session in the host's browser, recording it as the
// handle's remote target so later opens go straight there.
func (m *Model) agentOpenURL(id, url string) tea.Cmd {
	s := m.agentLoad(id)
	if s.Remote != url {
		s.Remote = url
		if s.SessionID == "" {
			s.SessionID = agentURLSessionID(url)
		}
		m.agentSave(id, s)
	}
	return func() tea.Msg {
		err := browser.Open(url)
		return agentOpenedMsg{url: url, err: err}
	}
}

// agentURLSessionID pulls the session id out of a claude.ai/code/<id> link.
func agentURLSessionID(url string) string {
	i := strings.LastIndex(url, "/code/")
	if i < 0 {
		return ""
	}
	id := strings.TrimSuffix(url[i+len("/code/"):], "/")
	if j := strings.IndexAny(id, "?#"); j >= 0 {
		id = id[:j]
	}
	return id
}

// agentOpenedMsg reports a browser open back to Update.
type agentOpenedMsg struct {
	url string
	err error
}

// ── the agent chip ─────────────────────────────────────────────────────────

// agentChipAtCaret returns the agent chip the caret sits on (its anchor begins
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

// insertAgentChip splices a session chip for a variant at the caret. attach is
// an existing session to point it at (zero value = a fresh session, created on
// the first alt+r).
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
		m.agentSave(id, agentSession{Variant: v.id, Cwd: m.agentNodeCwd(cur)})
	}
	m.insertLiteralAt(cur, m.caret, anchor)
	m.refreshAgentChip(id)
	m.flash = v.label + " chip · ⌥r opens it · ⌥e shows it"
}

// anchorChipID recovers the chip id from an anchor built by createChip.
func anchorChipID(anchor string) string {
	return strings.Trim(anchor, string(chipSentinel))
}

// runAgentChip is alt+r on a session chip: the same open/resume as a node.
func (m *Model) runAgentChip(c database.Chip) tea.Cmd {
	v, ok := agentVariantByID(c.Value)
	if !ok {
		m.flash = "unknown agent: " + c.Value
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
// title, so a session renamed inside its CLI reads renamed here too. A hosted
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
	if s.Name != "" || s.SessionID != "" || s.Remote != "" {
		label = clipStr(m.agentTitle(id, v, s), 34)
	}
	if m.agentCloud(v, s) {
		label += " " + glyphCloud // a hosted session says so on the chip itself
	}
	c.Label = label
	m.chips[id] = c
}

// agentRename sets (or clears, with "") the user's own name for a session — the
// chip's name, exactly like a link chip's. Clearing it hands the name back to
// the CLI's live title.
func (m *Model) agentRename(id, name string) {
	s := m.agentLoad(id)
	s.Name = strings.TrimSpace(name)
	m.agentSave(id, s)
	delete(m.nodeStore(id), "agentMeta")
	m.refreshAgentChip(id)
	if v, ok := agentVariantByID(s.Variant); ok {
		m.publishAgentLook(id, v)
	}
}

// hydrateAgentChips refreshes every session chip's label from the CLI stores.
// Called wherever hydrateCmdPreviews is — on open and after an aux reload — so a
// reopened editor shows current session titles without touching the chip rows.
func (m *Model) hydrateAgentChips() {
	for id, c := range m.chips {
		if c.Kind == chipKindAgent {
			m.refreshAgentChip(id)
		}
	}
}

// agentChipColor is a session chip's color: the session's own when its CLI gave
// it one, else the variant's.
func (m *Model) agentChipColor(c database.Chip) string {
	v, ok := agentVariantByID(c.Value)
	if !ok {
		return cDim
	}
	return m.agentColorFor(c.ID, v, m.agentLoad(c.ID))
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
