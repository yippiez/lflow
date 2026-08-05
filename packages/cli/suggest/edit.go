package suggest

import (
	"fmt"
	"os"
	"strings"

	"github.com/lflow/lflow/packages/cli/infra"
	"github.com/lflow/lflow/packages/database"
	"github.com/lflow/lflow/packages/database/resolve"
	"github.com/lflow/lflow/packages/tui"
	"github.com/lflow/lflow/packages/utils/log"
	"github.com/pkg/errors"
	"github.com/spf13/cobra"
)

type editOptions struct {
	name    string
	note    string
	typ     string
	message string
	author  string
	raw     bool
	strict  bool
}

// newEditCmd returns `lflow suggest edit`: propose new text, note or type for
// an existing node. The node's current text is snapshotted so a reviewer can
// see whether it drifted before approval.
func newEditCmd(ctx tui.Ctx) *cobra.Command {
	opts := &editOptions{}

	cmd := &cobra.Command{
		Use:   "edit <node>",
		Short: "Propose new text, note or type for a node",
		RunE:  newEditRun(ctx, opts),
	}

	f := cmd.Flags()
	f.StringVar(&opts.name, "name", "", "proposed text")
	f.StringVar(&opts.note, "note", "", "proposed note")
	f.StringVar(&opts.typ, "type", "", "proposed node type: "+database.TypeList())
	f.StringVar(&opts.message, "message", "", "why the change is proposed")
	f.StringVar(&opts.author, "author", "", "who is proposing, defaults to $LFLOW_AUTHOR or the OS user")
	f.BoolVar(&opts.raw, "raw", false, "with --name, store text verbatim instead of turning #tags, dates or [label](url) links into chips on approval")
	f.BoolVar(&opts.strict, "strict", false, "list matches instead of acting on the best match")

	return cmd
}

func newEditRun(ctx tui.Ctx, opts *editOptions) infra.RunEFunc {
	return func(cmd *cobra.Command, args []string) error {
		if len(args) < 1 {
			return errors.New("missing node reference")
		}
		flags := cmd.Flags()
		if !flags.Changed("name") && !flags.Changed("note") && !flags.Changed("type") {
			return errors.New("nothing to suggest: pass --name, --note or --type")
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

		var fields []string
		if flags.Changed("name") {
			fields = append(fields, database.FieldName)
		}
		if flags.Changed("note") {
			fields = append(fields, database.FieldNote)
		}
		if flags.Changed("type") {
			if !database.ValidTypes[opts.typ] {
				return errors.Errorf("unknown type %q: %s", opts.typ, database.TypeList())
			}
			fields = append(fields, database.FieldType)
		}

		s := database.Suggestion{
			Kind:       database.SuggestEdit,
			TargetUUID: r.Node.UUID,
			Name:       opts.name,
			Note:       opts.note,
			Type:       opts.typ,
			Fields:     database.NormalizeFields(fields),
			Raw:        opts.raw,
			BaseName:   r.Node.Name,
			BaseNote:   r.Node.Note,
			Author:     resolveAuthor(opts.author),
			Message:    opts.message,
		}
		if err := s.Insert(db); err != nil {
			return errors.Wrap(err, "storing suggestion")
		}

		log.Successf("suggested an edit to %q\n", display(db, r.Node.Name))
		fmt.Printf("    %s  %s\n", dim.Sprint(database.ShortID(s.UUID)), dim.Sprint(strings.Join(s.FieldList(), ", ")))
		if r.Total > 1 {
			fmt.Println(dim.Sprintf("    best of %d · --strict lists instead", r.Total))
		}
		fmt.Println(dim.Sprintf("  review with lflow suggest show %s", database.ShortID(s.UUID)))

		return nil
	}
}
