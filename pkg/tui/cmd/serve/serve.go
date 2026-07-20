// Package serve implements `lflow serve`: run the lflow daemon. Foreground
// serve prints one log line per server event; the auto-spawned background
// daemon runs the same server with --quiet --idle. The daemon is the single
// process that owns the SQLite file — every CLI command and editor is a
// client of it.
package serve

import (
	"os"
	"path/filepath"
	"time"

	"github.com/lflow/lflow/pkg/tui/client"
	"github.com/lflow/lflow/pkg/tui/daemon"
	"github.com/lflow/lflow/pkg/tui/infra"
	"github.com/pkg/errors"
	"github.com/spf13/cobra"
)

// idleExit is how long the background daemon lingers with no clients.
const idleExit = 10 * time.Minute

// NewCmd builds the serve command. It does not take a DnoteCtx: the daemon
// must never route through a daemon, so it resolves the database itself.
func NewCmd(versionTag string) *cobra.Command {
	var (
		quiet     bool
		idle      bool
		dbPath    string
		sock      string
		httpAddr  string
		httpToken string
	)

	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Run the lflow daemon and log its events",
		RunE: func(cmd *cobra.Command, args []string) error {
			if dbPath == "" {
				var err error
				dbPath, err = infra.ResolveDBPath()
				if err != nil {
					return errors.Wrap(err, "resolving database path")
				}
			}
			if sock == "" {
				sock = client.SockPath(dbPath)
			}
			// a fresh system: serve may be the first lflow process ever
			if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
				return errors.Wrap(err, "creating data dir")
			}

			store, err := daemon.OpenStore(dbPath)
			if err != nil {
				return errors.Wrap(err, "opening database")
			}
			defer store.Close()

			// the daemon owns schema and migrations: they run once here, not
			// in every client
			if err := infra.PrepareDB(store.DB(), versionTag); err != nil {
				return errors.Wrap(err, "preparing database")
			}

			opts := daemon.Options{Sock: sock, Version: versionTag, HTTP: httpAddr, HTTPToken: httpToken}
			if idle {
				opts.Idle = idleExit
			}
			if !quiet {
				opts.Log = os.Stdout
			}
			return daemon.Serve(store, opts)
		},
	}

	cmd.Flags().BoolVar(&quiet, "quiet", false, "no event log")
	cmd.Flags().BoolVar(&idle, "idle", false, "exit after 10m with no clients")
	cmd.Flags().StringVar(&dbPath, "db", "", "database file (default: the configured database)")
	cmd.Flags().StringVar(&sock, "sock", "", "socket path (default: daemon.sock next to the database)")
	cmd.Flags().StringVar(&httpAddr, "http", "", "also serve the HTTP API + mobile web app on this address (e.g. :7420)")
	cmd.Flags().StringVar(&httpToken, "http-token", "", "bearer token required by the HTTP API (default: open)")

	return cmd
}
