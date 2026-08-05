package main

import (
	"os"

	"github.com/lflow/lflow/packages/cli/infra"
	"github.com/lflow/lflow/packages/utils/log"
	_ "github.com/mattn/go-sqlite3"
	"github.com/pkg/errors"

	// commands
	"github.com/lflow/lflow/packages/cli/auth"
	"github.com/lflow/lflow/packages/cli/export"
	"github.com/lflow/lflow/packages/cli/file"
	"github.com/lflow/lflow/packages/cli/node"
	"github.com/lflow/lflow/packages/cli/root"
	"github.com/lflow/lflow/packages/cli/serve"
	"github.com/lflow/lflow/packages/cli/suggest"
	"github.com/lflow/lflow/packages/cli/version"
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
	root.Register(suggest.NewCmd(*ctx))
	root.Register(file.NewCmd(*ctx))
	root.Register(auth.NewCmd(*ctx))
	root.Register(export.NewCmd(*ctx))
	root.Register(version.NewCmd(*ctx))
	root.Register(serve.NewCmd(versionTag)) // listed in --help; runs via the early path

	if err := root.Execute(); err != nil {
		log.Errorf("%s\n", err.Error())
		os.Exit(1)
	}
}
