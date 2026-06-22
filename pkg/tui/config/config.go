package config

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/lflow/lflow/pkg/tui/consts"
	"github.com/lflow/lflow/pkg/tui/context"
	"github.com/pkg/errors"
)

// Config holds lflow configuration, stored as JSON at ~/.lflow/settings.json.
type Config struct {
	Editor             string `json:"editor"`
	APIEndpoint        string `json:"apiEndpoint"`
	EnableUpgradeCheck bool   `json:"enableUpgradeCheck"`
	// DBPath relocates the SQLite database. The settings file is the only
	// place to set it; there is no flag.
	DBPath string `json:"dbPath,omitempty"`
	// AgentModel is the default model for new agents, set by the editor's /model
	// command. It is a canonical agent.Model string: "upstream/model" for the pi
	// backend, or "<cli>:upstream/model" for opencode/grok. Empty → fall back to
	// pi's own configured default.
	AgentModel string `json:"agentModel,omitempty"`
	// AgentThinking is the default thinking level for new agents, set by ctrl+t in
	// the editor and persisted on save. One of agent.ThinkingLevels (off/low/
	// medium/high). Empty → fall back to the backend's own default.
	AgentThinking string `json:"agentThinking,omitempty"`
}

// GetPath returns the path to the lflow settings file: ~/.lflow/settings.json.
func GetPath(ctx context.DnoteCtx) string {
	return filepath.Join(ctx.Paths.Home, consts.LflowHomeDirName, consts.SettingsFilename)
}

// Read reads the settings file. A missing file is not an error — it yields the
// zero Config so first run and unconfigured commands keep working.
func Read(ctx context.DnoteCtx) (Config, error) {
	var ret Config

	b, err := os.ReadFile(GetPath(ctx))
	if err != nil {
		if os.IsNotExist(err) {
			return ret, nil
		}
		return ret, errors.Wrap(err, "reading settings file")
	}

	if err := json.Unmarshal(b, &ret); err != nil {
		return ret, errors.Wrap(err, "unmarshalling settings")
	}

	return ret, nil
}

// Write writes the config to the settings file, creating ~/.lflow if needed.
func Write(ctx context.DnoteCtx, cf Config) error {
	path := GetPath(ctx)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return errors.Wrap(err, "creating the settings directory")
	}

	b, err := json.MarshalIndent(cf, "", "  ")
	if err != nil {
		return errors.Wrap(err, "marshalling settings into JSON")
	}

	if err := os.WriteFile(path, b, 0644); err != nil {
		return errors.Wrap(err, "writing the settings file")
	}

	return nil
}
