package suggest

import (
	"fmt"
	"os"
	"strings"

	"github.com/lflow/lflow/tui/cli/infra"
	"github.com/lflow/lflow/tui/database"
	"github.com/lflow/lflow/tui/database/resolve"
	"github.com/lflow/lflow/tui/runtime"
	"github.com/lflow/lflow/tui/utils/log"
	"github.com/pkg/errors"
	"github.com/spf13/cobra"
)

type removeOptions struct {
	message string
	author  string
	strict  bool
}

// newRemoveCmd returns `lflow suggest remove`: propose deleting a node and
// everything under it. Like every suggestion it changes nothing — the node is
// still there, marked as proposed-for-removal, until somebody approves.
func newRemoveCmd(ctx runtime.Ctx) *cobra.Command {
	opts := &removeOptions{}

	cmd := &cobra.Command{
		Use:   "remove <node>",
		Short: "Propose deleting a node and its subtree",
		RunE:  newRemoveRun(ctx, opts),
	}

	f := cmd.Flags()
	f.StringVar(&opts.message, "message", "", "why the removal is proposed")
	f.StringVar(&opts.author, "author", "", "who is proposing, defaults to $LFLOW_AUTHOR or the OS user")
	f.BoolVar(&opts.strict, "strict", false, "list matches instead of acting on the best match")

	return cmd
}

func newRemoveRun(ctx runtime.Ctx, opts *removeOptions) infra.RunEFunc {
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

		s := database.Suggestion{
			Kind:       database.SuggestRemove,
			TargetUUID: r.Node.UUID,
			BaseName:   r.Node.Name,
			BaseNote:   r.Node.Note,
			Author:     resolveAuthor(opts.author),
			Message:    opts.message,
		}
		if err := s.Insert(db); err != nil {
			return errors.Wrap(err, "storing suggestion")
		}

		log.Successf("suggested removing %q\n", display(db, r.Node.Name))
		fmt.Printf("    %s\n", dim.Sprint(database.ShortID(s.UUID)))
		if n, err := database.CountSubtree(db, r.Node.UUID); err == nil && n > 1 {
			fmt.Println(dim.Sprintf("    takes %s with it", resolve.CountNoun(n-1, "node")))
		}
		if r.Total > 1 {
			fmt.Println(dim.Sprintf("    best of %d · --strict lists instead", r.Total))
		}
		fmt.Println(dim.Sprintf("  review with lflow suggest show %s", database.ShortID(s.UUID)))

		return nil
	}
}
