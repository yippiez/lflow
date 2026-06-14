package cmd

import (
	"fmt"

	"github.com/lflow/lflow/pkg/server/buildinfo"
)

func versionCmd() {
	fmt.Printf("lflow-server-%s\n", buildinfo.Version)
}
