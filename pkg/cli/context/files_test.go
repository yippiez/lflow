package context

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/lflow/lflow/pkg/cli/consts"
	"github.com/lflow/lflow/pkg/shared/assert"
)

func assertDirsExist(t *testing.T, paths Paths) {
	configDir := filepath.Join(paths.Config, consts.LflowDirName)
	info, err := os.Stat(configDir)
	assert.Equal(t, err, nil, "config dir should exist")
	assert.Equal(t, info.IsDir(), true, "config should be a directory")

	dataDir := filepath.Join(paths.Data, consts.LflowDirName)
	info, err = os.Stat(dataDir)
	assert.Equal(t, err, nil, "data dir should exist")
	assert.Equal(t, info.IsDir(), true, "data should be a directory")

	cacheDir := filepath.Join(paths.Cache, consts.LflowDirName)
	info, err = os.Stat(cacheDir)
	assert.Equal(t, err, nil, "cache dir should exist")
	assert.Equal(t, info.IsDir(), true, "cache should be a directory")
}

func TestInitLflowDirs(t *testing.T) {
	tmpDir := t.TempDir()

	paths := Paths{
		Config: filepath.Join(tmpDir, "config"),
		Data:   filepath.Join(tmpDir, "data"),
		Cache:  filepath.Join(tmpDir, "cache"),
	}

	// Initialize directories
	err := InitLflowDirs(paths)
	assert.Equal(t, err, nil, "InitLflowDirs should succeed")
	assertDirsExist(t, paths)

	// Call again - should be idempotent
	err = InitLflowDirs(paths)
	assert.Equal(t, err, nil, "InitLflowDirs should succeed when dirs already exist")
	assertDirsExist(t, paths)
}
