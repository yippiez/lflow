package version

import (
	"fmt"

	"github.com/lflow/lflow/pkg/cli/context"
	"github.com/spf13/cobra"
)

// NewCmd returns a new version command
func NewCmd(ctx context.DnoteCtx) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "version",
		Short: "Print the version",
		Long:  "Print the version number of Lflow",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Printf("lflow %s\n", ctx.Version)
		},
	}

	return cmd
}
