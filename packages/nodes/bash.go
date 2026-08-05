package nodes

import "github.com/lflow/lflow/packages/database"

// The Bash node is a shell command composed AS an outline (see bashnode.go): a
// "$" row whose children are its parts — a join operator (| && || ;) or a
// wrapper ($() ()) composes them, anything else heads them — so a long
// pipeline is written as a readable tree instead of one wide line. alt+r runs
// THIS node's subtree, alt+e opens the output. The inline cmd chip ("$$",
// cmdchip.go) remains the one-liner surface.
func init() {
	Register(Plugin{
		Key:             database.TypeBash,
		Label:           "Bash",
		InlineEditable:  true,
		ContinueOnEnter: true,
		RunInTail:       true,
		CLIDeps:         []string{"bash"},
		Glyph:           bashGlyph,
	})
}

// bashGlyph marks the row with the same red "$" the cmd chip wears — the two
// shell surfaces are one thing, and a Bash node is only the tree form of it.
// The glyph never changes: not on a fold, not while the run's terminal is
// open.
func bashGlyph() (string, string) { return "$", Theme().Red }
