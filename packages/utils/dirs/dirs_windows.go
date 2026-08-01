//go:build windows

package dirs

import (
	"path/filepath"
)

func initDirs() {
	Home = getHomeDir()
	ConfigHome = filepath.Join(Home, ".lflow")
	DataHome = filepath.Join(Home, ".lflow")
	CacheHome = filepath.Join(Home, ".lflow")
}
