package agent

import (
	"context"
	"os/exec"
)

// AgentBackend is one CLI agent adapter (pi, opencode, grok). Implementations shell out
// to the CLI and normalize its event stream into agent.Event. This is the Go
// equivalent of pir's per-provider run*/listModels functions, expressed as an
// interface so the registry can fan out over all of them.
type AgentBackend interface {
	Name() AgentProvider
	Available() bool                                                        // CLI on PATH
	ListModels() ([]Model, error)                                           // the CLI's selectable models
	Run(ctx context.Context, task string, opts AgentRunOptions) (AgentSession, error) // start a turn
}

// backends is the compiled-in registry, in picker order: grok first (the
// first non-pi option), then pi. Adding a provider = one Backend
// implementation (its own file, see grok.go) + one entry here.
var backends = []AgentBackend{grokBackend{}, piBackend{}}

// AgentBackends returns the registered backends.
func AgentBackends() []AgentBackend { return backends }

// AgentAvailable reports whether provider p's CLI is installed and runnable.
func AgentAvailable(p AgentProvider) bool {
	b, ok := AgentBackendFor(p)
	return ok && b.Available()
}

// AgentBackendFor returns the backend for provider p.
func AgentBackendFor(p AgentProvider) (AgentBackend, bool) {
	for _, b := range backends {
		if b.Name() == p {
			return b, true
		}
	}
	return nil, false
}

// onPath reports whether a CLI binary is resolvable, so listing/running a missing
// backend degrades to "no models" instead of erroring.
func onPath(bin string) bool {
	_, err := exec.LookPath(bin)
	return err == nil
}
