// Package grep searches nodes by name/note and prints a table of matches with
// their ids, so you can find the uuid to pass to other commands.
package grep

import (
	"fmt"
	"os"
	"strings"

	"github.com/fatih/color"
	"github.com/lflow/lflow/pkg/tui/context"
	"github.com/lflow/lflow/pkg/tui/database"
	"github.com/lflow/lflow/pkg/tui/infra"
	"github.com/pkg/errors"
	"github.com/spf13/cobra"
)

var dim = color.New(color.FgHiBlack)

type options struct {
	all bool
	typ string
}

// NewCmd returns the grep command.
func NewCmd(ctx context.DnoteCtx) *cobra.Command {
	opts := &options{}

	cmd := &cobra.Command{
		Use:   "grep [text]",
		Short: "Search nodes by text and print their ids in a table",
		RunE:  newRun(ctx, opts),
	}

	f := cmd.Flags()
	f.BoolVar(&opts.all, "all", false, "include completed nodes")
	f.StringVar(&opts.typ, "type", "", "only nodes of this type: "+database.TypeList())

	return cmd
}

// childCount returns the number of direct, non-deleted children of a node.
func childCount(db *database.DB, uuid string) int {
	var n int
	if err := db.QueryRow("SELECT COUNT(*) FROM nodes WHERE parent_uuid = ? AND deleted = 0", uuid).Scan(&n); err != nil {
		return 0
	}
	return n
}

func newRun(ctx context.DnoteCtx, opts *options) infra.RunEFunc {
	return func(cmd *cobra.Command, args []string) error {
		query := strings.Join(args, " ")
		db := ctx.DB

		if opts.typ != "" && !database.ValidTypes[opts.typ] {
			return errors.Errorf("unknown type %q: %s", opts.typ, database.TypeList())
		}
		if query == "" && opts.typ == "" {
			return errors.New("missing search text (or pass --type to list a type)")
		}

		var matches []database.Node
		var err error
		if query == "" {
			// --type with no text: list every live node of that type
			matches, err = database.AllLiveNodes(db)
		} else {
			matches, err = database.SearchNodes(db, query, opts.all)
		}
		if err != nil {
			return errors.Wrap(err, "searching nodes")
		}
		if opts.typ != "" {
			filtered := matches[:0]
			for _, n := range matches {
				if n.Type == opts.typ {
					filtered = append(filtered, n)
				}
			}
			matches = filtered
		}
		if len(matches) == 0 {
			if query == "" {
				fmt.Println(dim.Sprintf("→ no %s node", opts.typ))
			} else {
				fmt.Println(dim.Sprintf("→ no node matching %q", query))
			}
			os.Exit(1)
		}

		chips, _ := database.LoadChips(db) // resolve inline chip anchors for display

		fmt.Printf("%-8s  %-40s  %4s  %s\n",
			dim.Sprint("id"), dim.Sprint("name"), dim.Sprint("kids"), dim.Sprint("type"))
		for _, n := range matches {
			shortID := n.UUID
			if len(shortID) > 6 {
				shortID = shortID[:6]
			}
			name := database.DisplayAnchors(n.Name, chips)
			if len([]rune(name)) > 40 {
				name = string([]rune(name)[:39]) + "…"
			}
			fmt.Printf("%-8s  %-40s  %4d  %s\n",
				dim.Sprint(shortID), name, childCount(db, n.UUID), dim.Sprint(n.Type))
		}
		return nil
	}
}
