package editor

import (
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lflow/lflow/pkg/tui/database"
	"github.com/lflow/lflow/pkg/utils"
	"github.com/lflow/lflow/pkg/utils/browser"
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
	bin   string // the CLI binary; "" = a WEB service with no terminal to hand over
	glyph string // the agent's mark, worn by every chip of this variant
	color string // /style color name; the variant's own theming

	// assignsID reports whether the CLI accepts a session id lflow chooses. When
	// it does, the first launch pins the id and every later launch resumes exactly
	// that conversation; when it does not, lflow ADOPTS the id the CLI minted by
	// looking for the session its run touched (see agentNewestSince).
	assignsID bool

	// args builds the CLI's argv for a launch. resume is true once the chip has
	// opened the session at least once.
	args func(s agentSession, resume bool) []string

	// sessionRoots is where the CLI keeps its sessions: what the "attach an
	// existing session" picker lists and where a session's name is read from.
	sessionRoots func() []string
	exts         []string // record file extensions, e.g. ".jsonl"
	// sessionPath narrows discovery to records under this path fragment, for a
	// store that keeps sessions and their messages as sibling trees.
	sessionPath string
	// registryRoots is where the CLI records its RUNNING sessions (Claude Code
	// writes one file per live session). It is what tells lflow a session's own
	// name, whether it is live, and whether it is hosted rather than local.
	registryRoots func() []string

	// The web side of an agent, for the ones with a chat interface: webURL renders
	// a session's own page, webNew opens a fresh chat, and webHosts are the
	// fragments that identify one of this service's session links in a row's
	// text — paste a conversation URL beside a chip and the chip adopts it. A CLI
	// with no web surface leaves them empty; a service with no CLI (bin "") lives
	// here entirely, and ⌥r opens the browser instead of suspending lflow.
	//
	// webAny is for a SELF-HOSTED service, whose links wear whatever host you
	// happen to serve it on: there is no fragment to look for, so it recognizes a
	// session link by its SHAPE instead.
	webURL   func(id string) string
	webNew   string
	webHosts []string
	webAny   func(url string) bool
}

// webOnly reports a service lflow can only reach through the browser.
func (v agentVariant) webOnly() bool { return v.bin == "" }

// agentVariants is the registry, in picker order: the CLIs lflow can hand the
// terminal to, then the services it can only open in a browser.
//
// Each glyph is the closest plain Unicode to the agent's own mark: Claude Code's
// asterisk, Codex's blossom, Gemini's four-pointed spark, an empty set for Grok,
// small-caps ᴘɪ and ᴛ3, a squared O for opencode, and ChatGPT's ᴋɴᴏᴛ (U+168D0,
// the mark that shape is borrowed from). No emoji — a chip has to paint in one
// cell — but three of them ask more of a font than the rest: U+168D0 needs Bamum
// coverage (a Nerd Font or Noto Sans Bamum), and ᴘ / ᴛ need the phonetic block,
// so a terminal font without them draws a box. Everything else here is plain BMP.
//
// Two variants share purple: Pi and T3 Code, which is the palette's doing — it
// carries eight swatches, gray is the DONE fill and cannot theme a live agent,
// and there are more agents than the seven that leaves. The marks are what tell
// two chips apart; swap either entry's color if the pair reads as one.
var agentVariants = []agentVariant{
	{
		id: "claude", label: "Claude Code", bin: "claude",
		glyph: "✳", color: "orange", assignsID: true,
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
		webNew:        "https://claude.ai/code",
		webHosts:      []string{"claude.ai/code/", "claude.com/code/"}, // one session, two domains
	},
	{
		id: "codex", label: "Codex", bin: "codex",
		glyph: "✺", color: "green", assignsID: false,
		args: func(s agentSession, resume bool) []string {
			// codex mints its own ids and resumes by subcommand: `codex resume <id>`
			if resume && s.SessionID != "" {
				return []string{"resume", s.SessionID}
			}
			if resume {
				return []string{"resume", "--last"}
			}
			return nil
		},
		sessionRoots: func() []string { return homeStores(".codex/sessions") },
		exts:         []string{".jsonl"},
		webURL:       func(id string) string { return "https://chatgpt.com/codex/tasks/" + id },
		webNew:       "https://chatgpt.com/codex",
		webHosts:     []string{"chatgpt.com/codex/"},
	},
	{
		id: "gemini", label: "Gemini CLI", bin: "gemini",
		glyph: "✦", color: "blue", assignsID: false,
		args: func(s agentSession, resume bool) []string {
			if resume && s.SessionID != "" {
				return []string{"--resume", s.SessionID}
			}
			if resume {
				return []string{"--resume"} // the most recent session in this directory
			}
			return nil
		},
		// gemini keeps one chat directory per project hash under ~/.gemini/tmp
		sessionRoots: func() []string { return homeStores(".gemini/tmp") },
		exts:         []string{".json"},
		sessionPath:  "/chats/",
		webURL:       func(id string) string { return "https://gemini.google.com/app/" + id },
		webNew:       "https://gemini.google.com/app",
		webHosts:     []string{"gemini.google.com/app/"},
	},
	{
		id: "grok", label: "Grok CLI", bin: "grok",
		glyph: "∅", color: "red", assignsID: false,
		args: func(s agentSession, resume bool) []string {
			switch {
			case resume && s.SessionID != "":
				return []string{"--resume", s.SessionID}
			case resume:
				return []string{"--continue"}
			}
			return nil
		},
		sessionRoots: func() []string { return homeStores(".grok/sessions", ".grok/history", "<data>/grok-cli/sessions") },
		exts:         []string{".json", ".jsonl"},
		webURL:       func(id string) string { return "https://grok.com/chat/" + id },
		webNew:       "https://grok.com",
		webHosts:     []string{"grok.com/chat/"},
	},
	{
		id: "pi", label: "Pi", bin: "pi",
		glyph: "ᴘɪ", color: "purple", assignsID: true,
		args: func(s agentSession, _ bool) []string {
			// pi's --session-id resumes an existing conversation and creates it when
			// missing (see pkg/agent/pi.go), so new and resume are one command line.
			return []string{"--session-id", s.SessionID}
		},
		sessionRoots: func() []string { return homeStores(".pi/agent/sessions", ".pi/sessions", "<data>/pi/sessions") },
		exts:         []string{".jsonl", ".json"},
	},
	{
		id: "opencode", label: "opencode", bin: "opencode",
		glyph: "▣", color: "yellow", assignsID: false,
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
		// side.
		sessionRoots: func() []string { return homeStores("<data>/opencode/storage", "<data>/opencode/project") },
		exts:         []string{".json"},
		sessionPath:  "/session/",
	},
	{
		// A WEB service: there is no terminal to hand over, so ⌥r opens the chat in
		// the browser — a new one, or the conversation this chip was pointed at.
		id: "chatgpt", label: "ChatGPT (web)",
		glyph: "𖣐", color: "cyan", assignsID: false,
		webURL:   func(id string) string { return "https://chatgpt.com/c/" + id },
		webNew:   "https://chatgpt.com",
		webHosts: []string{"chatgpt.com/c/"},
	},
	{
		// A SELF-HOSTED web GUI, and the reason webAny exists: `t3` serves T3 Code
		// on your own machine (app.t3.codes only fronts a backend you paired), so
		// its links wear whatever host you run it on and there is no fragment to
		// look for. There is also no terminal to hand over — T3 Code drives Claude
		// Code, Codex and the rest itself — so a chip is a handle on the thread's
		// URL and ⌥r opens it in the browser.
		id: "t3code", label: "T3 Code (web)",
		glyph: "ᴛ3", color: "purple", assignsID: false,
		webNew: "https://app.t3.codes",
		webAny: t3ThreadURL,
	},
}

// t3ThreadURL reports a T3 Code thread link by its SHAPE, since the host is
// whatever you serve it on. What is constant is the route: a thread lives at
// /<environmentId>/<threadId> and nothing else does.
//
// A /pair link is deliberately NOT a thread. It carries a pairing token in its
// hash, and a chip must never become somewhere a credential is kept — the single
// path segment already excludes it, and it stays excluded on purpose.
func t3ThreadURL(u string) bool {
	rest, ok := strings.CutPrefix(u, "https://")
	if !ok {
		if rest, ok = strings.CutPrefix(u, "http://"); !ok {
			return false
		}
	}
	if i := strings.IndexAny(rest, "?#"); i >= 0 {
		rest = rest[:i]
	}
	segs := strings.Split(strings.TrimSuffix(rest, "/"), "/")
	if len(segs) != 3 || segs[0] == "" { // a host and exactly two path segments
		return false
	}
	return agentIDish(segs[1]) && agentIDish(segs[2])
}

// agentIDish reports a path segment that reads as a generated id rather than a
// word: long enough, alphanumeric with dashes, and carrying at least one digit.
func agentIDish(s string) bool {
	if len(s) < 8 {
		return false
	}
	digit := false
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9':
			digit = true
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r == '-', r == '_':
		default:
			return false
		}
	}
	return digit
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
		if !v.webOnly() {
			out = append(out, v.bin)
		}
	}
	return out
}

func (v agentVariant) colorSGR() string { return styleColorCode[v.color] }

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
	Runs      int    `json:"runs,omitempty"`
	OpenedAt  int64  `json:"opened,omitempty"` // unix seconds of the last open
	Title     string `json:"title,omitempty"`  // last name read from the CLI's store
	Remote    string `json:"remote,omitempty"` // a web session URL — opened in the browser
	// Name is the user's OWN name for this session, like a link chip's name: set
	// it and the chip reads that instead of whatever the CLI calls the session.
	// Empty = follow the CLI's live name.
	Name string `json:"name,omitempty"`
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

// agentCloud reports whether a session is HOSTED rather than local: a
// claude.ai/code link, or a registry entry whose entry point says the session was
// started from the web or a phone. A hosted session wears the cloud mark and
// opens in the browser.
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
	since   int64 // unix seconds the launch started at (adopting a minted id)
	err     error
}

// agentOpen suspends lflow and hands the terminal to the CLI, creating the
// session on the first open and resuming it on every later one. The editor is
// released for the whole run and redraws when the CLI exits.
func (m *Model) agentOpen(v agentVariant, id, cwd string) tea.Cmd {
	if v.webOnly() {
		// nothing to suspend into: the service lives in a browser tab
		return m.agentOpenURL(id, m.agentWebURL(v, m.agentLoad(id)))
	}
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
		if id := agentNewestSince(v.sessionDirs(), v.exts, time.Unix(msg.since, 0)); id != "" {
			s.SessionID = id
		}
	}
	if s.SessionID != "" {
		if meta := agentReadMeta(v.id, agentSessionPath(v.sessionDirs(), v.exts, s.SessionID)); meta.title != "" {
			s.Title = meta.title
		}
	}
	m.agentSave(msg.id, s)
	delete(m.nodeStore(msg.id), "agentMeta") // the session moved — re-read it
	m.refreshAgentChip(msg.id)

	switch {
	case msg.err != nil:
		m.flash = v.label + ": " + msg.err.Error()
	case s.SessionID != "":
		m.flash = v.id + " · session " + agentShortID(s.SessionID)
	default:
		m.flash = v.id + " · session closed"
	}
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
	if !sess.updated.IsZero() {
		s.OpenedAt = sess.updated.Unix()
	}
	s.Runs++ // an attached session already exists: the next open resumes it
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
	m.refreshAgentChip(id)
	if v, ok := agentVariantByID(s.Variant); ok {
		m.publishAgentLook(id, v)
	}
}

// ── remote (browser) sessions ──────────────────────────────────────────────

// agentWebURL is where a session opens in a browser: the link it was pointed at,
// then its own page once it has an id, then the service's fresh-chat page.
func (m *Model) agentWebURL(v agentVariant, s agentSession) string {
	if s.Remote != "" {
		return s.Remote
	}
	if v.webURL != nil && s.SessionID != "" {
		return v.webURL(s.SessionID)
	}
	return v.webNew
}

// agentRemoteURL finds a hosted session link in text. A session
// opened on the web has no local process to attach to, so lflow treats such a
// chip as a handle on the BROWSER.
func agentRemoteURL(text string, v agentVariant) string {
	if len(v.webHosts) == 0 && v.webAny == nil {
		return ""
	}
	for _, f := range strings.FieldsFunc(text, func(r rune) bool {
		return r == ' ' || r == '\t' || r == '\n' || r == '(' || r == ')' || r == '[' || r == ']' || r == '<' || r == '>'
	}) {
		f = strings.TrimRight(f, ".,;")
		low := strings.ToLower(f)
		for _, h := range v.webHosts {
			if strings.Contains(low, h) {
				return f
			}
		}
		if v.webAny != nil && v.webAny(low) {
			return f
		}
	}
	return ""
}

// agentOpenURL opens a hosted session in the host's browser, recording it as the
// handle's remote target so later opens go straight there.
func (m *Model) agentOpenURL(id, url string) tea.Cmd {
	if url == "" {
		m.flash = "no web session to open"
		return nil
	}
	s := m.agentLoad(id)
	if s.Remote != url {
		s.Remote = url
		if v, ok := agentVariantByID(s.Variant); ok && s.SessionID == "" {
			s.SessionID = agentURLSessionID(url, v)
		}
		m.agentSave(id, s)
	}
	return func() tea.Msg {
		err := browser.Open(url)
		return agentOpenedMsg{url: url, err: err}
	}
}

// agentURLSessionID pulls the session id out of a chat link — whatever follows
// the service's own path fragment (claude.ai/code/<id>, chatgpt.com/c/<id>, …).
// A self-hosted service has no such fragment, so the LAST path segment is the
// fallback: T3 Code's /<environmentId>/<threadId> ends in the thread.
func agentURLSessionID(url string, v agentVariant) string {
	low := strings.ToLower(url)
	frags := append(append([]string{}, v.webHosts...), "/code/", "/c/", "/chat/", "/app/")
	for _, h := range frags {
		if h == "" {
			continue
		}
		i := strings.LastIndex(low, h)
		if i < 0 {
			continue
		}
		if id := agentURLTail(url[i+len(h):]); id != "" {
			return id
		}
	}
	if v.webAny != nil && v.webAny(low) {
		if i := strings.LastIndex(strings.TrimSuffix(agentURLPath(url), "/"), "/"); i >= 0 {
			return agentURLTail(url[i+1:])
		}
	}
	return ""
}

// agentURLTail is the id in a URL remainder, with any query or fragment and a
// trailing slash cut away.
func agentURLTail(rest string) string {
	if j := strings.IndexAny(rest, "?#"); j >= 0 {
		rest = rest[:j]
	}
	return strings.TrimSuffix(rest, "/")
}

// agentURLPath is the URL with its query and fragment cut away.
func agentURLPath(url string) string {
	if j := strings.IndexAny(url, "?#"); j >= 0 {
		return url[:j]
	}
	return url
}

// agentOpenedMsg reports a browser open back to Update.
type agentOpenedMsg struct {
	url string
	err error
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

// insertAgentChip splices a session chip for a variant at the caret. attach is
// an existing session to point it at (zero value = a fresh session, created on
// the first ⌥r).
func (m *Model) insertAgentChip(cur *item, v agentVariant, attach agentStoreSession) {
	if cur == nil {
		return
	}
	anchor := m.createChip(chipKindAgent, v.id)
	if anchor == "" {
		return
	}
	id := anchorChipID(anchor)
	switch {
	case attach.id != "":
		m.agentAttach(id, attach)
	default:
		s := agentSession{Variant: v.id, Cwd: processCWD()}
		// a chat link already in this row is the session this chip points at
		if url := agentRemoteURL(expandAnchors(cur.name, m.chips), v); url != "" {
			s.Remote, s.SessionID = url, agentURLSessionID(url, v)
		}
		m.agentSave(id, s)
	}
	m.insertLiteralAt(cur, m.caret, anchor)
	m.publishAgentLook(id, v)
	m.refreshAgentChip(id)
	m.flash = v.label + " · ⌥r opens it · ⌥e shows it"
}

// anchorChipID recovers the chip id from an anchor built by createChip.
func anchorChipID(anchor string) string {
	return strings.Trim(anchor, string(chipSentinel))
}

// runAgentChip is ⌥r on a session chip: open, or resume, the session.
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
	if s.Name != "" || s.SessionID != "" || s.Remote != "" {
		label = clipStr(m.agentTitle(id, v, s), 34)
	}
	if m.agentCloud(v, s) {
		label += " " + glyphCloud // a hosted session says so on the chip itself
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
