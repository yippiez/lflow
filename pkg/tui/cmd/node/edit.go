package node

import (
	"os"
	"strings"
	"time"

	"github.com/lflow/lflow/pkg/tui/context"
	"github.com/lflow/lflow/pkg/tui/database"
	"github.com/lflow/lflow/pkg/tui/resolve"
	"github.com/lflow/lflow/pkg/utils/log"
	"github.com/pkg/errors"
	"github.com/spf13/cobra"
)

type editOptions struct {
	state  string
	name   string
	note   string
	typ    string
	strict bool
}

// newEditCmd returns the node edit command: one command for every node
// property — state, name, note and type.
func newEditCmd(ctx context.DnoteCtx) *cobra.Command {
	opts := &editOptions{}

	cmd := &cobra.Command{
		Use:   "edit <node>",
		Short: "Edit a node's state, name, note or type",
		RunE:  newEditRun(ctx, opts),
	}

	f := cmd.Flags()
	f.StringVar(&opts.state, "state", "", "complete or uncomplete")
	f.StringVar(&opts.name, "name", "", "rename the node")
	f.StringVar(&opts.note, "note", "", "replace the node's note")
	f.StringVar(&opts.typ, "type", "", "bullets, todo, h1, h2, h3, code or quote")
	f.BoolVar(&opts.strict, "strict", false, "list matches instead of acting on the best match")

	return cmd
}

func newEditRun(ctx context.DnoteCtx, opts *editOptions) func(cmd *cobra.Command, args []string) error {
	return func(cmd *cobra.Command, args []string) error {
		if len(args) < 1 {
			return errors.New("missing node reference")
		}
		flags := cmd.Flags()
		if !flags.Changed("state") && !flags.Changed("name") && !flags.Changed("note") && !flags.Changed("type") {
			return errors.New("nothing to edit: pass --state, --name, --note or --type")
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

		sets := []string{"edited_on = ?"}
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
		if flags.Changed("type") {
			if !database.ValidTypes[opts.typ] {
				return errors.Errorf("unknown type %q: bullets, todo, h1, h2, h3, code or quote", opts.typ)
			}
			sets = append(sets, "type = ?")
			vals = append(vals, opts.typ)
		}

		vals = append(vals, r.Node.UUID)
		if _, err := db.Exec("UPDATE nodes SET "+strings.Join(sets, ", ")+" WHERE uuid = ?", vals...); err != nil {
			return errors.Wrap(err, "updating node")
		}

		log.Successf("edited %q\n", r.Node.Name)
		return nil
	}
}
