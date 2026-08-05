package nodes

import "github.com/lflow/lflow/packages/database"

// An image: alt+r pastes from the host clipboard, alt+e opens the half-block
// preview, alt+o hands the PNG to the desktop's own image viewer. The pixels
// are a PNG blob in node_blobs keyed by node uuid — so the outline stays one
// portable SQLite file; the name holds an optional caption.
//
// Every hook here is Model-bound (the decoded-image cache in the per-node
// store, the DB blob, the async clipboard grab, the host-viewer launch) — none
// of it is pure over Ref/Theme, so renderM/run/view/openHost/flashActions/
// bands all stay in the editor's attachment (see image.go's attachCoreHooks in
// packages/editor).
func init() {
	Register(Plugin{
		Key:            database.TypeImage,
		Label:          "Image",
		InlineEditable: false,
	})
}
