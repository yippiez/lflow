package nodes

import (
	"github.com/lflow/lflow/packages/database"
	"github.com/lflow/lflow/packages/editor"
)

// The text node: a prose paragraph — markdown body text that is NOT a list
// item, and the structural node of the config codecs (json keys, toml lines).
// Markdown files stop being forced into an outline because of it: a plain
// line parses to text and renders back as a plain line, never as `- `.
func init() {
	editor.RegisterNodePlugin(editor.NodePlugin{
		Key:            database.TypeText,
		Label:          "Text",
		InlineEditable: true,
		Glyph:          func() (string, string) { return "¶", editor.NodeTheme().Dim },
		ToContext: func(h editor.NodeHost, n editor.NodeRef) (string, string, string) {
			return "text", "", n.Text()
		},
	})
}
