// Package node groups the node commands: open, list, add, move, remove,
// edit and install.
package node

import (
	"github.com/lflow/lflow/pkg/tui/cmd/add"
	"github.com/lflow/lflow/pkg/tui/cmd/grep"
	"github.com/lflow/lflow/pkg/tui/cmd/install"
	"github.com/lflow/lflow/pkg/tui/cmd/list"
	"github.com/lflow/lflow/pkg/tui/cmd/mv"
	"github.com/lflow/lflow/pkg/tui/cmd/open"
	"github.com/lflow/lflow/pkg/tui/cmd/remove"
	"github.com/lflow/lflow/pkg/tui/context"
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
	cmd.AddCommand(grep.NewCmd(ctx))
	cmd.AddCommand(install.NewCmd(ctx))
	cmd.AddCommand(mv.NewCmd(ctx))
	cmd.AddCommand(remove.NewCmd(ctx))
	cmd.AddCommand(newEditCmd(ctx))

	return cmd
}
