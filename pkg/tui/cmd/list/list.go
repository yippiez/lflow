// Package list dumps a node's subtree to stdout for scripting.
package list

import (
	"fmt"
	"os"

	"github.com/fatih/color"
	"github.com/lflow/lflow/pkg/tui/context"
	"github.com/lflow/lflow/pkg/tui/database"
	"github.com/lflow/lflow/pkg/tui/infra"
	"github.com/lflow/lflow/pkg/tui/outline"
	"github.com/lflow/lflow/pkg/tui/resolve"
	"github.com/pkg/errors"
	"github.com/spf13/cobra"
)

type options struct {
	format string
	depth  int
	strict bool
}

// NewCmd returns a new list command
func NewCmd(ctx context.DnoteCtx) *cobra.Command {
	opts := &options{}

	cmd := &cobra.Command{
		Use:   "list [node]",
		Short: "Print a node's subtree, or the top-level nodes with no argument",
		RunE:  newRun(ctx, opts),
	}

	f := cmd.Flags()
	f.StringVar(&opts.format, "format", "md", "output format: md|text|json")
	f.IntVar(&opts.depth, "depth", -1, "maximum depth, -1 means unlimited")
	f.BoolVar(&opts.strict, "strict", false, "list matches instead of acting on the best match")

	return cmd
}

func listRoots(db *database.DB) error {
	dim := color.New(color.FgHiBlack)
	if err := database.EnsureRoot(db); err != nil {
		return err
	}
	roots, err := database.GetChildren(db, database.RootUUID)
	if err != nil {
		return errors.Wrap(err, "querying top-level nodes")
	}
	for _, n := range roots {
		count, err := database.CountSubtree(db, n.UUID)
		if err != nil {
			count = 1
		}
		shortID := n.UUID
		if len(shortID) > 6 {
			shortID = shortID[:6]
		}
		fmt.Printf("%s  %-40s %s\n", dim.Sprint(shortID), n.Name, dim.Sprintf("%s · %s", n.Layout, resolve.CountNoun(count, "node")))
	}
	return nil
}

func newRun(ctx context.DnoteCtx, opts *options) infra.RunEFunc {
	return func(cmd *cobra.Command, args []string) error {
		db := ctx.DB

		if len(args) < 1 {
			// no reference: list the top level (children of root)
			return listRoots(db)
		}
		ref := args[0]

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

		var out string
		switch opts.format {
		case "md":
			out, err = outline.RenderMarkdown(db, r.Node, opts.depth, true)
		case "text":
			out, err = outline.RenderText(db, r.Node, opts.depth, true)
		case "json":
			out, err = outline.RenderJSON(db, r.Node, opts.depth, true)
		default:
			return errors.Errorf("unknown format %q: md, text or json", opts.format)
		}
		if err != nil {
			return errors.Wrap(err, "rendering outline")
		}

		if out != "" {
			fmt.Println(out)
		}

		return nil
	}
}
