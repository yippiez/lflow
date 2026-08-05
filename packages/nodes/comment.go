package nodes

import (
	"github.com/lflow/lflow/packages/database"
)

// The comment node: a comment as a first-class citizen, keyword-free text —
// each codec adds its own lead on save (`# ` in .py, `// ` in .rs,
// `<!-- -->` in .md). The whole row renders dim.
func init() {
	Register(Plugin{
		Key:            database.TypeComment,
		Label:          "Comment",
		InlineEditable: true,
		Glyph:          func() (string, string) { return "◦", Theme().Dim },
		BaseColor:      func() string { return Theme().Dim },
	})
}
