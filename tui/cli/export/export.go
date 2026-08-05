// Package export dumps the whole local forest as json or markdown.
package export

import (
	"encoding/json"
	"fmt"

	"github.com/lflow/lflow/tui/cli/infra"
	"github.com/lflow/lflow/tui/database"
	"github.com/lflow/lflow/tui/runtime"
	"github.com/pkg/errors"
	"github.com/spf13/cobra"
)

type options struct {
	format string
}

// NewCmd returns a new export command
func NewCmd(ctx runtime.Ctx) *cobra.Command {
	opts := &options{}

	cmd := &cobra.Command{
		Use:   "export",
		Short: "Export the whole forest",
		RunE:  newRun(ctx, opts),
	}

	f := cmd.Flags()
	f.StringVar(&opts.format, "format", "json", "output format: json|md")

	return cmd
}

func newRun(ctx runtime.Ctx, opts *options) infra.RunEFunc {
	return func(cmd *cobra.Command, args []string) error {
		db := ctx.DB
		if err := database.EnsureRoot(db); err != nil {
			return err
		}

		roots, err := database.GetChildren(db, database.RootUUID)
		if err != nil {
			return errors.Wrap(err, "querying top-level nodes")
		}

		switch opts.format {
		case "json":
			forest := []database.JSONNode{}
			for _, root := range roots {
				tree, err := database.BuildJSON(db, root, -1, true)
				if err != nil {
					return errors.Wrap(err, "building tree")
				}
				forest = append(forest, tree)
			}
			b, err := json.MarshalIndent(forest, "", "  ")
			if err != nil {
				return errors.Wrap(err, "marshalling forest")
			}
			fmt.Println(string(b))
		case "md":
			for _, root := range roots {
				fmt.Printf("- %s\n", root.Name)
				out, err := database.RenderMarkdown(db, root, -1, true)
				if err != nil {
					return errors.Wrap(err, "rendering outline")
				}
				if out != "" {
					// indent children under the root line
					fmt.Println(indentLines(out))
				}
			}
		default:
			return errors.Errorf("unknown format %q: json or md", opts.format)
		}

		return nil
	}
}

func indentLines(s string) string {
	out := ""
	for i, line := range splitLines(s) {
		if i > 0 {
			out += "\n"
		}
		out += "  " + line
	}
	return out
}

func splitLines(s string) []string {
	var lines []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			lines = append(lines, s[start:i])
			start = i + 1
		}
	}
	lines = append(lines, s[start:])
	return lines
}
