// Package remove tombstones a node subtree (pushed as a delete on sync).
package remove

import (
	"fmt"
	"os"
	"strings"

	"github.com/lflow/lflow/pkg/cli/context"
	"github.com/lflow/lflow/pkg/cli/database"
	"github.com/lflow/lflow/pkg/cli/infra"
	"github.com/lflow/lflow/pkg/cli/log"
	"github.com/lflow/lflow/pkg/cli/resolve"
	"github.com/lflow/lflow/pkg/cli/ui"
	"github.com/pkg/errors"
	"github.com/spf13/cobra"
)

type options struct {
	force  bool
	strict bool
}

// NewCmd returns a new rm command
func NewCmd(ctx context.DnoteCtx) *cobra.Command {
	opts := &options{}

	cmd := &cobra.Command{
		Use:   "remove <node>",
		Short: "Delete a node and its subtree",
		RunE:  newRun(ctx, opts),
	}

	f := cmd.Flags()
	f.BoolVarP(&opts.force, "force", "f", false, "skip confirmation")
	f.BoolVar(&opts.strict, "strict", false, "list matches instead of acting on the best match")

	return cmd
}

func newRun(ctx context.DnoteCtx, opts *options) infra.RunEFunc {
	return func(cmd *cobra.Command, args []string) error {
		if len(args) < 1 {
			return errors.New("missing node reference")
		}
		ref := strings.Join(args, " ")
		db := ctx.DB

		r, err := resolve.Resolve(db, ref)
		if err != nil {
			if _, ok := err.(resolve.ErrNoMatch); ok {
				resolve.PrintNoMatch(ref)
				os.Exit(1)
			}
			return err
		}

		if opts.strict && r.Total > 1 {
			resolve.PrintMatches(db, r.Matches)
			os.Exit(1)
		}

		count, err := database.CountSubtree(db, r.Node.UUID)
		if err != nil {
			return errors.Wrap(err, "counting subtree")
		}

		if !opts.force {
			confirmed, err := ui.Confirm(fmt.Sprintf("delete %q · %d nodes?", r.Node.Name, count), false)
			if err != nil {
				return errors.Wrap(err, "getting confirmation")
			}
			if !confirmed {
				return nil
			}
		}

		tx, err := db.Begin()
		if err != nil {
			return errors.Wrap(err, "beginning a transaction")
		}
		deleted, err := database.MarkSubtreeDeleted(tx, r.Node.UUID)
		if err != nil {
			tx.Rollback()
			return errors.Wrap(err, "deleting subtree")
		}
		if err := tx.Commit(); err != nil {
			return errors.Wrap(err, "committing transaction")
		}

		log.Successf("deleted %q (%d nodes)\n", r.Node.Name, deleted)

		return nil
	}
}
