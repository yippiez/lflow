package main

import (
	"os"

	"github.com/lflow/lflow/pkg/tui/infra"
	"github.com/lflow/lflow/pkg/utils/log"
	_ "github.com/mattn/go-sqlite3"
	"github.com/pkg/errors"

	// commands
	"github.com/lflow/lflow/pkg/tui/cmd/artifact"
	"github.com/lflow/lflow/pkg/tui/cmd/auth"
	"github.com/lflow/lflow/pkg/tui/cmd/export"
	"github.com/lflow/lflow/pkg/tui/cmd/node"
	"github.com/lflow/lflow/pkg/tui/cmd/root"
	"github.com/lflow/lflow/pkg/tui/cmd/version"
)

// versionTag is populated during link time
var versionTag = "master"

func main() {
	// the database location comes from the config file alone; there is no
	// flag for it
	ctx, err := infra.Init(versionTag)
	if err != nil {
		panic(errors.Wrap(err, "initializing context"))
	}
	defer ctx.DB.Close()

	root.Register(node.NewCmd(*ctx))
	root.Register(artifact.NewCmd(*ctx))
	root.Register(auth.NewCmd(*ctx))
	root.Register(export.NewCmd(*ctx))
	root.Register(version.NewCmd(*ctx))

	if err := root.Execute(); err != nil {
		log.Errorf("%s\n", err.Error())
		os.Exit(1)
	}
}
