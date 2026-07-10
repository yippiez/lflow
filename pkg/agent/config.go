package agent

import "os"

// ProviderDefault is the baked-in default model + thinking level for a provider.
// There is no in-app model picker — each backend ships one sensible default, so
// a @mention turn just runs whichever CLI is available with that provider's
// default. Pi honors the LFLOW_PI_MODEL / LFLOW_PI_THINKING env overrides (the
// e2e harness pins echo/echo through them); grok uses its own default model.
func ProviderDefault(p Provider) (Model, string) {
	switch p {
	case ProviderGrok:
		return Model{CLI: ProviderGrok, Name: "grok-4.5"}, ""
	default: // pi
		m := Model{CLI: ProviderPi, Upstream: "opencode-go", Name: "deepseek-v4-flash"}
		thinking := ""
		if v := os.Getenv("LFLOW_PI_MODEL"); v != "" {
			m = ParseModel(v)
		}
		if v := os.Getenv("LFLOW_PI_THINKING"); v != "" {
			thinking = v
		}
		return m, thinking
	}
}
