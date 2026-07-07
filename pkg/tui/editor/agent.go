package editor

import (
	"context"
	"regexp"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lflow/lflow/pkg/tui/database"
	"github.com/lflow/lflow/pkg/tui/tag"
)

// The @mention agent surface (see pkg/tui/tag and AGENTS.md).
//
// Committing a node that mentions a configured agent (Enter) IS the send —
// the keyboard gesture stands in for Slack's send button, so nothing fires
// from mere typing; alt+r on the node re-sends the thread. The mentioned node
// becomes the thread root: its ancestors + subtree are the session's whole
// visible world, replies land beneath it as agent nodes (red ✦, text + chips
// only), and the session id persists so a later mention resumes the context.

// agentGlyph is the agent reply marker: a red π — the agent's own initial as
// the glyph, like the heading digits. (When a second agent exists, the author
// will need to be recorded on the node so each agent can wear its own letter.)
func agentGlyph(it *item) (string, string) {
	return "π", cRed
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

// buildThread flattens the thread context: the root's ancestor chain (role
// "context", orientation only), then the root and its subtree depth-first.
// askedUUID marks the node this turn is about, so replies can target it.
// Mirrors expand at most once via the visited set, so a mirror pointing back
// at an ancestor can't loop the walk (the same guard the renderer uses).
func (m *Model) buildThread(root *item, askedUUID string) []tag.ThreadNode {
	var out []tag.ThreadNode

	var ancestors []*item
	for p := root.parent; p != nil; p = p.parent {
		ancestors = append([]*item{p}, ancestors...)
	}
	for i, a := range ancestors {
		out = append(out, tag.ThreadNode{
			UUID: a.uuid, Depth: i, Name: displayAnchors(m.tree.displayName(a), m.chips),
			Type: a.typ, Role: "context",
		})
	}

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

// sendThread ships the thread to the agent's session; asked is the node this
// turn is about (the mention, or a detected follow-up question).
func (m *Model) sendThread(asked *item, ag tag.Agent) tea.Cmd {
	root := m.threadRootFor(asked, ag)
	if m.agentBusy == nil {
		m.agentBusy = map[string]bool{}
	}
	if m.agentBusy[root.uuid] {
		return nil
	}

	sessionID := ""
	if s, ok, _ := database.GetThreadSession(m.db, root.uuid, ag.Name); ok {
		sessionID = s.ID
	}

	if m.tagClients == nil {
		m.tagClients = map[string]tag.Client{}
	}
	client, ok := m.tagClients[ag.Name]
	if !ok {
		client = tag.ClientFor(ag)
		m.tagClients[ag.Name] = client
	}

	ch, err := client.Send(context.Background(), ag.Name, sessionID, m.buildThread(root, asked.uuid))
	if err != nil {
		m.flash = "@" + ag.Name + " · " + err.Error()
		return nil
	}
	m.agentBusy[root.uuid] = true
	if sessionID != "" {
		m.touchSession(sessionID, root.uuid, ag.Name, "running")
	}
	return waitAgentCmd(root.uuid, asked.uuid, ag.Name, ch)
}

// handleAgentEvent lands one event in the outline and re-arms the stream.
func (m *Model) handleAgentEvent(msg agentEvMsg) (tea.Model, tea.Cmd) {
	rearm := waitAgentCmd(msg.thread, msg.asked, msg.agent, msg.ch)
	switch msg.ev.Op {
	case "session":
		m.touchSession(msg.ev.ID, msg.thread, msg.agent, "running")
	case "message":
		m.placeAgentNode(msg.thread, msg.asked, msg.ev.Text, msg.ev.Placement)
	case "artifact":
		a := database.Artifact{
			Key: msg.ev.Key, Label: msg.ev.Label, Version: 1, Source: msg.ev.Source,
			CreatedBy: msg.agent, CreatedAt: time.Now().UnixNano(), Enabled: true,
		}
		if err := installArtifact(m.db, a); err != nil {
			m.placeAgentNode(msg.thread, msg.asked, "failed to install artifact "+a.Key+": "+err.Error(), "thread")
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
	it.name = text

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
	// agent replies carry chips like any node: #tags and canonical dates in
	// the text become real chips via the same detection the backfill uses
	m.backfillName(it)
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

// finishThread clears the busy flag and parks the session.
func (m *Model) finishThread(threadUUID, agent string) {
	delete(m.agentBusy, threadUUID)
	if s, ok, _ := database.GetThreadSession(m.db, threadUUID, agent); ok {
		m.touchSession(s.ID, threadUUID, agent, "idle")
	}
}

// touchSession upserts the session row.
func (m *Model) touchSession(id, nodeUUID, agent, state string) {
	now := time.Now().UnixNano()
	s := database.AgentSession{
		ID: id, NodeUUID: nodeUUID, Agent: agent, State: state,
		CreatedAt: now, UpdatedAt: now,
	}
	_ = s.Upsert(m.db)
}

// mentionSendOnEnter fires the send when Enter commits a node the agent
// should see. Two cases, mirroring Claude Tag:
//   - a fresh @mention: Enter IS the send and is consumed (no split — the
//     reply arrives at this node);
//   - an untagged node inside a subtree that already has a session: it is
//     shipped for consideration (the agent answers only if it judges the node
//     a question) and Enter continues to behave normally.
func (m *Model) mentionSendOnEnter(cur *item) (tea.Cmd, bool) {
	if cur == nil || cur.name == "" || cur.typ == database.TypeAgent {
		return nil, false
	}
	if m.mentionSent == nil {
		m.mentionSent = map[string]bool{}
	}
	if m.mentionSent[cur.uuid] {
		return nil, false // already sent — Enter behaves normally; alt+r re-sends
	}

	if ag, ok := m.mentionedAgent(expandAnchors(cur.name, m.chips)); ok {
		m.mentionSent[cur.uuid] = true
		return m.sendThread(cur, ag), true
	}

	// no mention: inside an active thread, committing still shows the node to
	// the session — discretionary, so a plain note stays unanswered
	if ag, ok := m.activeThreadAgent(cur); ok {
		m.mentionSent[cur.uuid] = true
		return m.sendThread(cur, ag), false
	}
	return nil, false
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
