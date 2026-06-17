// Package add creates child nodes under a parent node. Piped stdin becomes
// one node per line; `lflow append` is the same operation by another name.
package add

import (
	"os"
	"strings"
	"time"

	"github.com/lflow/lflow/pkg/tui/context"
	"github.com/lflow/lflow/pkg/tui/database"
	"github.com/lflow/lflow/pkg/tui/infra"
	"github.com/lflow/lflow/pkg/tui/log"
	"github.com/lflow/lflow/pkg/tui/resolve"
	"github.com/lflow/lflow/pkg/tui/ui"
	"github.com/lflow/lflow/pkg/tui/utils"
	"github.com/pkg/errors"
	"github.com/spf13/cobra"
)

type options struct {
	parent   string
	intoNote bool
	top      bool
	strict   bool
}

// NewCmd returns a new add command
func NewCmd(ctx context.DnoteCtx) *cobra.Command {
	opts := &options{}

	cmd := &cobra.Command{
		Use:   "add [text]",
		Short: "Add nodes under a parent, root by default",
		RunE:  newRun(ctx, opts),
	}

	f := cmd.Flags()
	f.StringVar(&opts.parent, "parent", "", "parent node, defaults to root")
	f.BoolVar(&opts.intoNote, "note", false, "append the text to the parent's note instead of creating children")
	f.BoolVar(&opts.top, "top", false, "prepend instead of append")
	f.BoolVar(&opts.strict, "strict", false, "list matches instead of acting on the best match")

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
			Type:     database.TypeBullets,
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
		db := ctx.DB
		if err := database.EnsureRoot(db); err != nil {
			return err
		}

		// resolve --parent, defaulting to the always-available root
		parentUUID := database.RootUUID
		parentName := "root"

		var r resolve.Result
		if opts.parent != "" {
			var err error
			r, err = resolve.Resolve(db, opts.parent)
			if err != nil {
				if _, ok := err.(resolve.ErrNoMatch); ok {
					resolve.PrintNoMatch(opts.parent)
					os.Exit(1)
				}
				return err
			}

			if opts.strict && r.Total > 1 {
				resolve.PrintMatches(db, r.Matches)
				os.Exit(1)
			}
			parentUUID = r.Node.UUID
			parentName = r.Node.Name
		}

		lines, err := readLines(args)
		if err != nil {
			return err
		}

		if opts.intoNote {
			if opts.parent == "" {
				return errors.New("--note needs --parent")
			}
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

		count, err := insertChildren(db, parentUUID, lines, opts.top)
		if err != nil {
			return errors.Wrap(err, "inserting nodes")
		}

		log.Successf("added %s to %q\n", resolve.CountNoun(count, "node"), parentName)

		return nil
	}
}
