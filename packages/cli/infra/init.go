// Package infra provides operations and definitions for the
// local infrastructure for Dnote
package infra

import (
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/lflow/lflow/packages/cli/config"
	"github.com/lflow/lflow/packages/daemon/client"
	"github.com/lflow/lflow/packages/database"
	"github.com/lflow/lflow/packages/tui"
	"github.com/lflow/lflow/packages/utils"
	"github.com/lflow/lflow/packages/utils/consts"
	"github.com/lflow/lflow/packages/utils/dirs"
	"github.com/lflow/lflow/packages/utils/log"
	"github.com/pkg/errors"
	"github.com/spf13/cobra"
)

// RunEFunc is a function type of lflow commands
type RunEFunc func(*cobra.Command, []string) error

func getDBPath(paths tui.Paths, customPath string) string {
	// If custom path is provided, use it
	if customPath != "" {
		return customPath
	}

	return fmt.Sprintf("%s/%s/%s", paths.Data, consts.LflowDirName, consts.LflowDBFileName)
}

// resolvePaths returns the standard lflow directories.
func resolvePaths() tui.Paths {
	return tui.Paths{
		Home:   dirs.Home,
		Config: dirs.ConfigHome,
		Data:   dirs.DataHome,
		Cache:  dirs.CacheHome,
	}
}

// ResolveDBPath resolves the database location: the config override or the
// standard data dir.
func ResolveDBPath() (string, error) {
	paths := resolvePaths()
	// the config file is the only way to relocate the database; on a first
	// run the file does not exist yet and the standard location is used
	customDBPath := ""
	if cf, err := config.Read(tui.Ctx{Paths: paths}); err == nil {
		customDBPath = cf.DBPath
	}
	return getDBPath(paths, customDBPath), nil
}

// PrepareDB creates the schema and system rows. The daemon runs it once at
// startup as the database's single owner; a direct (LFLOW_NO_DAEMON) run does
// it for itself.
func PrepareDB(db *database.DB, versionTag string) error {
	ctx := tui.Ctx{Paths: resolvePaths(), Version: versionTag, DB: db}
	if err := initDB(ctx); err != nil {
		return errors.Wrap(err, "initializing database")
	}
	if err := initSystem(ctx); err != nil {
		return errors.Wrap(err, "initializing system data")
	}
	return nil
}

// clientName labels this process in daemon logs and change events.
func clientName() string {
	for _, a := range os.Args[1:] {
		if a == "open" {
			return "editor"
		}
	}
	return "cli"
}

// Init initializes the lflow environment and returns a new lflow context.
// Normal runs connect to the daemon (spawning it when absent) — the daemon
// is the only process that opens the SQLite file, so every client sees every
// change live. LFLOW_NO_DAEMON=1 opens the file directly instead.
func Init(versionTag string) (*tui.Ctx, error) {
	ctx := tui.Ctx{Paths: resolvePaths(), Version: versionTag}

	if err := initFiles(ctx); err != nil {
		return nil, errors.Wrap(err, "initializing files")
	}

	dbPath, err := ResolveDBPath()
	if err != nil {
		return nil, errors.Wrap(err, "resolving db path")
	}

	if os.Getenv("LFLOW_NO_DAEMON") != "" {
		db, err := database.Open(dbPath)
		if err != nil {
			return nil, errors.Wrap(err, "connecting to db")
		}
		ctx.DB = db
		if err := PrepareDB(db, versionTag); err != nil {
			return nil, err
		}
	} else {
		cl, err := client.Ensure(dbPath, clientName(), versionTag)
		if err != nil {
			return nil, errors.Wrap(err, "connecting to the lflow daemon")
		}
		ctx.DB = cl.DB()
		ctx.Live = cl
	}

	ctx, err = setupCtx(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "setting up the context")
	}

	log.Debug("context: %+v\n", ctx)

	return &ctx, nil
}

// setupCtx enriches the base context with values from config file and database.
// This is called after files and database have been initialized.
func setupCtx(ctx tui.Ctx) (tui.Ctx, error) {
	cf, err := config.Read(ctx)
	if err != nil {
		return ctx, errors.Wrap(err, "reading config")
	}

	ret := tui.Ctx{
		Paths:              ctx.Paths,
		Version:            ctx.Version,
		DB:                 ctx.DB,
		Live:               ctx.Live,
		Editor:             cf.Editor,
		EnableUpgradeCheck: cf.EnableUpgradeCheck,
	}

	return ret, nil
}

// initDB initializes the database.
//
// lflow has no migrations: a fresh database is created by applying the
// canonical schema.sql wholesale, and an existing one is left untouched.
func initDB(ctx tui.Ctx) error {
	log.Debug("initializing the database\n")

	db := ctx.DB

	// if the node model already exists, the database is already initialized
	var nodesCount int
	if err := db.QueryRow("SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = 'nodes'").Scan(&nodesCount); err != nil {
		return errors.Wrap(err, "checking for nodes table")
	}
	if nodesCount > 0 {
		return nil
	}

	if _, err := db.Exec(database.DefaultSchemaSQL()); err != nil {
		return errors.Wrap(err, "applying schema.sql")
	}

	return nil
}

func initSystemKV(db *database.DB, key string, val string) error {
	var count int
	if err := db.QueryRow("SELECT count(*) FROM system WHERE key = ?", key).Scan(&count); err != nil {
		return errors.Wrapf(err, "counting %s", key)
	}

	if count > 0 {
		return nil
	}

	if _, err := db.Exec("INSERT INTO system (key, value) VALUES (?, ?)", key, val); err != nil {
		return errors.Wrapf(err, "inserting %s %s", key, val)
	}

	return nil
}

// initSystem inserts system data if missing
func initSystem(ctx tui.Ctx) error {
	log.Debug("initializing the system\n")

	db := ctx.DB

	tx, err := db.Begin()
	if err != nil {
		return errors.Wrap(err, "beginning a transaction")
	}
	// the caller owns the transaction: any failure rolls everything back, and
	// rolling back an already-committed transaction is a no-op
	defer tx.Rollback()

	nowStr := strconv.FormatInt(time.Now().Unix(), 10)
	if err := initSystemKV(tx, consts.SystemLastUpgrade, nowStr); err != nil {
		return errors.Wrapf(err, "initializing system config for %s", consts.SystemLastUpgrade)
	}

	if err := tx.Commit(); err != nil {
		return errors.Wrap(err, "committing transaction")
	}

	return nil
}

// getEditorCommand returns the system's editor command with appropriate flags,
// if necessary, to make the command wait until editor is close to exit.
func getEditorCommand() string {
	editor := os.Getenv("EDITOR")

	var ret string

	switch editor {
	case "atom":
		ret = "atom -w"
	case "subl":
		ret = "subl -n -w"
	case "code":
		ret = "code -n -w"
	case "mate":
		ret = "mate -w"
	case "vim":
		ret = "vim"
	case "nano":
		ret = "nano"
	case "emacs":
		ret = "emacs"
	case "nvim":
		ret = "nvim"
	default:
		ret = "vi"
	}

	return ret
}

// initConfigFile populates a new config file if it does not exist yet
func initConfigFile(ctx tui.Ctx) error {
	path := config.GetPath(ctx)
	ok, err := utils.FileExists(path)
	if err != nil {
		return errors.Wrap(err, "checking if config exists")
	}
	if ok {
		return nil
	}

	cf := config.Config{
		Editor:             getEditorCommand(),
		EnableUpgradeCheck: true,
	}

	if err := config.Write(ctx, cf); err != nil {
		return errors.Wrap(err, "writing config")
	}

	return nil
}

// initFiles creates, if necessary, the lflow directory and files inside
func initFiles(ctx tui.Ctx) error {
	if err := tui.InitLflowDirs(ctx.Paths); err != nil {
		return errors.Wrap(err, "creating the lflow dir")
	}
	if err := initConfigFile(ctx); err != nil {
		return errors.Wrap(err, "generating the config file")
	}

	return nil
}
