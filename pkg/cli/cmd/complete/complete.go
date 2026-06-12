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

// Package complete toggles a node's completed state.
package complete

import (
	"os"
	"strings"
	"time"

	"github.com/lflow/lflow/pkg/cli/context"
	"github.com/lflow/lflow/pkg/cli/infra"
	"github.com/lflow/lflow/pkg/cli/log"
	"github.com/lflow/lflow/pkg/cli/resolve"
	"github.com/pkg/errors"
	"github.com/spf13/cobra"
)

// NewCmd returns a new complete command
func NewCmd(ctx context.DnoteCtx) *cobra.Command {
	return newCmd(ctx, true)
}

// NewUncompleteCmd returns a new uncomplete command
func NewUncompleteCmd(ctx context.DnoteCtx) *cobra.Command {
	return newCmd(ctx, false)
}

func newCmd(ctx context.DnoteCtx, complete bool) *cobra.Command {
	var strict bool

	use := "complete <node>"
	short := "Mark a node completed"
	if !complete {
		use = "uncomplete <node>"
		short = "Mark a node not completed"
	}

	cmd := &cobra.Command{
		Use:   use,
		Short: short,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) < 1 {
				return errors.New("missing node reference")
			}
			ref := strings.Join(args, " ")
			db := ctx.DB

			r, err := resolve.Resolve(db, ref, true)
			if err != nil {
				if _, ok := err.(resolve.ErrNoMatch); ok {
					resolve.PrintNoMatch(ref)
					os.Exit(1)
				}
				return err
			}

			if strict && r.Total > 1 {
				resolve.PrintMatches(db, r.Matches)
				os.Exit(1)
			}

			now := time.Now().UnixNano()
			completedAt := int64(0)
			if complete {
				completedAt = time.Now().Unix()
			}

			if _, err := db.Exec("UPDATE nodes SET completed_at = ?, edited_on = ?, dirty = 1 WHERE uuid = ?",
				completedAt, now, r.Node.UUID); err != nil {
				return errors.Wrap(err, "updating node")
			}

			if complete {
				log.Successf("completed %q\n", r.Node.Name)
			} else {
				log.Successf("uncompleted %q\n", r.Node.Name)
			}

			return nil
		},
	}

	cmd.Flags().BoolVar(&strict, "strict", false, "list matches instead of acting on the best match")

	_ = infra.RunEFunc(nil)
	return cmd
}
