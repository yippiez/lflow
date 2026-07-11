// Package auth provides the `lflow auth` command group for authenticating with
// external providers.
package auth

import (
	"github.com/lflow/lflow/pkg/tui/context"
	"github.com/spf13/cobra"
)

// NewCmd returns the `auth` command with its provider subcommands.
//
// The Colab provider (formerly `auth colab`) and its from-scratch runtime were
// removed; provider auth will be reintroduced via a library.
func NewCmd(ctx context.DnoteCtx) *cobra.Command {
	return &cobra.Command{
		Use:   "auth",
		Short: "Authenticate lflow with external providers",
	}
}
