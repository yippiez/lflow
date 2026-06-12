/* Copyright 2025 Lflow Authors
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

// Package export dumps the whole local forest as json or markdown.
package export

import (
	"encoding/json"
	"fmt"

	"github.com/lflow/lflow/pkg/cli/context"
	"github.com/lflow/lflow/pkg/cli/database"
	"github.com/lflow/lflow/pkg/cli/infra"
	"github.com/lflow/lflow/pkg/cli/outline"
	"github.com/pkg/errors"
	"github.com/spf13/cobra"
)

type options struct {
	format    string
	completed bool
}

// NewCmd returns a new export command
func NewCmd(ctx context.DnoteCtx) *cobra.Command {
	opts := &options{}

	cmd := &cobra.Command{
		Use:   "export",
		Short: "Export the whole forest",
		RunE:  newRun(ctx, opts),
	}

	f := cmd.Flags()
	f.StringVar(&opts.format, "format", "json", "output format: json|md")
	f.BoolVar(&opts.completed, "completed", true, "include completed nodes")

	return cmd
}

func newRun(ctx context.DnoteCtx, opts *options) infra.RunEFunc {
	return func(cmd *cobra.Command, args []string) error {
		db := ctx.DB

		roots, err := database.GetChildren(db, "")
		if err != nil {
			return errors.Wrap(err, "querying roots")
		}

		switch opts.format {
		case "json":
			forest := []outline.JSONNode{}
			for _, root := range roots {
				tree, err := outline.BuildJSON(db, root, -1, opts.completed)
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
				out, err := outline.RenderMarkdown(db, root, -1, opts.completed)
				if err != nil {
					return errors.Wrap(err, "rendering outline")
				}
				if out != "" {
					// indent children under the root line
					fmt.Println(indentLines(out))
				}
			}
		default:
			return errors.Errorf("unknown format %q (json|md)", opts.format)
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
