// Package open launches the inline editor on a node by id (or on the root
// when no argument is given).
package open

import (
	"os"
	"strings"

	"github.com/lflow/lflow/tui/database"
	"github.com/lflow/lflow/tui/database/resolve"
	"github.com/lflow/lflow/tui/editor"
	"github.com/lflow/lflow/tui/runtime"
	"github.com/spf13/cobra"
)

// NewCmd returns a new open command
func NewCmd(ctx runtime.Ctx) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "open [node]",
		Short: "Open the inline editor on a node, or the root when no node is given",
		RunE: func(cmd *cobra.Command, args []string) error {
			db := ctx.DB
			if err := database.EnsureRoot(db); err != nil {
				return err
			}
			if err := database.EnsureTemp(db); err != nil {
				return err
			}
			// retention sweep: drop temp entries unchanged for 7 days
			if _, err := database.SweepTempExpired(db, database.TempRetention); err != nil {
				return err
			}

			// no argument: open the root
			if len(args) == 0 {
				return editor.Run(ctx, database.RootUUID)
			}

			ref := strings.Join(args, " ")
			r, err := resolve.Resolve(db, ref)
			if err != nil {
				if _, ok := err.(resolve.ErrNoMatch); ok {
					resolve.PrintNoMatch(ref)
					os.Exit(1)
				}
				return err
			}

			resolve.Feedback("opening", r)
			return editor.Run(ctx, r.Node.UUID)
		},
	}

	return cmd
}
