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
	"fmt"
	"os"
	"path/filepath"

	"github.com/lflow/lflow/pkg/agent"
)

// ThreadNode is one node of the context sent to the agent: the mention's
// parent first (one Parent-marked line — where the thread sits), then the
// thread root (the mention node itself) and its subtree depth-first. Every
// node's children appear at most once, so mirrors can neither loop the walk
// nor duplicate a subtree. Nothing else is sent; the rest of the outline the
// agent searches via the lflow CLI.
type ThreadNode struct {
	UUID   string `json:"uuid"`
	Depth  int    `json:"depth"` // tree depth; the Parent line, when present, is 0
	Name   string `json:"name"`
	Type   string `json:"type"`
	Role   string `json:"role"`   // "user" | "agent"
	Asked  bool   `json:"asked"`  // the node this turn is about — replies target it
	Parent bool   `json:"parent"` // the mention's parent — ambient context, not the thread
	// per-type XML serialization, filled from the node type's toContext hook
	// (editor registry) so a typed node reads coherently in <NodeContext>:
	XMLTag   string `json:"xmlTag,omitempty"`   // element name; "" → node. Role tags (parent/asked/answer) still win.
	XMLAttrs string `json:"xmlAttrs,omitempty"` // attributes inside the opening tag, e.g. done="true"
	XMLBody  string `json:"xmlBody,omitempty"`  // multi-line element content replacing the one-line Name
}

// Event is one message streamed back from the agent service.
type Event struct {
	Op   string `json:"op"`   // "message" | "tool" | "done" | "error"
	Text string `json:"text"` // op=message/error; op=tool: the muted detail (file/cmd)
	Tool string `json:"tool"` // op=tool: the tool name (Read / Write / Edit …)
	// Placement is where a message lands relative to the asked node — the two
	// Claude-Tag surfaces: "below" posts it like a message-board reply (next
	// sibling), "thread" nests it as the asked node's child. Default: thread.
	Placement string `json:"placement"`
}

// Client runs agent turns, launch-and-forget: every Send is a FRESH agent fed
// the whole rendered thread, streaming events until done/error closes the
// channel. There is deliberately no session to resume — the outline is the
// user's and changes between turns (nodes edited, moved, deleted), so any
// remembered context would drift from what the thread actually says now. The
// thread root's uuid (the first non-Parent node) is the stable conversation
// identity for anything that needs one.
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

// ClientFor returns the client that serves the agent, or an error when no
// backend can. Each built-in agent runs on ITS CLI backend and nothing else —
// @Grok never falls through to pi, and a missing backend is an error, not a
// silent mock. (The mock only serves a test agent that sets Mock explicitly.)
func ClientFor(a Agent) (Client, error) {
	if a.Mock {
		return &MockClient{}, nil
	}
	if a.URL != "" {
		return &WSClient{URL: a.URL}, nil
	}
	prov, err := providerFor(a.Name)
	if err != nil {
		return nil, err
	}
	if !agent.AgentAvailable(prov) {
		return nil, fmt.Errorf("Missing dependency: %s", prov)
	}
	return &CLIClient{Provider: prov}, nil
}

// DepFor maps a built-in agent name to the CLI binary it needs — the entry
// dep-availability checks (daemon deps op, picker greying) key on.
func DepFor(name string) (string, bool) {
	prov, err := providerFor(name)
	if err != nil {
		return "", false
	}
	return string(prov), true
}

// providerFor maps a built-in agent to its CLI backend. Only the implemented
// agents are wired — there are no custom/plugin agents — so a new agent is a
// new case here. An unrecognized name is an error.
func providerFor(name string) (agent.AgentProvider, error) {
	switch name {
	case "Pi":
		return agent.AgentProviderPi, nil
	case "Grok":
		return agent.AgentProviderGrok, nil
	}
	return "", fmt.Errorf("Unknown agent @%s", name)
}
