// Package tag is the @mention agent subsystem — the Claude-Tag model brought
// to the outline: mentioning @AgentName in a node binds an agent session to
// that node, the node's subtree becomes the conversation thread, and the agent
// posts replies back as agent-type child nodes. See AGENTS.md.
//
// The package owns agent configuration, the session-facing Client interface,
// the websocket implementation that talks to a Pi coding-agent service, and
// the offline mock that stands in for it. The bridge lives inside the editor
// process: closing the editor pauses in-flight sessions (ids persist in the
// agent_sessions table, so a later mention resumes the remote context).
package tag

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
)

// ThreadNode is one node of the thread context sent to the agent: the thread
// root and its subtree depth-first. The agent sees its own level and below,
// never its ancestors or siblings elsewhere in the outline.
type ThreadNode struct {
	UUID  string `json:"uuid"`
	Depth int    `json:"depth"` // 0 = thread root
	Name  string `json:"name"`
	Type  string `json:"type"`
	Role  string `json:"role"`  // "user" | "agent"
	Asked bool   `json:"asked"` // the node this turn is about — replies target it
}

// Event is one message streamed back from the agent service.
type Event struct {
	Op   string `json:"op"`   // "session" | "message" | "artifact" | "done" | "error"
	ID   string `json:"id"`   // op=session: the assigned session id
	Text string `json:"text"` // op=message/error
	// Placement is where a message lands relative to the asked node — the two
	// Claude-Tag surfaces: "below" posts it like a message-board reply (next
	// sibling), "thread" nests it as the asked node's child. Default: thread.
	Placement string `json:"placement"`
	Key       string `json:"key"`    // op=artifact
	Label     string `json:"label"`  // op=artifact
	Source    string `json:"source"` // op=artifact: the JS program to install
}

// Client drives agent conversations. Send delivers the thread to the session
// (empty id = start a new one) and streams events until a done or error event
// closes the channel.
type Client interface {
	Send(ctx context.Context, agent, sessionID string, thread []ThreadNode) (<-chan Event, error)
}

// Agent is one configured @name.
type Agent struct {
	Name string `json:"name"`
	URL  string `json:"url"`  // websocket endpoint of the Pi service
	Mock bool   `json:"mock"` // true → the built-in offline mock serves this agent
}

// LoadAgents reads <configDir>/lflow/agents.json. With no file (or a broken
// one) the built-in mock Miso is registered so @mentions work out of the box.
func LoadAgents(configDir string) []Agent {
	// Miso defaults to a real backend (the local pi CLI); ClientFor falls back to
	// the mock when pi is not installed, so @mentions still work offline.
	fallback := []Agent{{Name: "Miso"}}
	b, err := os.ReadFile(filepath.Join(configDir, "lflow", "agents.json"))
	if err != nil {
		return fallback
	}
	var agents []Agent
	if err := json.Unmarshal(b, &agents); err != nil || len(agents) == 0 {
		return fallback
	}
	return agents
}

// ClientFor returns the client that serves the agent: the offline mock, the
// websocket bridge to a configured service, or the local pi CLI. A non-mock
// agent with no URL uses pi when it is installed, falling back to the mock so
// the feature still works with no agent backend at all.
func ClientFor(a Agent) Client {
	if a.Mock {
		return &MockClient{}
	}
	if a.URL != "" {
		return &WSClient{URL: a.URL}
	}
	if piAvailable() {
		return &PiClient{}
	}
	return &MockClient{}
}
