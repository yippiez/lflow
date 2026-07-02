// Package artifact implements the `lflow artifact` command group: the
// scriptable counterpart to the in-editor /artifacts view. One-shot and
// pipe-friendly like every lflow command.
package artifact

import (
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/lflow/lflow/pkg/tui/context"
	"github.com/lflow/lflow/pkg/tui/database"
	"github.com/pkg/errors"
)

// NewCmd returns the artifact command group.
func NewCmd(ctx context.DnoteCtx) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "artifact",
		Short: "manage runtime node-type artifacts",
	}
	cmd.AddCommand(newListCmd(ctx), newShowCmd(ctx), newAddCmd(ctx), newRmCmd(ctx), newEnableCmd(ctx, true), newEnableCmd(ctx, false))
	return cmd
}

func newListCmd(ctx context.DnoteCtx) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "list installed artifacts",
		RunE: func(cmd *cobra.Command, args []string) error {
			rows, err := database.ListArtifacts(ctx.DB)
			if err != nil {
				return err
			}
			for _, a := range rows {
				state := "enabled"
				if !a.Enabled {
					state = "disabled"
				}
				fmt.Printf("→ %s · %s · v%d · %s · %s\n", a.Key, a.Label, a.Version, a.CreatedBy, state)
			}
			if len(rows) == 0 {
				fmt.Println("→ no artifacts installed")
			}
			return nil
		},
	}
}

func newShowCmd(ctx context.DnoteCtx) *cobra.Command {
	return &cobra.Command{
		Use:   "show <key>",
		Short: "print an artifact's JS source",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := database.GetArtifact(ctx.DB, args[0])
			if err != nil {
				return err
			}
			fmt.Print(a.Source)
			return nil
		},
	}
}

func newAddCmd(ctx context.DnoteCtx) *cobra.Command {
	var label string
	cmd := &cobra.Command{
		Use:   "add <key> <file.js>",
		Short: "install an artifact from a JS file",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			src, err := os.ReadFile(args[1])
			if err != nil {
				return errors.Wrap(err, "reading source")
			}
			if label == "" {
				label = args[0]
			}
			a := database.Artifact{
				Key: args[0], Label: label, Version: 1, Source: string(src),
				CreatedBy: "user", CreatedAt: time.Now().UnixNano(), Enabled: true,
			}
			if err := a.Upsert(ctx.DB); err != nil {
				return err
			}
			fmt.Printf("→ installed artifact %s\n", args[0])
			return nil
		},
	}
	cmd.Flags().StringVar(&label, "label", "", "picker label (defaults to the key)")
	return cmd
}

func newRmCmd(ctx context.DnoteCtx) *cobra.Command {
	return &cobra.Command{
		Use:   "rm <key>",
		Short: "uninstall an artifact (its nodes fall back to bullets)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := database.DeleteArtifact(ctx.DB, args[0]); err != nil {
				return err
			}
			fmt.Printf("→ uninstalled artifact %s\n", args[0])
			return nil
		},
	}
}

func newEnableCmd(ctx context.DnoteCtx, enable bool) *cobra.Command {
	verb, short := "enable", "enable an artifact"
	if !enable {
		verb, short = "disable", "disable an artifact without uninstalling it"
	}
	return &cobra.Command{
		Use:   verb + " <key>",
		Short: short,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := database.SetArtifactEnabled(ctx.DB, args[0], enable); err != nil {
				return err
			}
			fmt.Printf("→ artifact %s %sd\n", args[0], verb)
			return nil
		},
	}
}
