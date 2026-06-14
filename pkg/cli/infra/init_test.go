package infra

import (
	"fmt"
	"os"
	"testing"

	"github.com/lflow/lflow/pkg/cli/config"
	"github.com/lflow/lflow/pkg/cli/database"
	"github.com/lflow/lflow/pkg/shared/assert"
	"github.com/lflow/lflow/pkg/shared/dirs"
	"github.com/pkg/errors"
)

func TestInitSystemKV(t *testing.T) {
	// Setup
	db := database.InitTestMemoryDB(t)

	var originalCount int
	database.MustScan(t, "counting system configs", db.QueryRow("SELECT count(*) FROM system"), &originalCount)

	// Execute
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(errors.Wrap(err, "beginning a transaction"))
	}

	if err := initSystemKV(tx, "testKey", "testVal"); err != nil {
		tx.Rollback()
		t.Fatal(errors.Wrap(err, "executing"))
	}

	tx.Commit()

	// Test
	var count int
	database.MustScan(t, "counting system configs", db.QueryRow("SELECT count(*) FROM system"), &count)
	assert.Equal(t, count, originalCount+1, "system count mismatch")

	var val string
	database.MustScan(t, "getting system value",
		db.QueryRow("SELECT value FROM system WHERE key = ?", "testKey"), &val)
	assert.Equal(t, val, "testVal", "system value mismatch")
}

func TestInitSystemKV_existing(t *testing.T) {
	// Setup
	db := database.InitTestMemoryDB(t)

	database.MustExec(t, "inserting a system config", db, "INSERT INTO system (key, value) VALUES (?, ?)", "testKey", "testVal")

	var originalCount int
	database.MustScan(t, "counting system configs", db.QueryRow("SELECT count(*) FROM system"), &originalCount)

	// Execute
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(errors.Wrap(err, "beginning a transaction"))
	}

	if err := initSystemKV(tx, "testKey", "newTestVal"); err != nil {
		tx.Rollback()
		t.Fatal(errors.Wrap(err, "executing"))
	}

	tx.Commit()

	// Test
	var count int
	database.MustScan(t, "counting system configs", db.QueryRow("SELECT count(*) FROM system"), &count)
	assert.Equal(t, count, originalCount, "system count mismatch")

	var val string
	database.MustScan(t, "getting system value",
		db.QueryRow("SELECT value FROM system WHERE key = ?", "testKey"), &val)
	assert.Equal(t, val, "testVal", "system value should not have been updated")
}

func TestInit_APIEndpoint(t *testing.T) {
	// Create a temporary directory for test
	tmpDir, err := os.MkdirTemp("", "lflow-init-test-*")
	if err != nil {
		t.Fatal(errors.Wrap(err, "creating temp dir"))
	}
	defer os.RemoveAll(tmpDir)

	// Set up environment to use our temp directory
	t.Setenv("XDG_CONFIG_HOME", fmt.Sprintf("%s/config", tmpDir))
	t.Setenv("XDG_DATA_HOME", fmt.Sprintf("%s/data", tmpDir))
	t.Setenv("XDG_CACHE_HOME", fmt.Sprintf("%s/cache", tmpDir))

	// Force dirs package to reload with new environment
	dirs.Reload()

	// Initialize - should create config with default apiEndpoint
	ctx, err := Init("test-version", "")
	if err != nil {
		t.Fatal(errors.Wrap(err, "initializing"))
	}
	defer ctx.DB.Close()

	// Read the config that was created
	cf, err := config.Read(*ctx)
	if err != nil {
		t.Fatal(errors.Wrap(err, "reading config"))
	}

	// Context should use the apiEndpoint from config
	assert.Equal(t, ctx.APIEndpoint, DefaultAPIEndpoint, "context should use apiEndpoint from config")
	assert.Equal(t, cf.APIEndpoint, DefaultAPIEndpoint, "context should use apiEndpoint from config")
}
