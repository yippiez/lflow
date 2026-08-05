package nodes

import "github.com/lflow/lflow/packages/database"

// markup composed as an outline (see packages/editor/markup.go): the node's
// text is a tag with its attributes and its children are its child elements.
// Stays inline-editable; a known tag tints accent, attribute names dim, and an
// element row carries a dim linear preview of the document beneath it.
//
// Every hook here is Model-bound (the chip table, the outline-composed
// document, the rasterizer subprocess, the rendered blob) — none of it is
// pure over Ref/Theme, so onType/run/view/flashActions/bands all stay in the
// editor's attachment (see markup.go's attachCoreHooks in packages/editor,
// shared with HTML).
func init() {
	Register(Plugin{
		Key:             database.TypeSVG,
		Label:           "SVG",
		InlineEditable:  true,
		ContinueOnEnter: true,
		// deliberately no cliDeps: there is no ONE binary this needs. It tries
		// several rasterizers in turn (see svgRasterizers), so declaring the
		// preferred one would warn "missing dependency" on a machine where the
		// node works perfectly through the next one down.
	})
}
