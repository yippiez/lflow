package root

import (
	"fmt"
	"strings"

	"github.com/fatih/color"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// help stays light: names and headers in the terminal's own color, only the
// descriptions muted gray
var helpDim = color.New(color.FgHiBlack)

func renderHelpFunc(cmd *cobra.Command, _ []string) {
	fmt.Fprint(cmd.OutOrStdout(), renderHelp(cmd))
}

func renderUsageFunc(cmd *cobra.Command) error {
	fmt.Fprint(cmd.OutOrStdout(), renderHelp(cmd))
	return nil
}

// renderHelp lays out help for any command: usage, subcommands, flags,
// global flags, and a dim trailing hint.
func renderHelp(cmd *cobra.Command) string {
	var b strings.Builder

	var subs []*cobra.Command
	for _, c := range cmd.Commands() {
		if c.IsAvailableCommand() {
			subs = append(subs, c)
		}
	}

	// command groups list their commands directly; only runnable commands
	// show a usage line, since it carries the argument shape
	if cmd.Runnable() || len(subs) == 0 {
		b.WriteString("usage\n")
		b.WriteString("  " + strings.TrimSuffix(cmd.UseLine(), " [flags]") + "\n")
	}
	if len(subs) > 0 {
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		b.WriteString("commands\n")
		width := 0
		for _, c := range subs {
			if len(c.Name()) > width {
				width = len(c.Name())
			}
		}
		for _, c := range subs {
			b.WriteString(fmt.Sprintf("  %-*s  %s\n",
				width, c.Name(), helpDim.Sprint(c.Short)))
		}
	}

	writeFlagSection(&b, "flags", cmd.NonInheritedFlags())
	writeFlagSection(&b, "global flags", cmd.InheritedFlags())

	if len(subs) > 0 {
		b.WriteString("\n" + helpDim.Sprintf("%s <command> --help shows command help", cmd.CommandPath()) + "\n")
	}

	return b.String()
}

func writeFlagSection(b *strings.Builder, title string, fs *pflag.FlagSet) {
	type entry struct{ left, desc string }
	var entries []entry
	width := 0

	fs.VisitAll(func(f *pflag.Flag) {
		if f.Hidden || f.Name == "help" {
			return
		}
		// flags line up at the same column as the command list
		left := "--" + f.Name
		if f.Shorthand != "" {
			left = "-" + f.Shorthand + ", --" + f.Name
		}
		if t := f.Value.Type(); t != "bool" {
			left += " " + t
		}
		desc := f.Usage
		if f.DefValue != "" && f.DefValue != "false" {
			desc += " · default " + f.DefValue
		}
		if len(left) > width {
			width = len(left)
		}
		entries = append(entries, entry{left, desc})
	})

	if len(entries) == 0 {
		return
	}
	b.WriteString("\n" + title + "\n")
	for _, e := range entries {
		b.WriteString(fmt.Sprintf("  %-*s  %s\n",
			width, e.left, helpDim.Sprint(e.desc)))
	}
}
