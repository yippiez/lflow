// Package tag is the @mention agent subsystem — the Claude-Tag model brought
// to the outline: mentioning @AgentName in a node binds an agent session to
// that node, the node's subtree becomes the conversation thread, and the agent
// posts replies back as agent-type child nodes. See AGENTS.md.
//
// The package owns agent configuration, the launch-and-forget Client
// interface, the websocket implementation that talks to a Pi coding-agent
// service, and the offline mock that stands in for it. Agents hold NO durable
// state: every turn re-sends the whole thread, so the outline itself is the
// conversation memory (edited past nodes are always honored). The editor
// keeps only a local thread binding (agent_sessions: node ↔ agent) so
// follow-ups inside a subtree keep reaching the agent across restarts.
package tag

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/lflow/lflow/pkg/agent"
)

// ThreadNode is one node of the context sent to the agent: the thread root
// (the mention node itself) and its subtree depth-first, then a Screen-marked
// section of whatever else is visible in the editor window — ambient context.
// Nothing above the mention is sent; the rest of the outline the agent
// searches via the lflow CLI.
type ThreadNode struct {
	UUID   string `json:"uuid"`
	Depth  int    `json:"depth"` // 0 = thread root
	Name   string `json:"name"`
	Type   string `json:"type"`
	Role   string `json:"role"`   // "user" | "agent"
	Asked  bool   `json:"asked"`  // the node this turn is about — replies target it
	Screen bool   `json:"screen"` // ambient "visible on screen" section, not the thread
}

// Event is one message streamed back from the agent service.
type Event struct {
	Op   string `json:"op"`   // "message" | "tool" | "artifact" | "done" | "error"
	Text string `json:"text"` // op=message/error; op=tool: the muted detail (file/cmd)
	Tool string `json:"tool"` // op=tool: the tool name (Read / Write / Edit …)
	// Placement is where a message lands relative to the asked node — the two
	// Claude-Tag surfaces: "below" posts it like a message-board reply (next
	// sibling), "thread" nests it as the asked node's child. Default: thread.
	Placement string `json:"placement"`
	// op=artifact installs a NodeMod — kept for the offline mock; a real
	// agent writes the <key>.js file into the mods dir itself and the editor
	// reloads the directory when the turn ends.
	Key    string `json:"key"`    // op=artifact: the node type key
	Source string `json:"source"` // op=artifact: the JS program to install
}

// Client runs agent turns, launch-and-forget: every Send is a FRESH agent fed
// the whole rendered thread, streaming events until done/error closes the
// channel. There is deliberately no session to resume — the outline is the
// user's and changes between turns (nodes edited, moved, deleted), so any
// remembered context would drift from what the thread actually says now. The
// thread root's uuid (thread[0].UUID) is the stable conversation identity for
// anything that needs one.
type Client interface {
	Send(ctx context.Context, agent string, thread []ThreadNode) (<-chan Event, error)
}

// Agent is one configured @name.
type Agent struct {
	Name string `json:"name"`
	URL  string `json:"url"`  // websocket endpoint of the Pi service
	Mock bool   `json:"mock"` // true → the built-in offline mock serves this agent
}

// LoadAgents reads <configDir>/lflow/agents.json. With no file (or a broken
// one) the built-in Pi and Grok agents are registered so @mentions work out of
// the box.
func LoadAgents(configDir string) []Agent {
	// Pi and Grok each run on their own local CLI (see ClientFor); the offline
	// mock serves an agent whose backend is not installed, so @mentions still
	// work with nothing installed.
	fallback := []Agent{{Name: "Pi"}, {Name: "Grok"}}
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
// websocket bridge to a configured service, or the agent's own CLI backend.
// Each built-in agent runs on ITS backend and nothing else — @Grok never falls
// through to pi. When that backend is not installed the offline mock serves the
// agent so @mentions still work in tests and offline.
func ClientFor(a Agent) Client {
	if a.Mock {
		return &MockClient{}
	}
	if a.URL != "" {
		return &WSClient{URL: a.URL}
	}
	if prov, ok := providerFor(a.Name); ok && agent.Available(prov) {
		return &CLIClient{Provider: prov}
	}
	return &MockClient{}
}

// providerFor maps a built-in agent to its CLI backend. Only the implemented
// agents are wired — there are no custom/plugin agents — so a new agent is a
// new case here. An unrecognized name is served by the offline mock.
func providerFor(name string) (agent.Provider, bool) {
	switch name {
	case "Pi":
		return agent.ProviderPi, true
	case "Grok":
		return agent.ProviderGrok, true
	}
	return "", false
}
