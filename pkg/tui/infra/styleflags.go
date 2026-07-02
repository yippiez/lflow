package infra

import (
	"strings"

	"github.com/lflow/lflow/pkg/tui/style"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// StyleFlags is the shared set of node-styling flags reused by `add` and
// `node edit`, so both commands expose styling the same way: --style sets the
// whole token list, while --color and the attribute booleans refine it.
type StyleFlags struct {
	style     string
	color     string
	bold      bool
	italic    bool
	underline bool
	strike    bool
}

// Register declares the styling flags on the given flag set.
func (s *StyleFlags) Register(f *pflag.FlagSet) {
	f.StringVar(&s.style, "style", "", `set style tokens, e.g. "bold,color:red" ("" clears all)`)
	f.StringVar(&s.color, "color", "", "set text color: "+strings.Join(style.Colors, ", ")+` ("" clears)`)
	f.BoolVar(&s.bold, "bold", false, "bold text (--bold=false removes it)")
	f.BoolVar(&s.italic, "italic", false, "italic text")
	f.BoolVar(&s.underline, "underline", false, "underline text")
	f.BoolVar(&s.strike, "strike", false, "strikethrough text")
}

// Change reads the flags that were actually set on cmd into a style.Change,
// leaving untouched aspects nil so they survive an edit.
func (s *StyleFlags) Change(cmd *cobra.Command) style.Change {
	f := cmd.Flags()
	var ch style.Change
	if f.Changed("style") {
		ch.Style = &s.style
	}
	if f.Changed("color") {
		ch.Color = &s.color
	}
	if f.Changed("bold") {
		ch.Bold = &s.bold
	}
	if f.Changed("italic") {
		ch.Italic = &s.italic
	}
	if f.Changed("underline") {
		ch.Underline = &s.underline
	}
	if f.Changed("strike") {
		ch.Strike = &s.strike
	}
	return ch
}
