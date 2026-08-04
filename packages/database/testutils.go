package database

import (
	"database/sql"
	"fmt"
	"testing"

	"github.com/lflow/lflow/packages/utils"
	"github.com/lflow/lflow/packages/utils/consts"
	"github.com/pkg/errors"
)

// MustScan scans the given row and fails a test in case of any errors
func MustScan(t *testing.T, message string, row *sql.Row, args ...interface{}) {
	err := row.Scan(args...)
	if err != nil {
		t.Fatal(errors.Wrap(errors.Wrap(err, "scanning a row"), message))
	}
}

// MustExec executes the given SQL query and fails a test if an error occurs
func MustExec(t *testing.T, message string, db *DB, query string, args ...interface{}) sql.Result {
	result, err := db.Exec(query, args...)
	if err != nil {
		t.Fatal(errors.Wrap(errors.Wrap(err, "executing sql"), message))
	}

	return result
}

// InitTestMemoryDB initializes an in-memory test database with the default schema.
func InitTestMemoryDB(t *testing.T) *DB {
	return initTestMemoryDBRaw(t, "")
}

// initTestMemoryDBRaw initializes an in-memory test database with the default
// schema. If schemaPath is non-empty, that SQL file is used instead.
func initTestMemoryDBRaw(t *testing.T, schemaPath string) *DB {
	uuid := mustGenerateTestUUID(t)
	dbName := fmt.Sprintf("file:%s?mode=memory&cache=shared", uuid)

	db, err := Open(dbName)
	if err != nil {
		t.Fatal(errors.Wrap(err, "opening in-memory database"))
	}

	var schemaSQL string
	if schemaPath != "" {
		schemaSQL = string(utils.ReadFileAbs(schemaPath))
	} else {
		schemaSQL = DefaultSchemaSQL()
	}

	if _, err := db.Exec(schemaSQL); err != nil {
		t.Fatal(errors.Wrap(err, "running schema sql"))
	}

	t.Cleanup(func() { db.Close() })
	return db
}

// OpenTestDB opens the database connection to a test database
// without initializing any schema
func OpenTestDB(t *testing.T, lflowDir string) *DB {
	dbPath := fmt.Sprintf("%s/%s/%s", lflowDir, consts.LflowDirName, consts.LflowDBFileName)
	db, err := Open(dbPath)
	if err != nil {
		t.Fatal(errors.Wrap(err, "opening database connection to the test database"))
	}

	return db
}

// mustGenerateTestUUID generates a UUID for test databases and fails the test on error
func mustGenerateTestUUID(t *testing.T) string {
	uuid, err := utils.GenerateUUID()
	if err != nil {
		t.Fatal(errors.Wrap(err, "generating UUID for test database"))
	}
	return uuid
}
