package root

import (
	"github.com/spf13/cobra"
)

var root = &cobra.Command{
	Use:           "lflow",
	Short:         "local-first terminal outline tool",
	SilenceErrors: true,
	SilenceUsage:  true,
	CompletionOptions: cobra.CompletionOptions{
		DisableDefaultCmd: true,
	},
}

func init() {
	// --help is the only help surface: no help subcommand
	root.SetHelpCommand(&cobra.Command{Use: "no-help", Hidden: true})
	root.SetHelpFunc(renderHelpFunc)
	root.SetUsageFunc(renderUsageFunc)
}

// Register adds a new command
func Register(cmd *cobra.Command) {
	root.AddCommand(cmd)
}

// Execute runs the main command
func Execute() error {
	return root.Execute()
}
