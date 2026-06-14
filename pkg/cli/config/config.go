package config

import (
	"fmt"
	"os"

	"github.com/lflow/lflow/pkg/cli/consts"
	"github.com/lflow/lflow/pkg/cli/context"
	"github.com/lflow/lflow/pkg/cli/log"
	"github.com/lflow/lflow/pkg/cli/utils"
	"github.com/pkg/errors"
	"gopkg.in/yaml.v2"
)

// Config holds lflow configuration
type Config struct {
	Editor             string `yaml:"editor"`
	APIEndpoint        string `yaml:"apiEndpoint"`
	EnableUpgradeCheck bool   `yaml:"enableUpgradeCheck"`
	// DBPath relocates the SQLite database. The config file is the only
	// place to set it; there is no flag.
	DBPath string `yaml:"dbPath,omitempty"`
	// WorkflowySessionID enables the `lflow wf` commands. There is no login
	// command — paste the value of the `sessionid` cookie from a logged-in
	// workflowy.com browser session here.
	WorkflowySessionID string `yaml:"workflowySessionId,omitempty"`
	// WorkflowyBaseURL overrides the workflowy endpoint for a self-hosted
	// instance or tests. Leave empty for workflowy.com.
	WorkflowyBaseURL string `yaml:"workflowyBaseUrl,omitempty"`
}

func checkLegacyPath(ctx context.DnoteCtx) (string, bool) {
	legacyPath := fmt.Sprintf("%s/%s", ctx.Paths.LegacyDnote, consts.ConfigFilename)

	ok, err := utils.FileExists(legacyPath)
	if err != nil {
		log.Error(errors.Wrapf(err, "checking legacy dnote directory at %s", legacyPath).Error())
	}
	if ok {
		return legacyPath, true
	}

	return "", false
}

// GetPath returns the path to the lflow config file
func GetPath(ctx context.DnoteCtx) string {
	legacyPath, ok := checkLegacyPath(ctx)
	if ok {
		return legacyPath
	}

	return fmt.Sprintf("%s/%s/%s", ctx.Paths.Config, consts.LflowDirName, consts.ConfigFilename)
}

// Read reads the config file
func Read(ctx context.DnoteCtx) (Config, error) {
	var ret Config

	configPath := GetPath(ctx)
	b, err := os.ReadFile(configPath)
	if err != nil {
		return ret, errors.Wrap(err, "reading config file")
	}

	err = yaml.Unmarshal(b, &ret)
	if err != nil {
		return ret, errors.Wrap(err, "unmarshalling config")
	}

	return ret, nil
}

// Write writes the config to the config file
func Write(ctx context.DnoteCtx, cf Config) error {
	path := GetPath(ctx)

	b, err := yaml.Marshal(cf)
	if err != nil {
		return errors.Wrap(err, "marshalling config into YAML")
	}

	err = os.WriteFile(path, b, 0644)
	if err != nil {
		return errors.Wrap(err, "writing the config file")
	}

	return nil
}
