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

// Package add creates child nodes under a parent node. Piped stdin becomes
// one node per line; `lflow append` is the same operation by another name.
package add

import (
	"os"
	"strings"
	"time"

	"github.com/lflow/lflow/pkg/cli/context"
	"github.com/lflow/lflow/pkg/cli/database"
	"github.com/lflow/lflow/pkg/cli/infra"
	"github.com/lflow/lflow/pkg/cli/log"
	"github.com/lflow/lflow/pkg/cli/resolve"
	"github.com/lflow/lflow/pkg/cli/ui"
	"github.com/lflow/lflow/pkg/cli/utils"
	"github.com/pkg/errors"
	"github.com/spf13/cobra"
)

type options struct {
	intoNote bool
	top      bool
	strict   bool
	all      bool
	root     bool
}

var example = `
 * Add a child node
 lflow add "experiment results" "attempt 3"

 * Pipe stdin: every line becomes a child node
 make bench 2>&1 | lflow append "experiment results"

 * Append to the node's note instead
 echo "context" | lflow append "experiment results" --note

 * Create a new root node
 lflow add --root "reading list"`

// NewCmd returns a new add command
func NewCmd(ctx context.DnoteCtx) *cobra.Command {
	return newCmd(ctx, "add", "Add child nodes under a node", []string{"a", "new"})
}

// NewAppendCmd returns the append alias command
func NewAppendCmd(ctx context.DnoteCtx) *cobra.Command {
	return newCmd(ctx, "append", "Append trailing children to a node", []string{"ap"})
}

func newCmd(ctx context.DnoteCtx, use, short string, aliases []string) *cobra.Command {
	opts := &options{}

	cmd := &cobra.Command{
		Use:     use + " <node> [text]",
		Short:   short,
		Aliases: aliases,
		Example: example,
		RunE:    newRun(ctx, opts),
	}

	f := cmd.Flags()
	f.BoolVar(&opts.intoNote, "note", false, "append the text to the node's note instead of creating children")
	f.BoolVar(&opts.top, "top", false, "prepend instead of append")
	f.BoolVar(&opts.strict, "strict", false, "list matches instead of acting on the best match")
	f.BoolVar(&opts.all, "all", false, "include completed nodes when resolving")
	f.BoolVar(&opts.root, "root", false, "create a new root node named <node> (no parent resolution)")

	return cmd
}

func readLines(args []string) ([]string, error) {
	// explicit text argument
	if len(args) > 0 {
		text := strings.Join(args, " ")
		return strings.Split(text, "\n"), nil
	}

	// piped stdin: one node per line
	fInfo, _ := os.Stdin.Stat()
	if fInfo.Mode()&os.ModeCharDevice == 0 {
		c, err := ui.ReadStdInput()
		if err != nil {
			return nil, errors.Wrap(err, "reading piped input")
		}
		var lines []string
		for _, l := range strings.Split(strings.TrimRight(c, "\n"), "\n") {
			lines = append(lines, l)
		}
		return lines, nil
	}

	return nil, errors.New("no content: pass text or pipe stdin")
}

func insertChildren(db *database.DB, parentUUID string, lines []string, top bool) (int, error) {
	now := time.Now().UnixNano()

	var rank int
	var err error
	if top {
		rank = 0
		// shift existing children down
		if _, err := db.Exec("UPDATE nodes SET rank = rank + ? WHERE parent_uuid = ? AND deleted = 0", len(lines), parentUUID); err != nil {
			return 0, errors.Wrap(err, "shifting sibling ranks")
		}
	} else {
		rank, err = database.NextRank(db, parentUUID)
		if err != nil {
			return 0, err
		}
	}

	count := 0
	for i, line := range lines {
		uuid, err := utils.GenerateUUID()
		if err != nil {
			return count, errors.Wrap(err, "generating uuid")
		}
		n := database.Node{
			UUID:       uuid,
			ParentUUID: parentUUID,
			Rank:       rank + i,
			Name:       line,
			Layout:     database.LayoutBullets,
			AddedOn:    now,
			EditedOn:   now,
			Dirty:      true,
		}
		if err := n.Insert(db); err != nil {
			return count, err
		}
		count++
	}

	return count, nil
}

func newRun(ctx context.DnoteCtx, opts *options) infra.RunEFunc {
	return func(cmd *cobra.Command, args []string) error {
		if len(args) < 1 {
			return errors.New("missing node reference")
		}
		ref := args[0]
		rest := args[1:]

		db := ctx.DB

		if opts.root {
			count, err := insertChildren(db, "", []string{ref}, opts.top)
			if err != nil {
				return errors.Wrap(err, "creating root node")
			}
			_ = count
			log.Successf("created root node %q\n", ref)
			return nil
		}

		r, err := resolve.Resolve(db, ref, opts.all)
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

		lines, err := readLines(rest)
		if err != nil {
			return err
		}

		if opts.intoNote {
			text := strings.Join(lines, "\n")
			note := r.Node.Note
			if note != "" {
				note += "\n"
			}
			note += text
			now := time.Now().UnixNano()
			if _, err := db.Exec("UPDATE nodes SET note = ?, edited_on = ?, dirty = 1 WHERE uuid = ?", note, now, r.Node.UUID); err != nil {
				return errors.Wrap(err, "updating note")
			}
			log.Successf("noted on %q\n", r.Node.Name)
			return nil
		}

		count, err := insertChildren(db, r.Node.UUID, lines, opts.top)
		if err != nil {
			return errors.Wrap(err, "inserting nodes")
		}

		if count == 1 {
			log.Successf("added 1 node to %q\n", r.Node.Name)
		} else {
			log.Successf("added %d nodes to %q\n", count, r.Node.Name)
		}

		return nil
	}
}
