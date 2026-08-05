package nodes

import "github.com/lflow/lflow/packages/database"

// markup composed as an outline (see packages/editor/markup.go): the node's
// text is a tag with its attributes and its children are its child elements.
// Stays inline-editable; a known tag tints accent, attribute names dim, and an
// element row carries a dim linear preview of the document beneath it.
//
// Every hook here is Model-bound (the chip table, the outline-composed
// document, the run band) — none of it is pure over Ref/Theme, so onType/run/
// view/flashActions all stay in the editor's attachment (see markup.go's
// attachCoreHooks in packages/editor, shared with SVG).
func init() {
	Register(Plugin{
		Key:             database.TypeHTML,
		Label:           "HTML",
		InlineEditable:  true,
		ContinueOnEnter: true,
		RunInTail:       true,
	})
}
