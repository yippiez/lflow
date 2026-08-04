package agent

import "os"

// AgentProviderDefault is the baked-in default model + thinking level for a provider.
// There is no in-app model picker — each backend ships one sensible default.
// Pi honors the LFLOW_PI_MODEL / LFLOW_PI_THINKING env overrides (the
// e2e harness pins echo/echo through them); grok uses its own default model.
func AgentProviderDefault(p AgentProvider) (Model, string) {
	switch p {
	case AgentProviderGrok:
		return Model{CLI: AgentProviderGrok, Name: "grok-4.5"}, ""
	default: // pi
		m := Model{CLI: AgentProviderPi, Upstream: "openai-codex", Name: "gpt-5.6-terra"}
		thinking := ""
		if v := os.Getenv("LFLOW_PI_MODEL"); v != "" {
			m = AgentModelParse(v)
		}
		if v := os.Getenv("LFLOW_PI_THINKING"); v != "" {
			thinking = v
		}
		return m, thinking
	}
}
