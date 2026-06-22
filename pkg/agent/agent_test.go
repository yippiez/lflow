package agent

import "testing"

func TestParseModelRoundTrip(t *testing.T) {
	cases := []Model{
		{CLI: ProviderPi, Upstream: "anthropic", Name: "claude-opus"},
		{CLI: ProviderOpencode, Upstream: "xai", Name: "grok-4"},
		{CLI: ProviderGrok, Name: "grok-build"},
	}
	for _, want := range cases {
		got := ParseModel(want.String())
		if got != want {
			t.Errorf("round-trip %q: got %+v want %+v", want.String(), got, want)
		}
	}
}

func TestParseModelBarePiDefault(t *testing.T) {
	// a bare "provider/model" (no CLI prefix) defaults to pi, matching legacy config.
	m := ParseModel("anthropic/claude-opus")
	if m.CLI != ProviderPi || m.Upstream != "anthropic" || m.Name != "claude-opus" {
		t.Fatalf("got %+v", m)
	}
	if m.FlagValue() != "anthropic/claude-opus" {
		t.Errorf("FlagValue = %q", m.FlagValue())
	}
}

func TestGrokFlagValueIsBareName(t *testing.T) {
	m := Model{CLI: ProviderGrok, Name: "grok-build"}
	if m.FlagValue() != "grok-build" {
		t.Errorf("grok FlagValue = %q, want bare name", m.FlagValue())
	}
	if m.String() != "grok:grok-build" {
		t.Errorf("grok String = %q", m.String())
	}
}

// TestListModelsLive hits the real CLIs when present; it is informational and
// never fails (a missing/unauthenticated CLI just contributes no models).
func TestListModelsLive(t *testing.T) {
	byCLI := map[Provider]int{}
	for _, m := range ListModels() {
		byCLI[m.CLI]++
	}
	t.Logf("models by backend: pi=%d opencode=%d grok=%d",
		byCLI[ProviderPi], byCLI[ProviderOpencode], byCLI[ProviderGrok])
}
