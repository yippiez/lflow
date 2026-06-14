package context

import (
	"path/filepath"
	"testing"

	"github.com/lflow/lflow/pkg/cli/consts"
	"github.com/lflow/lflow/pkg/cli/database"
	"github.com/lflow/lflow/pkg/shared/clock"
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
func InitTestCtx(t *testing.T) DnoteCtx {
	paths := getDefaultTestPaths(t)
	db := database.InitTestMemoryDB(t)

	if err := InitLflowDirs(paths); err != nil {
		t.Fatal(errors.Wrap(err, "creating test directories"))
	}

	return DnoteCtx{
		DB:    db,
		Paths: paths,
		Clock: clock.NewMock(), // Use a mock clock to test times
	}
}

// InitTestCtxWithDB initializes a test context with the provided database
// and a temporary directory for all paths.
// Used when you need full control over database initialization (e.g. migration tests).
func InitTestCtxWithDB(t *testing.T, db *database.DB) DnoteCtx {
	paths := getDefaultTestPaths(t)

	if err := InitLflowDirs(paths); err != nil {
		t.Fatal(errors.Wrap(err, "creating test directories"))
	}

	return DnoteCtx{
		DB:    db,
		Paths: paths,
		Clock: clock.NewMock(), // Use a mock clock to test times
	}
}

// InitTestCtxWithFileDB initializes a test context with a file-based database
// at the expected path.
func InitTestCtxWithFileDB(t *testing.T) DnoteCtx {
	paths := getDefaultTestPaths(t)

	if err := InitLflowDirs(paths); err != nil {
		t.Fatal(errors.Wrap(err, "creating test directories"))
	}

	dbPath := filepath.Join(paths.Data, consts.LflowDirName, consts.LflowDBFileName)
	db, err := database.Open(dbPath)
	if err != nil {
		t.Fatal(errors.Wrap(err, "opening database"))
	}

	if _, err := db.Exec(database.GetDefaultSchemaSQL()); err != nil {
		t.Fatal(errors.Wrap(err, "running schema sql"))
	}

	t.Cleanup(func() { db.Close() })

	return DnoteCtx{
		DB:    db,
		Paths: paths,
		Clock: clock.NewMock(), // Use a mock clock to test times
	}
}
