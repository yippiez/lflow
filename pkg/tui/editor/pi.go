package editor

import (
	_ "embed"
	"encoding/json"
	"os"
	"path/filepath"
)

//go:embed pi/worker_finish.ts
var workerFinishTS string

// workerExtensionPath writes lflow's finish_worker pi extension to ~/.lflow/pi/
// (creating it if needed) and returns its path, for `pi --extension`.
func workerExtensionPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	dir := filepath.Join(home, ".lflow", "pi")
	if os.MkdirAll(dir, 0o755) != nil {
		return ""
	}
	path := filepath.Join(dir, "worker_finish.ts")
	if cur, _ := os.ReadFile(path); string(cur) != workerFinishTS {
		if os.WriteFile(path, []byte(workerFinishTS), 0o644) != nil {
			return ""
		}
	}
	return path
}

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
