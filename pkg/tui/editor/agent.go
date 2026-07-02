package editor

import (
	"context"
	"regexp"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lflow/lflow/pkg/tui/database"
	"github.com/lflow/lflow/pkg/tui/tag"
)

// The @mention agent surface (see pkg/tui/tag and docs/ARTIFACTS.md).
//
// Committing a node that mentions a configured agent (Enter) IS the send —
// the keyboard gesture stands in for Slack's send button, so nothing fires
// from mere typing; alt+r on the node re-sends the thread. The mentioned node
// becomes the thread root: its ancestors + subtree are the session's whole
// visible world, replies land beneath it as agent nodes (red ✦, text + chips
// only), and the session id persists so a later mention resumes the context.

// agentGlyph is the agent reply marker: a red ✦ (dim once the thread is done).
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

// threadRootFor resolves the thread a mention belongs to: the nearest ancestor
// (or the node itself) that already has a session with the agent — so a
// follow-up mention deeper in a thread continues the conversation instead of
// forking a new one.
func (m *Model) threadRootFor(it *item, ag tag.Agent) *item {
	for p := it; p != nil; p = p.parent {
		if _, ok, _ := database.GetThreadSession(m.db, p.uuid, ag.Name); ok {
			return p
		}
	}
	return it
}

// buildThread flattens the thread context: the root's ancestor chain (role
// "context", orientation only), then the root and its subtree depth-first.
// Mirrors expand at most once via the visited set, so a mirror pointing back
// at an ancestor can't loop the walk (the same guard the renderer uses).
func (m *Model) buildThread(root *item) []tag.ThreadNode {
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
			Type: it.typ, Role: role,
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
	agent  string
	ev     tag.Event
	ch     <-chan tag.Event
}

// agentStreamEndMsg fires when the event channel closes.
type agentStreamEndMsg struct{ thread string }

func waitAgentCmd(thread, agent string, ch <-chan tag.Event) tea.Cmd {
	return func() tea.Msg {
		ev, ok := <-ch
		if !ok {
			return agentStreamEndMsg{thread: thread}
		}
		return agentEvMsg{thread: thread, agent: agent, ev: ev, ch: ch}
	}
}

// sendThread ships the thread to the agent's session on an explicit gesture.
func (m *Model) sendThread(it *item, ag tag.Agent) tea.Cmd {
	root := m.threadRootFor(it, ag)
	if m.agentBusy == nil {
		m.agentBusy = map[string]bool{}
	}
	if m.agentBusy[root.uuid] {
		m.flash = "@" + ag.Name + " · already thinking on this thread"
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

	ch, err := client.Send(context.Background(), ag.Name, sessionID, m.buildThread(root))
	if err != nil {
		m.flash = "@" + ag.Name + " · " + err.Error()
		return nil
	}
	m.agentBusy[root.uuid] = true
	if sessionID != "" {
		m.touchSession(sessionID, root.uuid, ag.Name, "running")
	}
	m.flash = "@" + ag.Name + " · thinking…"
	return waitAgentCmd(root.uuid, ag.Name, ch)
}

// handleAgentEvent lands one event in the outline and re-arms the stream.
func (m *Model) handleAgentEvent(msg agentEvMsg) (tea.Model, tea.Cmd) {
	rearm := waitAgentCmd(msg.thread, msg.agent, msg.ch)
	switch msg.ev.Op {
	case "session":
		m.touchSession(msg.ev.ID, msg.thread, msg.agent, "running")
	case "message":
		m.appendAgentNode(msg.thread, msg.ev.Text)
	case "artifact":
		a := database.Artifact{
			Key: msg.ev.Key, Label: msg.ev.Label, Version: 1, Source: msg.ev.Source,
			CreatedBy: msg.agent, CreatedAt: time.Now().UnixNano(), Enabled: true,
		}
		if err := installArtifact(m.db, a); err != nil {
			m.appendAgentNode(msg.thread, "failed to install artifact "+a.Key+": "+err.Error())
		} else {
			m.flash = "artifact · installed " + a.Key + " — available in /type"
		}
	case "error":
		m.appendAgentNode(msg.thread, "error: "+msg.ev.Text)
		m.finishThread(msg.thread, msg.agent)
		return m, nil
	case "done":
		m.finishThread(msg.thread, msg.agent)
		return m, nil
	}
	return m, rearm
}

// appendAgentNode adds one agent reply as the thread root's last child.
func (m *Model) appendAgentNode(threadUUID, text string) {
	root := m.tree.byUUID[threadUUID]
	if root == nil {
		return
	}
	it, err := m.tree.newItem()
	if err != nil {
		return
	}
	it.typ = database.TypeAgent
	it.name = text
	it.parent = root
	root.children = append(root.children, it)
	root.collapsed = false
	// agent replies carry chips like any node: #tags and canonical dates in
	// the text become real chips via the same detection the backfill uses
	m.backfillName(it)
	m.unsaved = true
	m.refreshRows()
}

// finishThread clears the busy flag and parks the session.
func (m *Model) finishThread(threadUUID, agent string) {
	delete(m.agentBusy, threadUUID)
	if s, ok, _ := database.GetThreadSession(m.db, threadUUID, agent); ok {
		m.touchSession(s.ID, threadUUID, agent, "idle")
	}
	m.flash = "@" + agent + " · replied"
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

// mentionSendOnEnter fires the send when Enter commits a node with a fresh
// mention. Returns the send command and true when the Enter was consumed as
// "send" (no split happens — the reply arrives beneath this node).
func (m *Model) mentionSendOnEnter(cur *item) (tea.Cmd, bool) {
	if cur == nil || cur.name == "" || cur.typ == database.TypeAgent {
		return nil, false
	}
	ag, ok := m.mentionedAgent(expandAnchors(cur.name, m.chips))
	if !ok {
		return nil, false
	}
	if m.mentionSent == nil {
		m.mentionSent = map[string]bool{}
	}
	if m.mentionSent[cur.uuid] {
		return nil, false // already sent — Enter behaves normally; alt+r re-sends
	}
	m.mentionSent[cur.uuid] = true
	return m.sendThread(cur, ag), true
}
