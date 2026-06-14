package node

import (
	"os"
	"strings"
	"time"

	"github.com/lflow/lflow/pkg/cli/context"
	"github.com/lflow/lflow/pkg/cli/database"
	"github.com/lflow/lflow/pkg/cli/log"
	"github.com/lflow/lflow/pkg/cli/resolve"
	"github.com/pkg/errors"
	"github.com/spf13/cobra"
)

type editOptions struct {
	state  string
	name   string
	note   string
	layout string
	strict bool
}

// newEditCmd returns the node edit command: one command for every node
// property — state, name, note and layout.
func newEditCmd(ctx context.DnoteCtx) *cobra.Command {
	opts := &editOptions{}

	cmd := &cobra.Command{
		Use:   "edit <node>",
		Short: "Edit a node's state, name, note or layout",
		RunE:  newEditRun(ctx, opts),
	}

	f := cmd.Flags()
	f.StringVar(&opts.state, "state", "", "complete or uncomplete")
	f.StringVar(&opts.name, "name", "", "rename the node")
	f.StringVar(&opts.note, "note", "", "replace the node's note")
	f.StringVar(&opts.layout, "layout", "", "bullets, todo, h1, h2, h3, code or quote")
	f.BoolVar(&opts.strict, "strict", false, "list matches instead of acting on the best match")

	return cmd
}

func newEditRun(ctx context.DnoteCtx, opts *editOptions) func(cmd *cobra.Command, args []string) error {
	return func(cmd *cobra.Command, args []string) error {
		if len(args) < 1 {
			return errors.New("missing node reference")
		}
		flags := cmd.Flags()
		if !flags.Changed("state") && !flags.Changed("name") && !flags.Changed("note") && !flags.Changed("layout") {
			return errors.New("nothing to edit: pass --state, --name, --note or --layout")
		}

		ref := strings.Join(args, " ")
		db := ctx.DB

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

		sets := []string{"edited_on = ?", "dirty = 1"}
		vals := []interface{}{time.Now().UnixNano()}

		if flags.Changed("state") {
			switch opts.state {
			case "complete":
				sets = append(sets, "completed_at = ?")
				vals = append(vals, time.Now().Unix())
			case "uncomplete":
				sets = append(sets, "completed_at = 0")
			default:
				return errors.Errorf("unknown state %q: complete or uncomplete", opts.state)
			}
		}
		if flags.Changed("name") {
			sets = append(sets, "name = ?")
			vals = append(vals, opts.name)
		}
		if flags.Changed("note") {
			sets = append(sets, "note = ?")
			vals = append(vals, opts.note)
		}
		if flags.Changed("layout") {
			if !database.ValidLayouts[opts.layout] {
				return errors.Errorf("unknown layout %q: bullets, todo, h1, h2, h3, code or quote", opts.layout)
			}
			sets = append(sets, "layout = ?")
			vals = append(vals, opts.layout)
		}

		vals = append(vals, r.Node.UUID)
		if _, err := db.Exec("UPDATE nodes SET "+strings.Join(sets, ", ")+" WHERE uuid = ?", vals...); err != nil {
			return errors.Wrap(err, "updating node")
		}

		log.Successf("edited %q\n", r.Node.Name)
		return nil
	}
}
