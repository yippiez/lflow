// Package ui provides the user interface for the program
package ui

import (
	"fmt"

	"github.com/lflow/lflow/packages/utils"
	"github.com/lflow/lflow/packages/utils/consts"
	"github.com/pkg/errors"
)

// GetTmpContentPath returns the path to the temporary file containing
// content being added or edited, given the cache directory to place it in
func GetTmpContentPath(cacheDir string) (string, error) {
	for i := 0; ; i++ {
		filename := fmt.Sprintf("%s_%d.%s", consts.TmpContentFileBase, i, consts.TmpContentFileExt)
		candidate := fmt.Sprintf("%s/%s", cacheDir, filename)

		ok, err := utils.FileExists(candidate)
		if err != nil {
			return "", errors.Wrapf(err, "checking if file exists at %s", candidate)
		}
		if !ok {
			return candidate, nil
		}
	}
}
