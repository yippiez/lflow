package suggest

import (
	"fmt"

	"github.com/lflow/lflow/tui/cli/infra"
	"github.com/lflow/lflow/tui/database"
	"github.com/lflow/lflow/tui/database/resolve"
	"github.com/lflow/lflow/tui/runtime"
	"github.com/lflow/lflow/tui/utils/log"
	"github.com/pkg/errors"
	"github.com/spf13/cobra"
)

type reviewOptions struct {
	all   bool
	force bool
	node  string
}

// newReviewCmd returns `lflow suggest approve` or `lflow suggest reject` —
// the same walk over a set of suggestions, differing only in the verdict.
// Approving applies the proposal to the outline; rejecting never touches it.
func newReviewCmd(ctx runtime.Ctx, verdict string) *cobra.Command {
	opts := &reviewOptions{}

	approve := verdict == database.SuggestApproved
	use, short, aliases := "reject <id>...", "Reject suggestions, leaving the outline untouched", []string{"disapprove", "deny"}
	if approve {
		use, short, aliases = "approve <id>...", "Approve suggestions, applying them to the outline", []string{"accept"}
	}

	cmd := &cobra.Command{
		Use:     use,
		Short:   short,
		Aliases: aliases,
		RunE:    newReviewRun(ctx, opts, verdict),
	}

	f := cmd.Flags()
	f.BoolVar(&opts.all, "all", false, "act on every pending suggestion")
	f.StringVar(&opts.node, "node", "", "with --all, only suggestions about this node")
	if approve {
		f.BoolVar(&opts.force, "force", false, "apply even when the target node changed since the suggestion was made")
	}

	return cmd
}

func newReviewRun(ctx runtime.Ctx, opts *reviewOptions, verdict string) infra.RunEFunc {
	return func(cmd *cobra.Command, args []string) error {
		db := ctx.DB

		targets, err := reviewSet(db, opts, args)
		if err != nil {
			return err
		}
		if len(targets) == 0 {
			fmt.Println(dim.Sprint("→ no pending suggestions"))
			return nil
		}

		var done int
		for _, s := range targets {
			if !s.Pending() {
				log.Warnf("skipped %s · already %s\n", database.ShortID(s.UUID), s.Status)
				continue
			}
			if verdict == database.SuggestRejected {
				if err := database.RejectSuggestion(db, s); err != nil {
					return err
				}
				done++
				fmt.Printf("  %s %s  %s\n", red.Sprint("→"), dim.Sprint(database.ShortID(s.UUID)), truncate(display(db, summary(s)), 50))
				continue
			}

			// a proposal about a node that was deleted can never be applied:
			// skip it so one zombie does not abort the whole batch
			if gone, gerr := database.TargetGone(db, s); gerr == nil && gone {
				log.Warnf("skipped %s · its target was deleted\n", database.ShortID(s.UUID))
				continue
			}

			// a stale proposal would silently overwrite a newer edit, so it
			// stops here unless the reviewer says otherwise
			if !opts.force {
				drifted, derr := database.TargetDrifted(db, s)
				if derr == nil && drifted {
					log.Warnf("skipped %s · the node changed since it was suggested · --force applies anyway\n",
						database.ShortID(s.UUID))
					continue
				}
			}

			applied, err := database.ApplySuggestion(db, s)
			if err != nil {
				return errors.Wrapf(err, "approving %s", database.ShortID(s.UUID))
			}
			done++
			fmt.Printf("  %s %s  %s\n", green.Sprint("→"), dim.Sprint(database.ShortID(applied.UUID)), truncate(display(db, summary(applied)), 50))
		}

		if verdict == database.SuggestApproved {
			log.Successf("approved %s\n", resolve.CountNoun(done, "suggestion"))
		} else {
			log.Successf("rejected %s\n", resolve.CountNoun(done, "suggestion"))
		}
		return nil
	}
}

// reviewSet is the suggestions a review command acts on: the referenced ids,
// or every pending one under --all.
func reviewSet(db *database.DB, opts *reviewOptions, args []string) ([]database.Suggestion, error) {
	if opts.all {
		if len(args) > 0 {
			return nil, errors.New("--all takes no ids")
		}
		filter := database.SuggestionFilter{Status: database.SuggestPending}
		if opts.node != "" {
			r, err := resolve.Resolve(db, opts.node)
			if err != nil {
				if _, ok := err.(resolve.ErrNoMatch); ok {
					resolve.PrintNoMatch(opts.node)
					return nil, errors.New("no such node")
				}
				return nil, err
			}
			filter.TargetUUID = r.Node.UUID
		}
		return database.ListSuggestions(db, filter)
	}

	if len(args) == 0 {
		return nil, errors.New("missing suggestion id: pass ids or --all")
	}
	var out []database.Suggestion
	for _, ref := range args {
		out = append(out, resolveRef(db, ref))
	}
	return out, nil
}
