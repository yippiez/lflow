package config

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/lflow/lflow/pkg/cli/consts"
	"github.com/lflow/lflow/pkg/cli/context"
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
	// WorkflowySessionID enables the `lflow wf` commands. There is no login
	// command — paste the value of the `sessionid` cookie from a logged-in
	// workflowy.com browser session here.
	WorkflowySessionID string `json:"workflowySessionId,omitempty"`
	// WorkflowyBaseURL overrides the workflowy endpoint for a self-hosted
	// instance or tests. Leave empty for workflowy.com.
	WorkflowyBaseURL string `json:"workflowyBaseUrl,omitempty"`
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
