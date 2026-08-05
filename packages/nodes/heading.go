package nodes

import (
	"github.com/lflow/lflow/packages/database"
)

// The heading nodes: H1/H2/H3, glyph the digit ("1"/"2"/"3") in bold yellow.
func init() {
	Register(Plugin{
		Key:            database.TypeH1,
		Label:          "Heading 1",
		InlineEditable: true,
		Glyph:          headingGlyph("1"),
	})
	Register(Plugin{
		Key:            database.TypeH2,
		Label:          "Heading 2",
		InlineEditable: true,
		Glyph:          headingGlyph("2"),
	})
	Register(Plugin{
		Key:            database.TypeH3,
		Label:          "Heading 3",
		InlineEditable: true,
		Glyph:          headingGlyph("3"),
	})
}

func headingGlyph(digit string) func() (string, string) {
	return func() (string, string) { return digit, Theme().Bold + Theme().Yellow }
}
