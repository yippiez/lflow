package nodes

import (
	"github.com/lflow/lflow/packages/database"
)

// The text node: a prose paragraph — markdown body text that is NOT a list
// item, and the structural node of the config codecs (json keys, toml lines).
// Markdown files stop being forced into an outline because of it: a plain
// line parses to text and renders back as a plain line, never as `- `.
func init() {
	Register(Plugin{
		Key:            database.TypeText,
		Label:          "Text",
		InlineEditable: true,
		Glyph:          func() (string, string) { return "¶", Theme().Dim },
	})
}
