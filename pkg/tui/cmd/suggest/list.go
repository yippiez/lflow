package suggest

import (
	"fmt"
	"os"
	"time"

	"github.com/lflow/lflow/pkg/tui/context"
	"github.com/lflow/lflow/pkg/tui/database"
	"github.com/lflow/lflow/pkg/tui/infra"
	"github.com/lflow/lflow/pkg/tui/resolve"
	"github.com/pkg/errors"
	"github.com/spf13/cobra"
)

type listOptions struct {
	status string
	node   string
	kind   string
	format string
	strict bool
}

// newListCmd returns `lflow suggest list`: the review queue, pending first.
func newListCmd(ctx context.DnoteCtx) *cobra.Command {
	opts := &listOptions{}

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List suggestions awaiting review",
		RunE:  newListRun(ctx, opts),
	}

	f := cmd.Flags()
	f.StringVar(&opts.status, "status", database.SuggestPending, "pending, approved, rejected or all")
	f.StringVar(&opts.node, "node", "", "only suggestions about this node (id, id prefix or text)")
	f.StringVar(&opts.kind, "kind", "", "only add, edit, complete or uncomplete suggestions")
	f.StringVar(&opts.format, "format", "text", "output format: text|json")
	f.BoolVar(&opts.strict, "strict", false, "list matches instead of acting on the best match")

	return cmd
}

func newListRun(ctx context.DnoteCtx, opts *listOptions) infra.RunEFunc {
	return func(cmd *cobra.Command, args []string) error {
		db := ctx.DB

		switch opts.status {
		case "", "all", database.SuggestPending, database.SuggestApproved, database.SuggestRejected:
		default:
			return errors.Errorf("unknown status %q: pending, approved, rejected or all", opts.status)
		}
		switch opts.kind {
		case "", database.SuggestAdd, database.SuggestEdit, database.SuggestComplete, database.SuggestUncomplete:
		default:
			return errors.Errorf("unknown kind %q: add, edit, complete or uncomplete", opts.kind)
		}

		filter := database.SuggestionFilter{Status: opts.status, Kind: opts.kind}
		if opts.node != "" {
			r, err := resolve.Resolve(db, opts.node)
			if err != nil {
				if _, ok := err.(resolve.ErrNoMatch); ok {
					resolve.PrintNoMatch(opts.node)
					os.Exit(1)
				}
				return err
			}
			if opts.strict && r.Total > 1 {
				resolve.PrintMatches(db, r.Matches)
				os.Exit(1)
			}
			filter.TargetUUID = r.Node.UUID
		}

		suggestions, err := database.ListSuggestions(db, filter)
		if err != nil {
			return errors.Wrap(err, "listing suggestions")
		}

		if opts.format == "json" {
			if suggestions == nil {
				suggestions = []database.Suggestion{}
			}
			return printJSON(suggestions)
		}
		if opts.format != "text" {
			return errors.Errorf("unknown format %q: text or json", opts.format)
		}

		if len(suggestions) == 0 {
			fmt.Println(dim.Sprint("→ no suggestions"))
			return nil
		}

		for _, s := range suggestions {
			fmt.Printf("%s  %s %-8s %-40s %s\n",
				dim.Sprint(database.ShortID(s.UUID)),
				statusMark(s.Status),
				dim.Sprint(s.Kind),
				truncate(display(db, summary(s)), 40),
				dim.Sprint(meta(db, s)))
		}
		return nil
	}
}

// summary is the one-line gist of what a suggestion proposes.
func summary(s database.Suggestion) string {
	if s.Kind == database.SuggestAdd {
		return s.Name
	}
	if s.Kind == database.SuggestComplete {
		return "mark complete"
	}
	if s.Kind == database.SuggestUncomplete {
		return "reopen"
	}
	if s.Proposes(database.FieldName) {
		return s.Name
	}
	if s.Proposes(database.FieldNote) {
		return "note: " + s.Note
	}
	if s.Proposes(database.FieldType) {
		return "type: " + s.Type
	}
	return ""
}

// meta is the trailing dim column: what the suggestion is about, who made it
// and when.
func meta(db *database.DB, s database.Suggestion) string {
	out := "on " + truncate(targetLine(db, s), 24)
	if s.Author != "" {
		out += " · " + s.Author
	}
	out += " · " + age(s.CreatedOn)
	return out
}

// statusMark renders a status as one colored circle: color carries the verdict
// the way it does everywhere else in the CLI — a yellow open circle is waiting,
// a green filled one landed, a muted open one was turned down.
func statusMark(status string) string {
	switch status {
	case database.SuggestApproved:
		return green.Sprint("●")
	case database.SuggestRejected:
		return dim.Sprint("○")
	default:
		return yellow.Sprint("○")
	}
}

// age renders a timestamp as a compact relative span.
func age(ns int64) string {
	if ns == 0 {
		return "unknown"
	}
	d := time.Since(time.Unix(0, ns))
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}
