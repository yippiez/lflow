package main

import (
	"os"

	_ "github.com/lflow/lflow/pkg/tui/editor/nodes" // register the pluggable node types
	"github.com/lflow/lflow/pkg/tui/infra"
	"github.com/lflow/lflow/pkg/utils/log"
	_ "github.com/mattn/go-sqlite3"
	"github.com/pkg/errors"

	// commands
	"github.com/lflow/lflow/pkg/tui/cmd/auth"
	"github.com/lflow/lflow/pkg/tui/cmd/export"
	"github.com/lflow/lflow/pkg/tui/cmd/node"
	"github.com/lflow/lflow/pkg/tui/cmd/root"
	"github.com/lflow/lflow/pkg/tui/cmd/serve"
	"github.com/lflow/lflow/pkg/tui/cmd/version"
)

// versionTag is populated during link time
var versionTag = "master"

func main() {
	// the daemon itself must never route through a daemon: `lflow serve`
	// skips client init entirely and owns the database directly
	if len(os.Args) > 1 && os.Args[1] == "serve" {
		root.Register(serve.NewCmd(versionTag))
		if err := root.Execute(); err != nil {
			log.Errorf("%s\n", err.Error())
			os.Exit(1)
		}
		return
	}

	// the database location comes from the config file alone; there is no
	// flag for it
	ctx, err := infra.Init(versionTag)
	if err != nil {
		panic(errors.Wrap(err, "initializing context"))
	}
	defer ctx.DB.Close()

	root.Register(node.NewCmd(*ctx))
	root.Register(auth.NewCmd(*ctx))
	root.Register(export.NewCmd(*ctx))
	root.Register(version.NewCmd(*ctx))
	root.Register(serve.NewCmd(versionTag)) // listed in --help; runs via the early path

	if err := root.Execute(); err != nil {
		log.Errorf("%s\n", err.Error())
		os.Exit(1)
	}
}
