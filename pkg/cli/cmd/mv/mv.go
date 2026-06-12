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

// Package mv reparents a node under a new parent.
package mv

import (
	"os"
	"time"

	"github.com/lflow/lflow/pkg/cli/context"
	"github.com/lflow/lflow/pkg/cli/database"
	"github.com/lflow/lflow/pkg/cli/infra"
	"github.com/lflow/lflow/pkg/cli/log"
	"github.com/lflow/lflow/pkg/cli/resolve"
	"github.com/pkg/errors"
	"github.com/spf13/cobra"
)

type options struct {
	top    bool
	after  string
	strict bool
}

// NewCmd returns a new mv command
func NewCmd(ctx context.DnoteCtx) *cobra.Command {
	opts := &options{}

	cmd := &cobra.Command{
		Use:   "move <node> <new-parent>",
		Short: "Move a node under another node",
		RunE:  newRun(ctx, opts),
	}

	f := cmd.Flags()
	f.BoolVar(&opts.top, "top", false, "place as the first child instead of the last")
	f.StringVar(&opts.after, "after", "", "place after the given sibling")
	f.BoolVar(&opts.strict, "strict", false, "list matches instead of acting on the best match")

	return cmd
}

// isDescendant reports whether candidate is inside the subtree of root.
func isDescendant(db *database.DB, rootUUID, candidateUUID string) (bool, error) {
	subtree, err := database.GetSubtree(db, rootUUID)
	if err != nil {
		return false, err
	}
	for _, n := range subtree {
		if n.UUID == candidateUUID {
			return true, nil
		}
	}
	return false, nil
}

func newRun(ctx context.DnoteCtx, opts *options) infra.RunEFunc {
	return func(cmd *cobra.Command, args []string) error {
		if len(args) < 2 {
			return errors.New("usage: lflow mv <node> <new-parent>")
		}
		db := ctx.DB

		nodeRes, err := resolve.Resolve(db, args[0])
		if err != nil {
			if _, ok := err.(resolve.ErrNoMatch); ok {
				resolve.PrintNoMatch(args[0])
				os.Exit(1)
			}
			return err
		}
		parentRes, err := resolve.Resolve(db, args[1])
		if err != nil {
			if _, ok := err.(resolve.ErrNoMatch); ok {
				resolve.PrintNoMatch(args[1])
				os.Exit(1)
			}
			return err
		}

		if opts.strict && (nodeRes.Total > 1 || parentRes.Total > 1) {
			resolve.PrintMatches(db, append(nodeRes.Matches, parentRes.Matches...))
			os.Exit(1)
		}

		if nodeRes.Node.UUID == parentRes.Node.UUID {
			return errors.New("cannot move a node into itself")
		}
		cyclic, err := isDescendant(db, nodeRes.Node.UUID, parentRes.Node.UUID)
		if err != nil {
			return errors.Wrap(err, "checking for cycles")
		}
		if cyclic {
			return errors.New("cannot move a node into its own subtree")
		}

		var rank int
		switch {
		case opts.top:
			rank = 0
			if _, err := db.Exec("UPDATE nodes SET rank = rank + 1 WHERE parent_uuid = ? AND deleted = 0", parentRes.Node.UUID); err != nil {
				return errors.Wrap(err, "shifting sibling ranks")
			}
		case opts.after != "":
			sibRes, err := resolve.Resolve(db, opts.after)
			if err != nil {
				return errors.Wrap(err, "resolving --after sibling")
			}
			if sibRes.Node.ParentUUID != parentRes.Node.UUID {
				return errors.New("--after node is not a child of the new parent")
			}
			rank = sibRes.Node.Rank + 1
			if _, err := db.Exec("UPDATE nodes SET rank = rank + 1 WHERE parent_uuid = ? AND rank > ? AND deleted = 0",
				parentRes.Node.UUID, sibRes.Node.Rank); err != nil {
				return errors.Wrap(err, "shifting sibling ranks")
			}
		default:
			rank, err = database.NextRank(db, parentRes.Node.UUID)
			if err != nil {
				return err
			}
		}

		now := time.Now().UnixNano()
		if _, err := db.Exec("UPDATE nodes SET parent_uuid = ?, rank = ?, edited_on = ?, dirty = 1 WHERE uuid = ?",
			parentRes.Node.UUID, rank, now, nodeRes.Node.UUID); err != nil {
			return errors.Wrap(err, "moving node")
		}

		log.Successf("moved %q → %q\n", nodeRes.Node.Name, parentRes.Node.Name)

		return nil
	}
}
