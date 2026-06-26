// Package serve exposes `lflow serve`: run the local outline as a live server
// that the mobile app (and, later, the desktop TUI) connects to over WebSocket.
package serve

import (
	"github.com/lflow/lflow/pkg/tui/context"
	"github.com/lflow/lflow/pkg/tui/infra"
	"github.com/lflow/lflow/pkg/tui/live"
	"github.com/spf13/cobra"
)

type options struct {
	port int
}

// NewCmd returns the serve command.
func NewCmd(ctx context.DnoteCtx) *cobra.Command {
	opts := &options{}

	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Serve the local outline to mobile/other clients over the LAN",
		RunE:  newRun(ctx, opts),
	}

	cmd.Flags().IntVar(&opts.port, "port", 8765, "TCP port to listen on")

	return cmd
}

func newRun(ctx context.DnoteCtx, opts *options) infra.RunEFunc {
	return func(cmd *cobra.Command, args []string) error {
		return live.Serve(ctx, opts.port)
	}
}
