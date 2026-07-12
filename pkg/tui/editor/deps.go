package editor

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"

	"github.com/lflow/lflow/pkg/tui/client"
	"github.com/lflow/lflow/pkg/tui/tag"
)

// NodeCLIDeps: features that shell out declare the CLI binaries they need —
// node types via the registry's cliDeps, agents via tag.DepFor. Execution is
// DAEMON-side (the client is only a client), so availability is judged by the
// daemon (client.Deps → LookPath there); without a daemon (LFLOW_NO_DAEMON)
// the check runs locally, since the CLI then runs in this process. A missing
// dependency greys the entry out in pickers, and running an existing node
// errors: "Missing dependency: <bin>".

// loadDeps probes every declared dependency once at startup.
func (m *Model) loadDeps() {
	set := map[string]bool{}
	for _, nt := range nodeTypes {
		for _, b := range nt.cliDeps {
			set[b] = true
		}
	}
	for _, a := range m.agents {
		if a.Mock || a.URL != "" {
			continue // served without a local CLI
		}
		if b, ok := tag.DepFor(a.Name); ok {
			set[b] = true
		}
	}
	bins := make([]string, 0, len(set))
	for b := range set {
		bins = append(bins, b)
	}
	if len(bins) == 0 {
		return
	}
	if m.live != nil {
		if got, err := m.live.Deps(bins); err == nil {
			m.deps = got
			return
		}
	}
	m.deps = map[string]bool{}
	for _, b := range bins {
		_, err := exec.LookPath(b)
		m.deps[b] = err == nil
	}
}

// depOK reports whether a binary is available. Unprobed bins count available —
// an unknown dependency fails at run time with the daemon's own error, never
// silently disables UI.
func (m *Model) depOK(bin string) bool {
	if m.deps == nil {
		return true
	}
	ok, probed := m.deps[bin]
	return !probed || ok
}

// typeDepMissing returns the first missing dependency of a node type.
func (m *Model) typeDepMissing(key string) (string, bool) {
	for _, b := range typeOf(key).cliDeps {
		if !m.depOK(b) {
			return b, true
		}
	}
	return "", false
}

// agentByName finds a configured agent.
func (m *Model) agentByName(name string) (tag.Agent, bool) {
	for _, a := range m.agents {
		if a.Name == name {
			return a, true
		}
	}
	return tag.Agent{}, false
}

// agentDepMissing returns the missing CLI backend of a configured agent.
func (m *Model) agentDepMissing(a tag.Agent) (string, bool) {
	if a.Mock || a.URL != "" {
		return "", false
	}
	if b, ok := tag.DepFor(a.Name); ok && !m.depOK(b) {
		return b, true
	}
	return "", false
}

// daemonTagClient runs @mention turns on the daemon — the editor only ships
// the rendered thread and receives the event stream back. Closing the turn's
// ctx (flash stop, re-send) closes the conn, which kills the CLI daemon-side.
type daemonTagClient struct{ live *client.Client }

func (d daemonTagClient) Send(ctx context.Context, agentName string, thread []tag.ThreadNode) (<-chan tag.Event, error) {
	raw, err := json.Marshal(thread)
	if err != nil {
		return nil, err
	}
	cwd, _ := os.Getwd() // same "pwd where run" rule as $ chips
	wch, err := d.live.AgentTurn(ctx, agentName, raw, cwd, tag.SkillDir())
	if err != nil {
		return nil, err
	}
	out := make(chan tag.Event, 16)
	go func() {
		defer close(out)
		sawEnd := false
		for ev := range wch {
			if ev.Op == "done" || ev.Op == "error" {
				sawEnd = true
			}
			out <- tag.Event{Op: ev.Op, Text: ev.Text, Tool: ev.Tool, Placement: ev.Placement}
		}
		if !sawEnd { // conn lost mid-turn (daemon died): the thread must not hang busy
			out <- tag.Event{Op: "error", Text: "daemon connection lost mid-turn"}
		}
	}()
	return out, nil
}

// tagClientFor picks the transport for an agent: an explicitly mocked or
// websocket agent keeps its own client; everything else runs on the daemon
// when one is connected, and falls back to the local CLI (LFLOW_NO_DAEMON,
// tests) otherwise.
func (m *Model) tagClientFor(ag tag.Agent) (tag.Client, error) {
	if !ag.Mock && ag.URL == "" && m.live != nil {
		return daemonTagClient{live: m.live}, nil
	}
	return tag.ClientFor(ag)
}
