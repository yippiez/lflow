// Package file opens supported source files (.md, .py) in the node editor:
// the file parses into a node tree in a throwaway scratch database, the
// ordinary editor edits that tree, and every save serializes the tree back
// through the codec to the file — two-way binding, file ⇄ outline.
package file

import (
	"github.com/lflow/lflow/tui/runtime"
	"github.com/spf13/cobra"
)

// NewCmd returns the file command group.
func NewCmd(ctx runtime.Ctx) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "file",
		Short: "Open supported files (.md, .py) in the node editor",
	}
	cmd.AddCommand(newOpenCmd(ctx))
	return cmd
}
