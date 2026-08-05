package editor

import "github.com/lflow/lflow/packages/database"

// The heading nodes: H1/H2/H3, glyph the digit ("1"/"2"/"3") in bold yellow.
func init() {
	for _, h := range []nodeType{
		{key: database.TypeH1, label: "Heading 1"},
		{key: database.TypeH2, label: "Heading 2"},
		{key: database.TypeH3, label: "Heading 3"},
	} {
		digit := h.key[len(h.key)-1:] // "1"/"2"/"3"
		h.inlineEditable = true
		h.glyph = func(*item) (string, string) { return digit, cBold + cYellow }
		registerType(h)
	}
}
