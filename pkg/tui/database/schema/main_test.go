package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lflow/lflow/pkg/shared/assert"
	"github.com/lflow/lflow/pkg/tui/consts"
)

func TestRun(t *testing.T) {
	tmpDir := t.TempDir()
	outputPath := filepath.Join(tmpDir, "schema.sql")

	// Run the function
	if err := run(tmpDir, outputPath); err != nil {
		t.Fatalf("run() failed: %v", err)
	}

	// Verify schema.sql was created
	content, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("reading schema.sql: %v", err)
	}

	schema := string(content)

	// Verify it has the header
	assert.Equal(t, strings.HasPrefix(schema, "-- This is the final state"), true, "schema.sql should have header comment")

	// Verify schema contains expected tables
	expectedTables := []string{
		"CREATE TABLE nodes",
		"CREATE TABLE system",
		"CREATE TABLE wf_mirrors",
		"CREATE VIRTUAL TABLE node_fts",
	}

	for _, expected := range expectedTables {
		assert.Equal(t, strings.Contains(schema, expected), true, fmt.Sprintf("schema should contain %s", expected))
	}

	// Verify schema contains triggers
	expectedTriggers := []string{
		"CREATE TRIGGER nodes_after_insert",
		"CREATE TRIGGER nodes_after_delete",
		"CREATE TRIGGER nodes_after_update",
	}

	for _, expected := range expectedTriggers {
		assert.Equal(t, strings.Contains(schema, expected), true, fmt.Sprintf("schema should contain %s", expected))
	}

	// Verify schema does not contain sqlite internal tables
	assert.Equal(t, strings.Contains(schema, "sqlite_sequence"), false, "schema should not contain sqlite_sequence")

	// Verify system key-value pairs for schema versions are present
	expectedSchemaKey := fmt.Sprintf("INSERT INTO system (key, value) VALUES ('%s',", consts.SystemSchema)
	assert.Equal(t, strings.Contains(schema, expectedSchemaKey), true, "schema should contain schema version INSERT statement")

	expectedRemoteSchemaKey := fmt.Sprintf("INSERT INTO system (key, value) VALUES ('%s',", consts.SystemRemoteSchema)
	assert.Equal(t, strings.Contains(schema, expectedRemoteSchemaKey), true, "schema should contain remote_schema version INSERT statement")
}
