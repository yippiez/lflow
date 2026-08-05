package editor

import "github.com/lflow/lflow/packages/database"

// Thinking is a plain marker node: always the muted-gray thinking glyph, no
// other behavior.
func init() {
	registerType(nodeType{
		key:            database.TypeThinking,
		label:          "Thinking",
		inlineEditable: true,
		prefix:         thinkingPrefix, // the ※ rides in the body, never the glyph column
		baseColor:      func(*item) string { return cDim },
		fixedColor:     true,
	})
}

func thinkingPrefix(*item) string { return cDim + "※ " + cReset }
