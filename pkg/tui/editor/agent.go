package editor

import (
	"context"
	"regexp"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lflow/lflow/pkg/tui/database"
	"github.com/lflow/lflow/pkg/tui/tag"
)

// The @mention agent surface (see pkg/tui/tag and AGENTS.md).
//
// Two trigger rules, nothing else:
//  1. alt+r on the node that mentions a configured agent is the manual fire —
//     always; it starts the session or re-sends the thread as it reads now.
//  2. a committed change to a DESCENDANT of the mention (Enter, or the cursor
//     leaving the typed node — blurSendCheck) ships automatically.
// The mention node itself is the thread root: the session binds to it, so
// siblings and ancestors never trigger a turn or receive replies. Context per
// turn = the mention + everything beneath it, PLUS whatever is visible on
// screen (ambient, marked Screen) — nothing else; the agent searches the rest
// of the outline itself via the lflow CLI.

// agentGlyph is the agent reply marker: a red ✦ — the general AI sparkle mark for the
// default agent Pi. (When a second agent exists, the author will need to be
// recorded on the node so each agent can wear its own mark.)
func agentGlyph(it *item) (string, string) {
	return "✦", cRed
}

var mentionRe = regexp.MustCompile(`@([A-Za-z][A-Za-z0-9_-]*)`)

// mentionedAgent returns the configured agent mentioned in the text.
func (m *Model) mentionedAgent(text string) (tag.Agent, bool) {
	for _, match := range mentionRe.FindAllStringSubmatch(text, -1) {
		for _, a := range m.agents {
			if a.Name == match[1] {
				return a, true
			}
		}
	}
	return tag.Agent{}, false
}

// mentionsName reports whether the text mentions exactly this agent name.
func mentionsName(text, name string) bool {
	for _, match := range mentionRe.FindAllStringSubmatch(text, -1) {
		if match[1] == name {
			return true
		}
	}
	return false
}

// threadSessionAt reports whether a VALID session binds this node to the
// agent. Valid means the node still mentions the agent — a session lives on
// the @-chipped node and nowhere else, so rows left by the earlier
// parent-binding scheme (or a mention edited away) must not keep an invisible
// thread alive, silently swallowing every new node beneath them. Stale rows
// are deleted on sight: the DB self-heals as it is read.
func (m *Model) threadSessionAt(p *item, agentName string) bool {
	if _, ok, _ := database.GetThreadSession(m.db, p.uuid, agentName); !ok {
		return false
	}
	if mentionsName(expandAnchors(p.name, m.chips), agentName) {
		return true
	}
	_ = database.DeleteThreadSession(m.db, p.uuid, agentName)
	return false
}

// threadRootFor resolves the conversation a message belongs to: an existing
// session on the node or any ancestor wins (follow-ups continue it), and a
// fresh mention roots ITS OWN thread — the session binds to the mention node,
// so only the mention's descendants are ever in-thread. Siblings and parents
// stay outside: they neither trigger turns nor receive replies.
func (m *Model) threadRootFor(it *item, ag tag.Agent) *item {
	for p := it; p != nil; p = p.parent {
		if p.uuid == "" {
			break
		}
		if m.threadSessionAt(p, ag.Name) {
			return p
		}
	}
	return it
}

// buildThread flattens the thread context: the root (the mention node) and
// its subtree depth-first, then a Screen-marked section holding whatever ELSE
// is visible in the current window — ambient context, "what the user sees
// right now". Nothing above the mention is sent; anything else in the outline
// the agent fetches itself through the lflow CLI (see cliSystemPrompt).
// askedUUID marks the node this turn is about, so replies can
// target it. Mirrors expand at most once via the visited set, so a mirror
// pointing back at an ancestor can't loop the walk (the same guard the renderer
// uses).
func (m *Model) buildThread(root *item, askedUUID string) []tag.ThreadNode {
	var out []tag.ThreadNode

	roleOf := func(it *item) string {
		if it.typ == database.TypeAgent {
			return "agent"
		}
		return "user"
	}
	var walk func(it *item, depth int, seen map[*item]bool)
	walk = func(it *item, depth int, seen map[*item]bool) {
		out = append(out, tag.ThreadNode{
			UUID: it.uuid, Depth: depth, Name: expandAnchors(m.tree.displayName(it), m.chips),
			Type: it.typ, Role: roleOf(it), Asked: it.uuid == askedUUID,
		})
		tgt := m.tree.expandTarget(it)
		if tgt == nil || seen[tgt] {
			return
		}
		next := cloneSeen(seen)
		next[tgt] = true
		for _, c := range m.tree.childItems(it) {
			walk(c, depth+1, next)
		}
	}
	walk(root, 0, map[*item]bool{})

	// the screen section: every item visible in the window the last render drew
	// (m.screenRows) not already in the thread. Appended AFTER the thread so
	// consumers reading the thread structurally are undisturbed.
	sent := map[string]bool{}
	for _, n := range out {
		sent[n.UUID] = true
	}
	for _, sr := range m.screenRows {
		if sr.it == nil || sr.it.uuid == "" || sent[sr.it.uuid] {
			continue
		}
		sent[sr.it.uuid] = true
		out = append(out, tag.ThreadNode{
			UUID: sr.it.uuid, Depth: sr.depth, Name: expandAnchors(m.tree.displayName(sr.it), m.chips),
			Type: sr.it.typ, Role: roleOf(sr.it), Screen: true,
		})
	}
	return out
}

// screenRow is one item visible in the last rendered window, with its outline
// depth — what buildThread's Screen section is made of.
type screenRow struct {
	it    *item
	depth int
}

// agentEvMsg carries one streamed event into the update loop.
type agentEvMsg struct {
	thread string // thread root uuid
	asked  string // the node this turn is about — replies target it
	agent  string
	ev     tag.Event
	ch     <-chan tag.Event
}

// agentStreamEndMsg fires when the event channel closes.
type agentStreamEndMsg struct{ thread string }

func waitAgentCmd(thread, asked, agent string, ch <-chan tag.Event) tea.Cmd {
	return func() tea.Msg {
		ev, ok := <-ch
		if !ok {
			return agentStreamEndMsg{thread: thread}
		}
		return agentEvMsg{thread: thread, asked: asked, agent: agent, ev: ev, ch: ch}
	}
}

// sendThread ships the thread to the agent, launch-and-forget: every turn is
// a fresh agent fed the whole thread as it reads NOW (no remote session to
// drift from edited nodes). asked is the node this turn is about (the
// mention, or a detected follow-up question).
func (m *Model) sendThread(asked *item, ag tag.Agent) tea.Cmd {
	root := m.threadRootFor(asked, ag)
	if t := m.thread(root.uuid); t != nil && t.busy {
		return nil
	}
	m.agentErr = "" // a fresh attempt clears the last failure

	if m.tagClients == nil {
		m.tagClients = map[string]tag.Client{}
	}
	client, ok := m.tagClients[ag.Name]
	if !ok {
		c, err := tag.ClientFor(ag)
		if err != nil {
			m.agentErr = err.Error() // no backend for @Name — surfaced in the bar
			return nil
		}
		client = c
		m.tagClients[ag.Name] = client
	}

	// a cancelable context per turn so flash "stop" (or a re-send) can kill the
	// in-flight CLI — the process is scoped to this ctx all the way down.
	ctx, cancel := context.WithCancel(context.Background())
	ch, err := client.Send(ctx, ag.Name, m.buildThread(root, asked.uuid))
	if err != nil {
		cancel()
		m.agentErr = err.Error()
		return nil
	}
	t := m.ensureThread(root.uuid)
	t.cancel = cancel
	t.busy = true
	// record the LOCAL thread binding (node ↔ agent) — what makes follow-ups
	// inside this subtree reach the agent, across editor restarts too
	m.touchThread(root.uuid, ag.Name, "running")
	return waitAgentCmd(root.uuid, asked.uuid, ag.Name, ch)
}

// agentToolLine is the last tool call seen this turn — rendered as a muted band
// beneath the running mention (tool name colored, detail gray). Ephemeral: it
// never enters the outline or the DB, and clears when the turn ends.
type agentToolLine struct {
	name   string
	detail string
}

// agentThread is the per-uuid turn state for the @mention agent: all keyed by
// ONE uuid so a lifecycle event (finish/stop/stream-end) clears it together.
// busy/cancel/tool key on the thread ROOT uuid; sent keys on the committed
// node's uuid — they may be different nodes, but each field is independently
// keyed, so one map still holds both.
type agentThread struct {
	busy   bool          // a turn is in flight for this thread root
	cancel func()        // cancels the in-flight turn (flash "stop"); nil when idle
	tool   agentToolLine // the last tool call (live band under the running mention)
	sent   bool          // this node's mention already sent this session
}

// thread returns the existing thread state for a uuid, or nil — nil-safe for
// read/comma-ok sites (an absent uuid reads as "not busy, nothing sent").
func (m *Model) thread(uuid string) *agentThread { return m.threads[uuid] }

// ensureThread returns the thread state for a uuid, lazily creating m.threads
// and the entry (mirrors nodeStore). Every write site goes through here.
func (m *Model) ensureThread(uuid string) *agentThread {
	if m.threads == nil {
		m.threads = map[string]*agentThread{}
	}
	t := m.threads[uuid]
	if t == nil {
		t = &agentThread{}
		m.threads[uuid] = t
	}
	return t
}

// busyThreadCount reports how many threads have a turn in flight. NOT
// len(m.threads): a thread entry survives idle (it may only hold sent), so the
// bar must count busy==true.
func (m *Model) busyThreadCount() int {
	n := 0
	for _, t := range m.threads {
		if t.busy {
			n++
		}
	}
	return n
}

// handleAgentEvent lands one event in the outline and re-arms the stream.
func (m *Model) handleAgentEvent(msg agentEvMsg) (tea.Model, tea.Cmd) {
	rearm := waitAgentCmd(msg.thread, msg.asked, msg.agent, msg.ch)
	switch msg.ev.Op {
	case "tool":
		// live progress under the running mention — keep only the latest call.
		if msg.ev.Tool != "" {
			m.ensureThread(msg.thread).tool = agentToolLine{name: msg.ev.Tool, detail: msg.ev.Text}
		}
		return m, rearm
	case "thinking":
		// the model moved past its last tool (reasoning / answering) — drop the
		// tool line so the band falls back to "Thinking…" instead of freezing.
		if t := m.thread(msg.thread); t != nil {
			t.tool = agentToolLine{}
		}
		return m, rearm
	case "message":
		m.placeAgentNode(msg.thread, msg.asked, msg.ev.Text, msg.ev.Placement)
	case "artifact":
		// the offline mock's install path — a real agent writes the file into
		// the nodes dir itself and the after-turn reload picks it up
		if err := installNodeMod(msg.ev.Key, msg.ev.Source); err != nil {
			m.placeAgentNode(msg.thread, msg.asked, "failed to install node type "+msg.ev.Key+": "+err.Error(), "thread")
		}
	case "error":
		// an agent failure shows in the status bar (like thinking), not the
		// outline — it is transient state, not a note the user wrote or wants kept
		m.agentErr = msg.ev.Text
		m.finishThread(msg.thread, msg.agent)
		return m, nil
	case "done":
		m.finishThread(msg.thread, msg.agent)
		return m, nil
	}
	return m, rearm
}

// placeAgentNode lands one agent reply relative to the asked node — the two
// Claude-Tag surfaces: "thread" nests it as the asked node's child, "below"
// posts it message-board style as the next sibling. A missing asked node
// (e.g. deleted mid-turn) falls back to the thread root's children.
func (m *Model) placeAgentNode(threadUUID, askedUUID, text, placement string) {
	asked := m.tree.byUUID[askedUUID]
	if asked == nil {
		asked = m.tree.byUUID[threadUUID]
		placement = "thread"
	}
	if asked == nil {
		return
	}
	it, err := m.tree.newItem()
	if err != nil {
		return
	}
	it.typ = database.TypeAgent
	it.name = m.chipifyAgentText(text)

	// a reply to the thread root itself always nests — "below" would land it
	// outside the mention's subtree, beyond the session's reach
	if asked.uuid == threadUUID {
		placement = "thread"
	}
	if placement == "below" && asked.parent != nil {
		it.parent = asked.parent
		idx := indexOf(asked)
		sibs := asked.parent.children
		asked.parent.children = append(sibs, nil)
		copy(asked.parent.children[idx+2:], asked.parent.children[idx+1:])
		asked.parent.children[idx+1] = it
	} else {
		it.parent = asked
		asked.children = append(asked.children, it)
		asked.collapsed = false
	}
	// agent replies carry chips like any node: spoken {{…}} tokens converted
	// above, and #tags / canonical dates via the same detection the backfill
	// uses. Skip the backfill once anchors exist — its span detection is not
	// anchor-aware; a skipped plain #tag still renders via inlineSpans.
	if !hasAnchor(it.name) {
		m.backfillName(it)
	}
	m.unsaved = true

	// a reply arrives asynchronously: re-anchor the cursor to the ITEM it was
	// on, or an insertion above it shifts every row and typing lands on the
	// wrong node
	var curIt, curCtx *item
	if m.cursor >= 0 && m.cursor < len(m.rows) {
		curIt, curCtx = m.rows[m.cursor].it, m.rows[m.cursor].ctx
	}
	m.refreshRows()
	if curIt != nil {
		if r := m.findRow(curIt, curCtx); r >= 0 {
			m.cursor = r
		}
	}
}

// agentChipRe matches the {{kind:value}} chip tokens an agent may speak — see
// cliSystemPrompt in pkg/tui/tag. A link value may carry a "label|" prefix.
var agentChipRe = regexp.MustCompile(`\{\{(cmd|path|link|tag|date):([^{}]+)\}\}`)

// chipifyAgentText converts a reply's chip tokens into real chips (anchor in
// the text + chip record), so a spoken {{cmd:…}} lands as the same runnable
// chip the user would have typed. Unknown kinds stay literal text.
func (m *Model) chipifyAgentText(text string) string {
	return agentChipRe.ReplaceAllStringFunc(text, func(tok string) string {
		sub := agentChipRe.FindStringSubmatch(tok)
		kind, val := sub[1], strings.TrimSpace(sub[2])
		if val == "" {
			return ""
		}
		switch kind {
		case chipKindLink:
			label, target := "", val
			if i := strings.IndexByte(val, '|'); i >= 0 {
				label, target = strings.TrimSpace(val[:i]), strings.TrimSpace(val[i+1:])
			}
			if target == "" {
				return ""
			}
			return m.createLabeledChip(chipKindLink, target, label)
		case chipKindTag:
			val = strings.TrimPrefix(val, "#")
		}
		if val == "" {
			return ""
		}
		return m.createChip(kind, val)
	})
}

// finishThread clears the busy flag and parks the thread. The agent may have
// created or edited mod files during its turn — reload them now.
func (m *Model) finishThread(threadUUID, agent string) {
	loadNodeMods()
	if t := m.thread(threadUUID); t != nil {
		t.busy = false
		t.tool = agentToolLine{} // the live tool band is gone once the turn ends
		if t.cancel != nil {
			t.cancel() // release the ctx (a no-op if the turn already ended)
			t.cancel = nil
		}
	}
	m.touchThread(threadUUID, agent, "idle")
}

// stopThread cancels an in-flight turn: killing the CLI closes the stream,
// which flows back as a "done" event and finishThread cleans up. Safe to call
// when nothing is running (no cancel recorded → no-op).
func (m *Model) stopThread(threadUUID, agentName string) {
	if t := m.thread(threadUUID); t != nil && t.cancel != nil {
		t.cancel()
		m.flash = "@" + agentName + " stopped"
	}
}

// touchThread upserts the LOCAL thread binding (agent_sessions row, id =
// thread root uuid). This is editor bookkeeping only — which subtree talks to
// which agent — never a remote session; the agent itself is launch-and-forget.
func (m *Model) touchThread(nodeUUID, agent, state string) {
	now := time.Now().UnixNano()
	s := database.AgentSession{
		ID: nodeUUID, NodeUUID: nodeUUID, Agent: agent, State: state,
		CreatedAt: now, UpdatedAt: now,
	}
	_ = s.Upsert(m.db)
}

// mentionSendOnEnter ships an untagged node committed inside an active
// thread for consideration (the agent answers only if it judges the node a
// question); Enter always continues to behave normally. Starting a session
// is deliberate: alt+r on the @mention node — Enter near a mention just
// edits text, wherever the caret sits.
func (m *Model) mentionSendOnEnter(cur *item) (tea.Cmd, bool) {
	if cur == nil || cur.name == "" || cur.typ == database.TypeAgent {
		return nil, false
	}
	if t := m.thread(cur.uuid); t != nil && t.sent {
		return nil, false // already sent — alt+r re-sends
	}
	if _, ok := m.mentionedAgent(expandAnchors(cur.name, m.chips)); ok {
		return nil, false // a fresh mention starts its session on alt+r only
	}

	// no mention: inside an active thread, committing still shows the node to
	// the session — discretionary, so a plain note stays unanswered
	if ag, ok := m.activeThreadAgent(cur); ok {
		m.ensureThread(cur.uuid).sent = true
		return m.sendThread(cur, ag), false
	}
	return nil, false
}

// blurSendCheck ships a typed-into node when the cursor leaves it: inside an
// active thread, typing a follow-up and moving away is as deliberate as Enter.
// Fresh @mentions are exempt — starting a conversation stays an explicit alt+r.
// Enter. Runs after every key (see Update); cheap when the cursor sat still.
func (m *Model) blurSendCheck() tea.Cmd {
	uuid := ""
	if cur := m.cursorItem(); cur != nil {
		uuid = cur.uuid
	}
	if uuid == m.focusUUID {
		return nil
	}
	prevUUID := m.focusUUID
	m.focusUUID = uuid
	if prevUUID == "" || prevUUID != m.typedUUID {
		return nil
	}
	m.typedUUID = ""
	prev := m.tree.byUUID[prevUUID]
	if prev == nil || prev.name == "" || prev.typ == database.TypeAgent {
		return nil
	}
	if t := m.thread(prevUUID); t != nil && t.sent {
		return nil // Enter (or an earlier blur) already sent it
	}
	if _, ok := m.mentionedAgent(expandAnchors(prev.name, m.chips)); ok {
		return nil // a fresh @mention sends on alt+r only
	}
	if ag, ok := m.activeThreadAgent(prev); ok {
		m.ensureThread(prevUUID).sent = true
		return m.sendThread(prev, ag)
	}
	return nil
}

// activeThreadAgent finds the agent whose session covers this node — the
// nearest ancestor (or the node itself) that is a session-bound @mention
// node. Nodes outside active threads reach no agent, ever.
func (m *Model) activeThreadAgent(it *item) (tag.Agent, bool) {
	for p := it; p != nil; p = p.parent {
		for _, a := range m.agents {
			if m.threadSessionAt(p, a.Name) {
				return a, true
			}
		}
	}
	return tag.Agent{}, false
}
