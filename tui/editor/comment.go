package editor

import "github.com/lflow/lflow/tui/database"

// The comment node: a comment as a first-class citizen, keyword-free text —
// each codec adds its own lead on save (`# ` in .py, `// ` in .rs,
// `<!-- -->` in .md). The whole row renders dim.
func init() {
	registerType(nodeType{
		key:            database.TypeComment,
		label:          "Comment",
		inlineEditable: true,
		glyph: func(*item) (string, string) {
			return "◦", cDim
		},
		baseColor: func(*item) string { return cDim },
	})
}
