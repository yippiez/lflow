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

// Package find searches for a node and opens the inline editor on the best
// match. With --print it dumps the outline to stdout instead.
package find

import (
	"fmt"
	"os"
	"strings"

	"github.com/lflow/lflow/pkg/cli/context"
	"github.com/lflow/lflow/pkg/cli/editor"
	"github.com/lflow/lflow/pkg/cli/infra"
	"github.com/lflow/lflow/pkg/cli/outline"
	"github.com/lflow/lflow/pkg/cli/resolve"
	"github.com/pkg/errors"
	"github.com/spf13/cobra"
)

type options struct {
	print  bool
	strict bool
	all    bool
	idOnly bool
}

var example = `
 * Open the inline editor on the best match
 lflow find "experiment results"

 * Print the outline instead of opening the editor
 lflow find "experiment results" --print

 * List all matches instead of acting
 lflow find exp --strict`

// NewCmd returns a new find command
func NewCmd(ctx context.DnoteCtx) *cobra.Command {
	opts := &options{}

	cmd := &cobra.Command{
		Use:     "find <query>",
		Short:   "Find a node and open the inline editor on it",
		Aliases: []string{"f"},
		Example: example,
		RunE:    newRun(ctx, opts),
	}

	f := cmd.Flags()
	f.BoolVar(&opts.print, "print", false, "print the outline instead of opening the editor")
	f.BoolVar(&opts.strict, "strict", false, "list matches instead of opening the best one")
	f.BoolVar(&opts.all, "all", false, "include completed nodes")
	f.BoolVar(&opts.idOnly, "id", false, "print only the node id of the best match")

	return cmd
}

func newRun(ctx context.DnoteCtx, opts *options) infra.RunEFunc {
	return func(cmd *cobra.Command, args []string) error {
		if len(args) < 1 {
			return errors.New("missing search query")
		}
		query := strings.Join(args, " ")
		db := ctx.DB

		r, err := resolve.Resolve(db, query, opts.all)
		if err != nil {
			if _, ok := err.(resolve.ErrNoMatch); ok {
				resolve.PrintNoMatch(query)
				os.Exit(1)
			}
			return err
		}

		if opts.strict && r.Total > 1 {
			resolve.PrintMatches(db, r.Matches)
			return nil
		}

		if opts.idOnly {
			fmt.Println(r.Node.UUID)
			return nil
		}

		if opts.print {
			out, err := outline.RenderMarkdown(db, r.Node, -1, opts.all)
			if err != nil {
				return errors.Wrap(err, "rendering outline")
			}
			if out != "" {
				fmt.Println(out)
			}
			return nil
		}

		resolve.Feedback("opening", r)

		return editor.Run(ctx, r.Node.UUID)
	}
}
