package nodes

import (
	"github.com/lflow/lflow/packages/database"
)

// The fn node: a function as a first-class, language-neutral citizen. Its
// text is the signature without the language's keyword — `greet(name)` — and
// its body is the children; each file codec puts the syntax back on save
// (`def greet(name):` in .py, `fn greet(name) {` in .rs). The ƒ glyph names
// it in the outline, the tail shows the folded body size.
func init() {
	Register(Plugin{
		Key:            database.TypeFn,
		Label:          "Function",
		InlineEditable: true,
		Glyph:          func() (string, string) { return "ƒ", Theme().Yellow },
		BodyTail:       CodeBodyTail,
	})
}
