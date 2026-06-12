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

package root

import (
	"fmt"
	"strings"

	"github.com/fatih/color"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// the help palette mirrors the rest of the output: blue section headers,
// yellow names, muted gray prose
var (
	helpHeader = color.New(color.FgBlue)
	helpName   = color.New(color.FgYellow)
	helpDim    = color.New(color.FgHiBlack)
)

func renderHelpFunc(cmd *cobra.Command, _ []string) {
	fmt.Fprint(cmd.OutOrStdout(), renderHelp(cmd))
}

func renderUsageFunc(cmd *cobra.Command) error {
	fmt.Fprint(cmd.OutOrStdout(), renderHelp(cmd))
	return nil
}

// renderHelp lays out help for any command: title, usage, subcommands,
// flags, global flags, and a dim trailing hint.
func renderHelp(cmd *cobra.Command) string {
	var b strings.Builder

	b.WriteString(fmt.Sprintf("%s %s %s\n",
		helpName.Sprint(cmd.CommandPath()), helpDim.Sprint("·"), cmd.Short))

	var subs []*cobra.Command
	for _, c := range cmd.Commands() {
		if c.IsAvailableCommand() {
			subs = append(subs, c)
		}
	}

	useLine := strings.TrimSuffix(cmd.UseLine(), " [flags]")
	if len(subs) > 0 && !cmd.Runnable() {
		useLine = cmd.CommandPath() + " <command>"
	}
	b.WriteString("\n" + helpHeader.Sprint("usage") + "\n")
	b.WriteString("  " + useLine + "\n")
	if len(subs) > 0 {
		b.WriteString("\n" + helpHeader.Sprint("commands") + "\n")
		width := 0
		for _, c := range subs {
			if len(c.Name()) > width {
				width = len(c.Name())
			}
		}
		for _, c := range subs {
			b.WriteString(fmt.Sprintf("  %s  %s\n",
				helpName.Sprintf("%-*s", width, c.Name()), helpDim.Sprint(c.Short)))
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
		left := "    --" + f.Name
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
	b.WriteString("\n" + helpHeader.Sprint(title) + "\n")
	for _, e := range entries {
		b.WriteString(fmt.Sprintf("  %s  %s\n",
			helpName.Sprintf("%-*s", width, e.left), helpDim.Sprint(e.desc)))
	}
}
