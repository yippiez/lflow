// Package node groups the node commands: open, list, add, move, remove
// and edit.
package node

import (
	"github.com/lflow/lflow/tui/cli/add"
	"github.com/lflow/lflow/tui/cli/grep"
	"github.com/lflow/lflow/tui/cli/list"
	"github.com/lflow/lflow/tui/cli/mv"
	"github.com/lflow/lflow/tui/cli/open"
	"github.com/lflow/lflow/tui/cli/remove"
	"github.com/lflow/lflow/tui/runtime"
	"github.com/spf13/cobra"
)

// NewCmd returns the node command group.
func NewCmd(ctx runtime.Ctx) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "node",
		Short: "Open, list, create, move, edit and delete nodes",
	}

	cmd.AddCommand(open.NewCmd(ctx))
	cmd.AddCommand(list.NewCmd(ctx))
	cmd.AddCommand(add.NewCmd(ctx))
	cmd.AddCommand(grep.NewCmd(ctx))
	cmd.AddCommand(mv.NewCmd(ctx))
	cmd.AddCommand(remove.NewCmd(ctx))
	cmd.AddCommand(newEditCmd(ctx))

	return cmd
}
