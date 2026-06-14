// Package server groups the lflow-server commands: login, logout and sync.
package server

import (
	"github.com/lflow/lflow/pkg/tui/cmd/login"
	"github.com/lflow/lflow/pkg/tui/cmd/logout"
	"github.com/lflow/lflow/pkg/tui/cmd/sync"
	"github.com/lflow/lflow/pkg/tui/context"
	"github.com/spf13/cobra"
)

// NewCmd returns the server command group.
func NewCmd(ctx context.DnoteCtx) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "server",
		Short: "Log in to and sync with a self-hosted lflow server",
	}

	cmd.AddCommand(login.NewCmd(ctx))
	cmd.AddCommand(logout.NewCmd(ctx))
	cmd.AddCommand(sync.NewCmd(ctx))

	return cmd
}
