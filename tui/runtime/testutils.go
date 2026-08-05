package runtime

import (
	"testing"

	"github.com/lflow/lflow/tui/database"
	"github.com/pkg/errors"
)

// getDefaultTestPaths creates default test paths with all paths pointing to a temp directory
func getDefaultTestPaths(t *testing.T) Paths {
	tmpDir := t.TempDir()
	return Paths{
		Home:   tmpDir,
		Cache:  tmpDir,
		Config: tmpDir,
		Data:   tmpDir,
	}
}

// InitTestCtx initializes a test context with an in-memory database
// and a temporary directory for all paths
func InitTestCtx(t *testing.T) Ctx {
	paths := getDefaultTestPaths(t)
	db := database.InitTestMemoryDB(t)

	if err := InitLflowDirs(paths); err != nil {
		t.Fatal(errors.Wrap(err, "creating test directories"))
	}

	return Ctx{
		DB:    db,
		Paths: paths,
	}
}
