// Package node groups the node commands: open, list, add, move, remove
// and edit.
package node

import (
	"github.com/lflow/lflow/pkg/cli/cmd/add"
	"github.com/lflow/lflow/pkg/cli/cmd/list"
	"github.com/lflow/lflow/pkg/cli/cmd/mv"
	"github.com/lflow/lflow/pkg/cli/cmd/open"
	"github.com/lflow/lflow/pkg/cli/cmd/remove"
	"github.com/lflow/lflow/pkg/cli/context"
	"github.com/spf13/cobra"
)

// NewCmd returns the node command group.
func NewCmd(ctx context.DnoteCtx) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "node",
		Short: "Open, list, create, move, edit and delete nodes",
	}

	cmd.AddCommand(open.NewCmd(ctx))
	cmd.AddCommand(list.NewCmd(ctx))
	cmd.AddCommand(add.NewCmd(ctx))
	cmd.AddCommand(mv.NewCmd(ctx))
	cmd.AddCommand(remove.NewCmd(ctx))
	cmd.AddCommand(newEditCmd(ctx))

	return cmd
}
