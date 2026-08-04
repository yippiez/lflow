package suggest

import (
	"fmt"
	"os"
	"strings"

	"github.com/lflow/lflow/pkg/tui/context"
	"github.com/lflow/lflow/pkg/tui/database"
	"github.com/lflow/lflow/pkg/tui/infra"
	"github.com/lflow/lflow/pkg/tui/resolve"
	"github.com/lflow/lflow/pkg/utils/log"
	"github.com/pkg/errors"
	"github.com/spf13/cobra"
)

type stateOptions struct {
	message string
	author  string
	strict  bool
}

// newStateCmd returns `lflow suggest complete` or `suggest uncomplete`: a
// reviewable state change that, like every suggestion, leaves the node untouched
// until approval.
func newStateCmd(ctx context.DnoteCtx, complete bool) *cobra.Command {
	opts := &stateOptions{}
	verb := database.SuggestComplete
	short := "Propose marking a node complete"
	if !complete {
		verb = database.SuggestUncomplete
		short = "Propose reopening a completed node"
	}
	cmd := &cobra.Command{
		Use:   verb + " <node>",
		Short: short,
		RunE:  newStateRun(ctx, opts, verb),
	}
	f := cmd.Flags()
	f.StringVar(&opts.message, "message", "", "why the state change is proposed")
	f.StringVar(&opts.author, "author", "", "who is proposing, defaults to $LFLOW_AUTHOR or the OS user")
	f.BoolVar(&opts.strict, "strict", false, "list matches instead of acting on the best match")
	return cmd
}

func newStateRun(ctx context.DnoteCtx, opts *stateOptions, kind string) infra.RunEFunc {
	return func(cmd *cobra.Command, args []string) error {
		if len(args) < 1 {
			return errors.New("missing node reference")
		}
		ref := strings.Join(args, " ")
		r, err := resolve.Resolve(ctx.DB, ref)
		if err != nil {
			if _, ok := err.(resolve.ErrNoMatch); ok {
				resolve.PrintNoMatch(ref)
				os.Exit(1)
			}
			return err
		}
		if opts.strict && r.Total > 1 {
			resolve.PrintMatches(ctx.DB, r.Matches)
			os.Exit(1)
		}

		s := database.Suggestion{
			Kind:       kind,
			TargetUUID: r.Node.UUID,
			BaseName:   r.Node.Name,
			Author:     resolveAuthor(opts.author),
			Message:    opts.message,
		}
		if err := s.Insert(ctx.DB); err != nil {
			return errors.Wrap(err, "storing suggestion")
		}
		log.Successf("suggested %s for %q\n", kind, display(ctx.DB, r.Node.Name))
		fmt.Printf("    %s\n", dim.Sprint(database.ShortID(s.UUID)))
		if r.Total > 1 {
			fmt.Println(dim.Sprintf("    best of %d · --strict lists instead", r.Total))
		}
		fmt.Println(dim.Sprintf("  review with lflow suggest show %s", database.ShortID(s.UUID)))
		return nil
	}
}
