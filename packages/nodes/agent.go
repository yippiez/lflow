package nodes

import (
	"github.com/lflow/lflow/packages/database"
)

// The Agent node is RETIRED (2026-08-04): a session had two surfaces and the
// whole-row one was the worse of them, so the inline chip is the only one
// left. The entry survives as internal — never offered by /type — so a node
// somebody still has typed this way keeps its own name and its own row
// instead of reading as an unknown type.
func init() {
	Register(Plugin{
		Key:            database.TypeAgent,
		Label:          "Agent",
		Internal:       true,
		InlineEditable: true,
	})
}
