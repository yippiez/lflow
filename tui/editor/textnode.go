package editor

import "github.com/lflow/lflow/tui/database"

// The text node: a prose paragraph — markdown body text that is NOT a list
// item, and the structural node of the config codecs (json keys, toml lines).
// Markdown files stop being forced into an outline because of it: a plain
// line parses to text and renders back as a plain line, never as `- `.
func init() {
	registerType(nodeType{
		key:            database.TypeText,
		label:          "Text",
		inlineEditable: true,
		glyph: func(*item) (string, string) {
			return "¶", cDim
		},
	})
}
