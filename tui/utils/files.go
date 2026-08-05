package utils

import (
	"os"
	"path/filepath"

	"github.com/pkg/errors"
)

// ReadFileAbs reads the content of the file with the given file path by resolving
// it as an absolute path
func ReadFileAbs(relpath string) []byte {
	fp, err := filepath.Abs(relpath)
	if err != nil {
		panic(err)
	}

	b, err := os.ReadFile(fp)
	if err != nil {
		panic(err)
	}

	return b
}

// FileExists checks if the file exists at the given path
func FileExists(filepath string) (bool, error) {
	_, err := os.Stat(filepath)
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}

	return false, errors.Wrap(err, "getting file info")
}

// EnsureDir creates a directory if it doesn't exist.
// Returns nil if the directory already exists or was successfully created.
func EnsureDir(path string) error {
	ok, err := FileExists(path)
	if err != nil {
		return errors.Wrapf(err, "checking if dir exists at %s", path)
	}
	if ok {
		return nil
	}

	if err := os.MkdirAll(path, 0755); err != nil {
		return errors.Wrapf(err, "creating directory at %s", path)
	}

	return nil
}
