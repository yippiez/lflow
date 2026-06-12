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

// Package open launches the inline editor on a node by id (or on the root
// when no argument is given).
package open

import (
	"os"
	"strings"

	"github.com/lflow/lflow/pkg/cli/context"
	"github.com/lflow/lflow/pkg/cli/database"
	"github.com/lflow/lflow/pkg/cli/editor"
	"github.com/lflow/lflow/pkg/cli/resolve"
	"github.com/spf13/cobra"
)

// NewCmd returns a new open command
func NewCmd(ctx context.DnoteCtx) *cobra.Command {
	var all bool

	cmd := &cobra.Command{
		Use:     "open [node]",
		Short:   "Open the editor on a node (root when no node is given)",
		Long:    "Open the inline editor on a node by id or query. With no argument, open the root.",
		Aliases: []string{"o", "e", "f", "edit", "find"},
		RunE: func(cmd *cobra.Command, args []string) error {
			db := ctx.DB
			if err := database.EnsureRoot(db); err != nil {
				return err
			}

			// no argument: open the root
			if len(args) == 0 {
				return editor.Run(ctx, database.RootUUID)
			}

			ref := strings.Join(args, " ")
			r, err := resolve.Resolve(db, ref, all)
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

	cmd.Flags().BoolVar(&all, "all", false, "include completed nodes")

	return cmd
}
