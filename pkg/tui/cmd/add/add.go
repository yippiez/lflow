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
	"github.com/lflow/lflow/pkg/tui/resolve"
	"github.com/lflow/lflow/pkg/tui/ui"
	"github.com/lflow/lflow/pkg/utils"
	"github.com/lflow/lflow/pkg/utils/log"
	"github.com/pkg/errors"
	"github.com/spf13/cobra"
)

type options struct {
	parent string
	typ    string
	note   string
	top    bool
	strict bool
	raw    bool
	style  infra.StyleFlags
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
	f.StringVar(&opts.parent, "parent", "", "parent node (id, id prefix or text), defaults to root")
	f.StringVar(&opts.typ, "type", database.TypeBullets, "node type: "+database.TypeList())
	f.StringVar(&opts.note, "note", "", "set the note on the added node(s)")
	f.BoolVar(&opts.top, "top", false, "prepend instead of append")
	f.BoolVar(&opts.strict, "strict", false, "list matches instead of acting on the best match")
	f.BoolVar(&opts.raw, "raw", false, "store text verbatim, without turning #tags, dates or [label](url) links into chips")
	opts.style.Register(f)

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

func insertChildren(db *database.DB, parentUUID string, lines []string, typ, note, styleStr string, top, raw bool) (int, error) {
	now := time.Now().UnixNano()

	var rank int
	var err error
	if top {
		rank = 0
		// shift existing children down
		if err := database.ShiftRanksAll(db, parentUUID, len(lines)); err != nil {
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
		name := line
		if !raw {
			name, err = database.ChipifyName(db, line)
			if err != nil {
				return count, errors.Wrap(err, "creating chips")
			}
		}
		n := database.Node{
			UUID:       uuid,
			ParentUUID: parentUUID,
			Rank:       rank + i,
			Name:       name,
			Note:       note,
			Type:       typ,
			Style:      styleStr,
			AddedOn:    now,
			EditedOn:   now,
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

		if !database.ValidTypes[opts.typ] {
			return errors.Errorf("unknown type %q: %s", opts.typ, database.TypeList())
		}

		styleChange := opts.style.Change(cmd)
		styleStr, err := styleChange.Apply("")
		if err != nil {
			return err
		}

		// resolve --parent, defaulting to the always-available root
		parentUUID := database.RootUUID
		parentName := "root"

		var r resolve.Result
		if opts.parent != "" {
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

		count, err := insertChildren(db, parentUUID, lines, opts.typ, opts.note, styleStr, opts.top, opts.raw)
		if err != nil {
			return errors.Wrap(err, "inserting nodes")
		}

		log.Successf("added %s to %q\n", resolve.CountNoun(count, "node"), parentName)

		return nil
	}
}
