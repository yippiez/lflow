package agent

import (
	"context"
	"os/exec"
)

// Backend is one CLI agent adapter (pi, opencode, grok). Implementations shell out
// to the CLI and normalize its event stream into agent.Event. This is the Go
// equivalent of pir's per-provider run*/listModels functions, expressed as an
// interface so the registry can fan out over all of them.
type Backend interface {
	Name() Provider
	Available() bool                                                        // CLI on PATH
	ListModels() ([]Model, error)                                           // the CLI's selectable models
	Run(ctx context.Context, task string, opts RunOptions) (Session, error) // start a turn
}

// backends is the compiled-in registry, in picker order: grok first (the
// first non-pi option), then pi. Adding a provider = one Backend
// implementation (its own file, see grok.go) + one entry here.
var backends = []Backend{grokBackend{}, piBackend{}}

// Backends returns the registered backends.
func Backends() []Backend { return backends }

// Get returns the backend for provider p.
func Get(p Provider) (Backend, bool) {
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
