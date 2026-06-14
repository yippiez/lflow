package main

import (
	"os"

	"github.com/lflow/lflow/pkg/cli/infra"
	"github.com/lflow/lflow/pkg/cli/log"
	_ "github.com/mattn/go-sqlite3"
	"github.com/pkg/errors"

	// commands
	"github.com/lflow/lflow/pkg/cli/cmd/export"
	"github.com/lflow/lflow/pkg/cli/cmd/node"
	"github.com/lflow/lflow/pkg/cli/cmd/root"
	"github.com/lflow/lflow/pkg/cli/cmd/server"
	"github.com/lflow/lflow/pkg/cli/cmd/version"
	wfcmd "github.com/lflow/lflow/pkg/cli/cmd/wf"
)

// apiEndpoint and versionTag are populated during link time
var apiEndpoint string
var versionTag = "master"

func main() {
	// the database location comes from the config file alone; there is no
	// flag for it
	ctx, err := infra.Init(versionTag, apiEndpoint)
	if err != nil {
		panic(errors.Wrap(err, "initializing context"))
	}
	defer ctx.DB.Close()

	root.Register(node.NewCmd(*ctx))
	root.Register(export.NewCmd(*ctx))
	root.Register(server.NewCmd(*ctx))
	root.Register(wfcmd.NewCmd(*ctx))
	root.Register(version.NewCmd(*ctx))

	if err := root.Execute(); err != nil {
		log.Errorf("%s\n", err.Error())
		os.Exit(1)
	}
}
