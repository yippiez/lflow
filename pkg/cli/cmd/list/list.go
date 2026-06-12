/* Copyright 2025 Lflow Authors
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

// Package list dumps a node's subtree to stdout for scripting.
package list

import (
	"fmt"
	"os"

	"github.com/fatih/color"
	"github.com/lflow/lflow/pkg/cli/context"
	"github.com/lflow/lflow/pkg/cli/database"
	"github.com/lflow/lflow/pkg/cli/infra"
	"github.com/lflow/lflow/pkg/cli/outline"
	"github.com/lflow/lflow/pkg/cli/resolve"
	"github.com/pkg/errors"
	"github.com/spf13/cobra"
)

type options struct {
	format string
	depth  int
	strict bool
}

var example = `
 * Top-level nodes
 lflow list

 * Markdown outline of a subtree
 lflow list "experiment results" --depth 2

 * JSON for scripting
 lflow list "experiment results" --format json | jq -r .children[0].name`

// NewCmd returns a new list command
func NewCmd(ctx context.DnoteCtx) *cobra.Command {
	opts := &options{}

	cmd := &cobra.Command{
		Use:     "list [node]",
		Short:   "Print a node's subtree",
		Aliases: []string{"ls", "l"},
		Example: example,
		RunE:    newRun(ctx, opts),
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
