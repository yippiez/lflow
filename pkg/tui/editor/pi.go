package editor

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// piSettings mirrors the fields we read from ~/.pi/agent/settings.json.
type piSettings struct {
	DefaultProvider      string `json:"defaultProvider"`
	DefaultModel         string `json:"defaultModel"`
	DefaultThinkingLevel string `json:"defaultThinkingLevel"`
}

var piInfoCache *piSettings

func piInfo() piSettings {
	if piInfoCache == nil {
		piInfoCache = &piSettings{}
		home, _ := os.UserHomeDir()
		if data, err := os.ReadFile(filepath.Join(home, ".pi", "agent", "settings.json")); err == nil {
			_ = json.Unmarshal(data, piInfoCache)
		}
	}
	return *piInfoCache
}

// piModelInfo returns pi's configured "provider/model" and thinking level for the
// status bar and worker invocation.
func piModelInfo() (model, thinking string) {
	s := piInfo()
	model = s.DefaultModel
	if s.DefaultProvider != "" && model != "" {
		model = s.DefaultProvider + "/" + model
	}
	return model, s.DefaultThinkingLevel
}
