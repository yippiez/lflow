package context

import (
	"path/filepath"

	"github.com/lflow/lflow/pkg/cli/consts"
	"github.com/lflow/lflow/pkg/cli/utils"
	"github.com/pkg/errors"
)

// InitLflowDirs creates the lflow directories if they don't already exist.
func InitLflowDirs(paths Paths) error {
	if paths.Config != "" {
		configDir := filepath.Join(paths.Config, consts.LflowDirName)
		if err := utils.EnsureDir(configDir); err != nil {
			return errors.Wrap(err, "initializing config dir")
		}
	}
	if paths.Data != "" {
		dataDir := filepath.Join(paths.Data, consts.LflowDirName)
		if err := utils.EnsureDir(dataDir); err != nil {
			return errors.Wrap(err, "initializing data dir")
		}
	}
	if paths.Cache != "" {
		cacheDir := filepath.Join(paths.Cache, consts.LflowDirName)
		if err := utils.EnsureDir(cacheDir); err != nil {
			return errors.Wrap(err, "initializing cache dir")
		}
	}

	return nil
}
