package nodes

import (
	"github.com/lflow/lflow/packages/database"
	"github.com/lflow/lflow/packages/editor"
)

// The comment node: a comment as a first-class citizen, keyword-free text —
// each codec adds its own lead on save (`# ` in .py, `// ` in .rs,
// `<!-- -->` in .md). The whole row renders dim.
func init() {
	editor.RegisterNodePlugin(editor.NodePlugin{
		Key:            database.TypeComment,
		Label:          "Comment",
		InlineEditable: true,
		Glyph:          func() (string, string) { return "◦", editor.NodeTheme().Dim },
		BaseColor:      func() string { return editor.NodeTheme().Dim },
	})
}
