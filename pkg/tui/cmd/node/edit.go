package node

import (
	"os"
	"strings"
	"time"

	"github.com/lflow/lflow/pkg/tui/context"
	"github.com/lflow/lflow/pkg/tui/database"
	"github.com/lflow/lflow/pkg/tui/infra"
	"github.com/lflow/lflow/pkg/tui/resolve"
	"github.com/lflow/lflow/pkg/utils/log"
	"github.com/pkg/errors"
	"github.com/spf13/cobra"
)

type editOptions struct {
	state    string
	name     string
	note     string
	typ      string
	readonly bool
	strict   bool
	raw      bool
	style    infra.StyleFlags
}

// newEditCmd returns the node edit command: one command for every node
// property — state, name, note, type, style and lock.
func newEditCmd(ctx context.DnoteCtx) *cobra.Command {
	opts := &editOptions{}

	cmd := &cobra.Command{
		Use:   "edit <node>",
		Short: "Edit a node's state, name, note, type, style or lock",
		RunE:  newEditRun(ctx, opts),
	}

	f := cmd.Flags()
	f.StringVar(&opts.state, "state", "", "complete or uncomplete")
	f.StringVar(&opts.name, "name", "", "rename the node")
	f.StringVar(&opts.note, "note", "", "replace the node's note")
	f.StringVar(&opts.typ, "type", "", "node type: "+database.TypeList())
	f.BoolVar(&opts.readonly, "readonly", false, "lock the node against editing (--readonly=false unlocks)")
	f.BoolVar(&opts.strict, "strict", false, "list matches instead of acting on the best match")
	f.BoolVar(&opts.raw, "raw", false, "with --name, store text verbatim instead of turning #tags, dates or [label](url) links into chips")
	opts.style.Register(f)

	return cmd
}

func newEditRun(ctx context.DnoteCtx, opts *editOptions) func(cmd *cobra.Command, args []string) error {
	return func(cmd *cobra.Command, args []string) error {
		if len(args) < 1 {
			return errors.New("missing node reference")
		}
		flags := cmd.Flags()
		styleChange := opts.style.Change(cmd)
		if !flags.Changed("state") && !flags.Changed("name") && !flags.Changed("note") &&
			!flags.Changed("type") && !flags.Changed("readonly") && !styleChange.Any() {
			return errors.New("nothing to edit: pass --state, --name, --note, --type, --readonly or a style flag (--style/--color/--bold/...)")
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
			name := opts.name
			if !opts.raw {
				name, err = database.ChipifyName(db, opts.name)
				if err != nil {
					return errors.Wrap(err, "creating chips")
				}
			}
			sets = append(sets, "name = ?")
			vals = append(vals, name)
		}
		if flags.Changed("note") {
			sets = append(sets, "note = ?")
			vals = append(vals, opts.note)
		}
		if flags.Changed("type") {
			if !database.ValidTypes[opts.typ] {
				return errors.Errorf("unknown type %q: %s", opts.typ, database.TypeList())
			}
			sets = append(sets, "type = ?")
			vals = append(vals, opts.typ)
		}
		if flags.Changed("readonly") {
			// readonly is now a lock bitset: CLI content locking must preserve the
			// independent structural lock used by generated query views.
			if opts.readonly {
				sets = append(sets, "readonly = readonly | 1")
			} else {
				sets = append(sets, "readonly = readonly & ~1")
			}
		}
		if styleChange.Any() {
			newStyle, err := styleChange.Apply(r.Node.Style)
			if err != nil {
				return err
			}
			sets = append(sets, "style = ?")
			vals = append(vals, newStyle)
		}

		vals = append(vals, r.Node.UUID)
		if _, err := db.Exec("UPDATE nodes SET "+strings.Join(sets, ", ")+" WHERE uuid = ?", vals...); err != nil {
			return errors.Wrap(err, "updating node")
		}

		log.Successf("edited %q\n", r.Node.Name)
		return nil
	}
}
