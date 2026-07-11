package tag

import (
	"testing"

	"github.com/lflow/lflow/pkg/agent"
)

// The built-in agents register out of the box with no config file, so @Pi and
// @Grok both complete and fire without any setup.
func TestLoadAgentsDefaultsToPiAndGrok(t *testing.T) {
	agents := LoadAgents(t.TempDir()) // empty dir → no agents.json → fallback
	got := map[string]bool{}
	for _, a := range agents {
		got[a.Name] = true
	}
	for _, name := range []string{"Pi", "Grok"} {
		if !got[name] {
			t.Errorf("built-in agent %q not registered; got %v", name, agents)
		}
	}
}

// Each built-in agent is hardcoded to exactly one backend — @Pi → pi, @Grok →
// grok — and nothing else: @Grok must never resolve to pi. An unrecognized name
// returns false and is served by the offline mock.
func TestProviderFor(t *testing.T) {
	cases := []struct {
		name    string
		want    agent.Provider
		matched bool
	}{
		{"Pi", agent.ProviderPi, true},
		{"Grok", agent.ProviderGrok, true},
		{"grok", "", false},    // exact match only — not the generic lookup
		{"Zamboni", "", false}, // no custom agents
	}
	for _, c := range cases {
		got, ok := providerFor(c.name)
		if ok != c.matched || got != c.want {
			t.Errorf("providerFor(%q) = (%q, %v), want (%q, %v)",
				c.name, got, ok, c.want, c.matched)
		}
	}
	// the safety net the user asked for: Grok is never pi.
	if p, _ := providerFor("Grok"); p == agent.ProviderPi {
		t.Fatal("@Grok resolved to the pi backend")
	}
}
