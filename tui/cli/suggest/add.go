package suggest

import (
	"fmt"
	"os"
	"strings"

	"github.com/lflow/lflow/tui/cli/infra"
	"github.com/lflow/lflow/tui/cli/ui"
	"github.com/lflow/lflow/tui/database"
	"github.com/lflow/lflow/tui/database/resolve"
	"github.com/lflow/lflow/tui/runtime"
	"github.com/lflow/lflow/tui/utils/log"
	"github.com/pkg/errors"
	"github.com/spf13/cobra"
)

type addOptions struct {
	parent   string
	typ      string
	note     string
	position string
	message  string
	author   string
	raw      bool
	strict   bool
}

// newAddCmd returns `lflow suggest add`: propose one or more new nodes under a
// parent. Piped stdin becomes one suggestion per line, matching `node add`.
func newAddCmd(ctx runtime.Ctx) *cobra.Command {
	opts := &addOptions{}

	cmd := &cobra.Command{
		Use:   "add [text]",
		Short: "Propose a new node under a parent, root by default",
		RunE:  newAddRun(ctx, opts),
	}

	f := cmd.Flags()
	f.StringVar(&opts.parent, "parent", "", "parent node (id, id prefix or text), defaults to root")
	f.StringVar(&opts.typ, "type", database.TypeBullets, "node type: "+database.TypeList())
	f.StringVar(&opts.note, "note", "", "note for the proposed node")
	f.StringVar(&opts.position, "position", "", "where the node lands on approval: top or bottom, defaults to the parent's priority")
	f.StringVar(&opts.message, "message", "", "why the change is proposed")
	f.StringVar(&opts.author, "author", "", "who is proposing, defaults to $LFLOW_AUTHOR or the OS user")
	f.BoolVar(&opts.raw, "raw", false, "store text verbatim, without turning #tags, dates or [label](url) links into chips on approval")
	f.BoolVar(&opts.strict, "strict", false, "list matches instead of acting on the best match")

	return cmd
}

// readLines takes the proposed text from arguments or piped stdin, one node
// per line.
func readLines(args []string) ([]string, error) {
	if len(args) > 0 {
		return strings.Split(strings.Join(args, " "), "\n"), nil
	}

	fInfo, _ := os.Stdin.Stat()
	if fInfo.Mode()&os.ModeCharDevice == 0 {
		c, err := ui.ReadStdInput()
		if err != nil {
			return nil, errors.Wrap(err, "reading piped input")
		}
		return strings.Split(strings.TrimRight(c, "\n"), "\n"), nil
	}

	return nil, errors.New("no content: pass text or pipe stdin")
}

func newAddRun(ctx runtime.Ctx, opts *addOptions) infra.RunEFunc {
	return func(cmd *cobra.Command, args []string) error {
		db := ctx.DB
		if err := database.EnsureRoot(db); err != nil {
			return err
		}

		if !database.ValidTypes[opts.typ] {
			return errors.Errorf("unknown type %q: %s", opts.typ, database.TypeList())
		}
		switch opts.position {
		case "", database.PositionTop, database.PositionBottom:
		default:
			return errors.Errorf("unknown position %q: top or bottom", opts.position)
		}

		// the parent is resolved now, so the suggestion names a node rather
		// than a search that could drift before review
		parentUUID := database.RootUUID
		parentName := "root"
		if opts.parent != "" {
			r, err := resolve.Resolve(db, opts.parent)
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

		author := resolveAuthor(opts.author)
		var made []database.Suggestion
		for _, line := range lines {
			s := database.Suggestion{
				Kind:       database.SuggestAdd,
				TargetUUID: parentUUID,
				Name:       line,
				Note:       opts.note,
				Type:       opts.typ,
				Position:   opts.position,
				Raw:        opts.raw,
				Author:     author,
				Message:    opts.message,
			}
			if err := s.Insert(db); err != nil {
				return errors.Wrap(err, "storing suggestion")
			}
			made = append(made, s)
		}

		log.Successf("suggested %s under %q\n", resolve.CountNoun(len(made), "node"), parentName)
		for _, s := range made {
			fmt.Printf("    %s  %s\n", dim.Sprint(database.ShortID(s.UUID)), truncate(s.Name, 60))
		}
		fmt.Println(dim.Sprint("  review with lflow suggest list · approve with lflow suggest approve <id>"))

		return nil
	}
}

// resolveAuthor falls back to the environment so agents and humans are told
// apart in a review listing without every call passing --author.
func resolveAuthor(flag string) string {
	if flag != "" {
		return flag
	}
	if env := os.Getenv("LFLOW_AUTHOR"); env != "" {
		return env
	}
	if u := os.Getenv("USER"); u != "" {
		return u
	}
	return ""
}
