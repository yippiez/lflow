package nodes

import "github.com/lflow/lflow/packages/database"

// The wf node type: a Workflowy mirror. The node's text holds a pasted
// Workflowy link (or bare node id); alt+r pulls that subtree through the API
// and reconciles it in place as readonly children, each bound to its
// Workflowy id in the wf_nodes table. Every pulled node is itself a wf
// mirror — alt+r on any of them refreshes just that branch (the
// recursive-mirror model). The integration is read-only today; the id map is
// the two-way hook for later.
func init() {
	Register(Plugin{
		Key:            database.TypeWF,
		Label:          "Workflowy",
		InlineEditable: true,
		Glyph:          wfGlyph,
	})
}

// wfGlyph is the mirror mark: ◈ in the accent color, the live diamond that
// marks a Workflowy-backed node.
func wfGlyph() (string, string) { return "◈", Theme().Accent }
