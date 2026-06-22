package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// Settings mirrors the fields lflow reads from pi's own config at
// ~/.pi/agent/settings.json (moved here from editor/pi.go).
type Settings struct {
	DefaultProvider string `json:"defaultProvider"`
	DefaultModel    string `json:"defaultModel"`
	DefaultThinking string `json:"defaultThinkingLevel"`
}

var settingsCache *Settings

// ReadSettings reads pi's settings.json (cached). A missing or malformed file
// yields the zero Settings so the picker and default resolution keep working.
func ReadSettings() Settings {
	if settingsCache == nil {
		settingsCache = &Settings{}
		home, _ := os.UserHomeDir()
		if data, err := os.ReadFile(filepath.Join(home, ".pi", "agent", "settings.json")); err == nil {
			_ = json.Unmarshal(data, settingsCache)
		}
	}
	return *settingsCache
}

// DefaultModel resolves the model + thinking level a new agent uses absent any
// lflow-config or session override: pi's configured default, with LFLOW_PI_MODEL /
// LFLOW_PI_THINKING env overrides (used by e2e tests to pin echo/echo). This is the
// last fallback in the editor's resolution chain (config → here → session override).
func DefaultModel() (Model, string) {
	s := ReadSettings()
	m := Model{CLI: ProviderPi, Upstream: s.DefaultProvider, Name: s.DefaultModel}
	thinking := s.DefaultThinking
	if v := os.Getenv("LFLOW_PI_MODEL"); v != "" {
		m = ParseModel(v)
	}
	if v := os.Getenv("LFLOW_PI_THINKING"); v != "" {
		thinking = v
	}
	return m, thinking
}

// ThinkingLevels is the cycle ctrl+t steps through. "off" is a real override
// (distinct from "" = use config); it maps to no --thinking flag at run.
var ThinkingLevels = []string{"off", "low", "medium", "high"}
