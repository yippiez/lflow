// Package auth provides the `lflow auth` command group for authenticating with
// external providers.
package auth

import (
	"github.com/lflow/lflow/pkg/tui/context"
	"github.com/spf13/cobra"
)

// NewCmd returns the `auth` command with its provider subcommands.
func NewCmd(ctx context.DnoteCtx) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "auth",
		Short: "Authenticate lflow with external providers",
	}

	cmd.AddCommand(newColabCmd(ctx))

	return cmd
}
