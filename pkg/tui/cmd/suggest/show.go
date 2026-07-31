package suggest

import (
	"fmt"
	"strings"
	"time"

	"github.com/lflow/lflow/pkg/tui/context"
	"github.com/lflow/lflow/pkg/tui/database"
	"github.com/lflow/lflow/pkg/tui/infra"
	"github.com/pkg/errors"
	"github.com/spf13/cobra"
)

type showOptions struct {
	format string
}

// newShowCmd returns `lflow suggest show`: the full proposal, current value
// beside proposed value, so a reviewer decides from one screen.
func newShowCmd(ctx context.DnoteCtx) *cobra.Command {
	opts := &showOptions{}

	cmd := &cobra.Command{
		Use:   "show <id>",
		Short: "Show one suggestion: current value beside the proposed one",
		RunE:  newShowRun(ctx, opts),
	}

	cmd.Flags().StringVar(&opts.format, "format", "text", "output format: text|json")

	return cmd
}

func newShowRun(ctx context.DnoteCtx, opts *showOptions) infra.RunEFunc {
	return func(cmd *cobra.Command, args []string) error {
		if len(args) < 1 {
			return errors.New("missing suggestion id")
		}
		db := ctx.DB
		s := resolveRef(db, args[0])

		if opts.format == "json" {
			drifted, _ := database.TargetDrifted(db, s)
			return printJSON(struct {
				database.Suggestion
				Target  string `json:"target"`
				Drifted bool   `json:"drifted"`
			}{s, targetLine(db, s), drifted})
		}
		if opts.format != "text" {
			return errors.Errorf("unknown format %q: text or json", opts.format)
		}

		fmt.Printf("%s  %s %s\n", dim.Sprint(database.ShortID(s.UUID)), statusMark(s.Status), dim.Sprint(s.Status))
		field("kind", s.Kind)
		if s.Kind == database.SuggestAdd {
			field("parent", targetLine(db, s))
		} else {
			field("node", targetLine(db, s))
		}
		if s.Author != "" {
			field("author", s.Author)
		}
		field("made", time.Unix(0, s.CreatedOn).Format("2006-01-02 15:04"))
		if s.Message != "" {
			field("why", s.Message)
		}

		fmt.Println()
		if s.Kind == database.SuggestAdd {
			showAdd(db, s)
		} else {
			showEdit(db, s)
		}

		if s.Status == database.SuggestApproved && s.ResultUUID != "" {
			fmt.Println(dim.Sprintf("\n  applied as node %s", database.ShortID(s.ResultUUID)))
		}
		if s.Pending() {
			short := database.ShortID(s.UUID)
			fmt.Println(dim.Sprintf("\n  lflow suggest approve %s · lflow suggest reject %s", short, short))
		}
		return nil
	}
}

// showAdd prints the node an add suggestion proposes.
func showAdd(db *database.DB, s database.Suggestion) {
	fmt.Printf("  %s %s\n", green.Sprint("+"), display(db, s.Name))
	if s.Note != "" {
		fmt.Printf("  %s %s\n", green.Sprint("+"), dim.Sprint("note: "+s.Note))
	}
	trailer := s.Type
	if s.Position != "" {
		trailer += " · " + s.Position
	}
	fmt.Println(dim.Sprint("    " + trailer))
}

// showEdit prints each proposed field as a current/proposed pair, flagging a
// target that moved underneath the suggestion.
func showEdit(db *database.DB, s database.Suggestion) {
	n, err := database.GetNode(db, s.TargetUUID)
	if err != nil {
		fmt.Println(red.Sprint("  → the target node is gone"))
		return
	}

	pairs := []struct{ label, current, proposed string }{}
	if s.Proposes(database.FieldName) {
		pairs = append(pairs, struct{ label, current, proposed string }{"text", n.Name, s.Name})
	}
	if s.Proposes(database.FieldNote) {
		pairs = append(pairs, struct{ label, current, proposed string }{"note", n.Note, s.Note})
	}
	if s.Proposes(database.FieldType) {
		pairs = append(pairs, struct{ label, current, proposed string }{"type", n.Type, s.Type})
	}

	for _, p := range pairs {
		fmt.Println(dim.Sprint("  " + p.label))
		fmt.Printf("  %s %s\n", red.Sprint("-"), display(db, p.current))
		fmt.Printf("  %s %s\n", green.Sprint("+"), display(db, p.proposed))
	}

	if drifted, err := database.TargetDrifted(db, s); err == nil && drifted && s.Pending() {
		fmt.Println(yellow.Sprint("\n  → the node changed since this was suggested"))
		fmt.Println(dim.Sprintf("    it read %q when proposed · approve --force applies anyway", truncate(display(db, s.BaseName), 40)))
	}
}

// field prints one dim-labeled detail line.
func field(label, value string) {
	fmt.Printf("  %s %s\n", dim.Sprintf("%-7s", label), strings.TrimSpace(value))
}
