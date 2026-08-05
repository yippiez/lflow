package nodes

import (
	"github.com/lflow/lflow/packages/database"
)

// Thinking is a plain marker node: always the muted-gray thinking glyph, no
// other behavior.
func init() {
	Register(Plugin{
		Key:            database.TypeThinking,
		Label:          "Thinking",
		InlineEditable: true,
		Prefix:         thinkingPrefix, // the ※ rides in the body, never the glyph column
		BaseColor:      func() string { return Theme().Dim },
		FixedColor:     true,
	})
}

func thinkingPrefix(n Ref) string { return Theme().Dim + "※ " + Theme().Reset }
