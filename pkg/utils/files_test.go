package utils

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/lflow/lflow/pkg/utils/assert"
)

func TestEnsureDir(t *testing.T) {
	tmpDir := t.TempDir()
	testPath := filepath.Join(tmpDir, "test", "nested", "dir")

	// Create directory
	err := EnsureDir(testPath)
	assert.Equal(t, err, nil, "EnsureDir should succeed")

	// Verify it exists
	info, err := os.Stat(testPath)
	assert.Equal(t, err, nil, "directory should exist")
	assert.Equal(t, info.IsDir(), true, "should be a directory")

	// Call again on existing directory - should not error
	err = EnsureDir(testPath)
	assert.Equal(t, err, nil, "EnsureDir should succeed on existing directory")
}
