// Package suggest is the edit-suggestion API: proposed changes to the outline
// that land in a review queue instead of the tree. An author suggests a new
// node (`suggest add`) or new text for an existing one (`suggest edit`); a
// reviewer lists, shows, approves or rejects them. Nothing in the outline
// changes until approval — the whole point is that a suggestion is inert until
// somebody says yes.
//
// Every subcommand is one-shot and pipe-friendly like the rest of the CLI, and
// speaks to the daemon through the ordinary wire protocol, so suggestions made
// by an agent on one client show up for a reviewer on another.
package suggest

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/fatih/color"
	"github.com/lflow/lflow/pkg/tui/context"
	"github.com/lflow/lflow/pkg/tui/database"
	"github.com/spf13/cobra"
)

var (
	dim    = color.New(color.FgHiBlack)
	yellow = color.New(color.FgYellow)
	red    = color.New(color.FgRed)
	green  = color.New(color.FgGreen)
)

// NewCmd returns the suggest command group.
func NewCmd(ctx context.DnoteCtx) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "suggest",
		Short: "Propose or resolve node changes",
	}

	cmd.AddCommand(newAddCmd(ctx))
	cmd.AddCommand(newEditCmd(ctx))
	cmd.AddCommand(newStateCmd(ctx, true))
	cmd.AddCommand(newStateCmd(ctx, false))
	cmd.AddCommand(newListCmd(ctx))
	cmd.AddCommand(newShowCmd(ctx))
	cmd.AddCommand(newReviewCmd(ctx, database.SuggestApproved))
	cmd.AddCommand(newReviewCmd(ctx, database.SuggestRejected))

	return cmd
}

// resolveRef turns a suggestion reference into a suggestion, printing the
// standard miss/ambiguity output and exiting rather than returning an error —
// the same shape node references use.
func resolveRef(db *database.DB, ref string) database.Suggestion {
	s, err := database.ResolveSuggestion(db, ref)
	if err == nil {
		return s
	}
	switch e := err.(type) {
	case database.ErrNoSuggestion:
		fmt.Println(red.Sprint("→ ") + fmt.Sprintf("no suggestion matching %s", yellow.Sprintf("%q", ref)))
		fmt.Println(dim.Sprint("  hint: lflow suggest list shows ids"))
	case database.ErrAmbiguousSuggestion:
		fmt.Println(red.Sprint("→ ") + fmt.Sprintf("%s matches %d suggestions", yellow.Sprintf("%q", ref), len(e.Matches)))
		for _, m := range e.Matches {
			fmt.Printf("    %s  %s\n", dim.Sprint(database.ShortID(m.UUID)), dim.Sprint(m.Status))
		}
	default:
		fmt.Println(red.Sprint("→ ") + err.Error())
	}
	os.Exit(1)
	return database.Suggestion{}
}

// targetLine renders the node a suggestion is about, chips resolved, for
// listings and confirmations.
func targetLine(db *database.DB, s database.Suggestion) string {
	if s.TargetUUID == "" {
		return "root"
	}
	n, err := database.GetNode(db, s.TargetUUID)
	if err != nil {
		return database.ShortID(s.TargetUUID) + " (gone)"
	}
	chips, _ := database.LoadChips(db)
	return database.DisplayAnchors(n.Name, chips)
}

// display resolves chip anchors in text a suggestion stored raw from the CLI.
func display(db *database.DB, text string) string {
	if !database.HasAnchor(text) {
		return text
	}
	chips, _ := database.LoadChips(db)
	return database.DisplayAnchors(text, chips)
}

// printJSON writes a value as indented JSON, the machine-readable face of
// every read command here.
func printJSON(v interface{}) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(b))
	return nil
}

// truncate shortens text to fit a listing column.
func truncate(s string, max int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max-1]) + "…"
}
