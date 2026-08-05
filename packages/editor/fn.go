package editor

import "github.com/lflow/lflow/packages/database"

// The fn node: a function as a first-class, language-neutral citizen. Its
// text is the signature without the language's keyword — `greet(name)` — and
// its body is the children; each file codec puts the syntax back on save
// (`def greet(name):` in .py, `fn greet(name) {` in .rs). The ƒ glyph names
// it in the outline, the tail shows the folded body size.
func init() {
	registerType(nodeType{
		key:            database.TypeFn,
		label:          "Function",
		inlineEditable: true,
		glyph: func(*item) (string, string) {
			return "ƒ", cYellow
		},
		bodyTail: codeBodyTail,
	})
}
