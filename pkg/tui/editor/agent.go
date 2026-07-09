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
// alt+r on a node that mentions a configured agent IS the send — a deliberate
// gesture, so nothing fires from mere typing or from Enter (which just edits
// text); alt+r again re-sends the thread. The mentioned node becomes the
// thread root: its ancestors + subtree are the session's whole visible world,
// replies land beneath it as agent nodes (red ✦, text + chips only), and the
// session id persists so a later mention resumes the context. Follow-ups
// inside a live thread still ship on Enter or on cursor-leave (blurSendCheck).

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

// threadRootFor resolves the conversation a message belongs to. The Slack
// shape: a NOTE is the channel — its children are the board messages, and a
// message's children are that message's reply thread. So an existing session
// on any ancestor wins (follow-ups continue it), and a fresh mention binds
// the session to the mention's PARENT: the note, covering the whole board.
func (m *Model) threadRootFor(it *item, ag tag.Agent) *item {
	for p := it; p != nil; p = p.parent {
		if _, ok, _ := database.GetThreadSession(m.db, p.uuid, ag.Name); ok {
			return p
		}
	}
	if it.parent != nil {
		return it.parent
	}
	return it
}

// buildThread flattens the thread context: the root and its subtree depth-first.
// The agent owns only its own subtree — the ancestor chain (lineage) is
// deliberately excluded, so a mention sees its node and everything below it,
// nothing above. askedUUID marks the node this turn is about, so replies can
// target it. Mirrors expand at most once via the visited set, so a mirror
// pointing back at an ancestor can't loop the walk (the same guard the renderer
// uses).
func (m *Model) buildThread(root *item, askedUUID string) []tag.ThreadNode {
	var out []tag.ThreadNode

	var walk func(it *item, depth int, seen map[*item]bool)
	walk = func(it *item, depth int, seen map[*item]bool) {
		role := "user"
		if it.typ == database.TypeAgent {
			role = "agent"
		}
		out = append(out, tag.ThreadNode{
			UUID: it.uuid, Depth: depth, Name: expandAnchors(m.tree.displayName(it), m.chips),
			Type: it.typ, Role: role, Asked: it.uuid == askedUUID,
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
	return out
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
	if m.agentBusy == nil {
		m.agentBusy = map[string]bool{}
	}
	if m.agentBusy[root.uuid] {
		return nil
	}

	if m.tagClients == nil {
		m.tagClients = map[string]tag.Client{}
	}
	client, ok := m.tagClients[ag.Name]
	if !ok {
		client = tag.ClientFor(ag)
		m.tagClients[ag.Name] = client
	}

	ch, err := client.Send(context.Background(), ag.Name, m.buildThread(root, asked.uuid))
	if err != nil {
		m.flash = "@" + ag.Name + " · " + err.Error()
		return nil
	}
	m.agentBusy[root.uuid] = true
	// record the LOCAL thread binding (node ↔ agent) — what makes follow-ups
	// inside this subtree reach the agent, across editor restarts too
	m.touchThread(root.uuid, ag.Name, "running")
	return waitAgentCmd(root.uuid, asked.uuid, ag.Name, ch)
}

// handleAgentEvent lands one event in the outline and re-arms the stream.
func (m *Model) handleAgentEvent(msg agentEvMsg) (tea.Model, tea.Cmd) {
	rearm := waitAgentCmd(msg.thread, msg.asked, msg.agent, msg.ch)
	switch msg.ev.Op {
	case "message":
		m.placeAgentNode(msg.thread, msg.asked, msg.ev.Text, msg.ev.Placement)
	case "artifact":
		// the offline mock's install path — a real agent writes the file into
		// the nodes dir itself and the after-turn reload picks it up
		if err := installNodeMod(msg.ev.Key, msg.ev.Source); err != nil {
			m.placeAgentNode(msg.thread, msg.asked, "failed to install node type "+msg.ev.Key+": "+err.Error(), "thread")
		}
	case "error":
		m.placeAgentNode(msg.thread, msg.asked, "error: "+msg.ev.Text, "thread")
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
	// wrong node (worst case the read-only reply itself)
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
// piSystemPrompt in pkg/tui/tag. A link value may carry a "label|" prefix.
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
	delete(m.agentBusy, threadUUID)
	m.touchThread(threadUUID, agent, "idle")
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
	if m.mentionSent == nil {
		m.mentionSent = map[string]bool{}
	}
	if m.mentionSent[cur.uuid] {
		return nil, false // already sent — alt+r re-sends
	}
	if _, ok := m.mentionedAgent(expandAnchors(cur.name, m.chips)); ok {
		return nil, false // a fresh mention starts its session on alt+r only
	}

	// no mention: inside an active thread, committing still shows the node to
	// the session — discretionary, so a plain note stays unanswered
	if ag, ok := m.activeThreadAgent(cur); ok {
		m.mentionSent[cur.uuid] = true
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
	if m.mentionSent == nil {
		m.mentionSent = map[string]bool{}
	}
	if m.mentionSent[prevUUID] {
		return nil // Enter (or an earlier blur) already sent it
	}
	if _, ok := m.mentionedAgent(expandAnchors(prev.name, m.chips)); ok {
		return nil // a fresh @mention sends on alt+r only
	}
	if ag, ok := m.activeThreadAgent(prev); ok {
		m.mentionSent[prevUUID] = true
		return m.sendThread(prev, ag)
	}
	return nil
}

// activeThreadAgent finds the agent whose session covers this node — the
// nearest ancestor (or the node itself) bound to a session. Nodes outside
// active threads reach no agent, ever.
func (m *Model) activeThreadAgent(it *item) (tag.Agent, bool) {
	for p := it; p != nil; p = p.parent {
		for _, a := range m.agents {
			if _, ok, _ := database.GetThreadSession(m.db, p.uuid, a.Name); ok {
				return a, true
			}
		}
	}
	return tag.Agent{}, false
}
