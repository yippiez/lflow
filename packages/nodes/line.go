package nodes

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/lflow/lflow/packages/database"
)

// The Line node type: ordinary dialogue text with a character attached, shown
// as a colored "[NAME] " prefix — never baked into the stored name (the
// WARNING invariant that no markup leaks into stored text holds here too). A
// character is shared across every Line node that names it, so its identity
// and color live in editor-private tables (node uuid → character name,
// character name → style color) keyed nowhere Ref exposes — LinePrefix is a
// bridge var the editor rebinds at init (see plugin.go), so this file itself
// stays Model-free. Expand opens the character picker, a Model-bound list UI
// — also a bridge var (OpenCharacterPicker).
func init() {
	Register(Plugin{
		Key:             database.TypeLine,
		Label:           "Line",
		InlineEditable:  true,
		ContinueOnEnter: true,
		// wrapped, not assigned directly: LinePrefix is a bridge var the editor
		// rebinds at ITS init, which runs after this package's — a direct field
		// assignment would freeze in the pre-rebind default.
		Prefix: func(n Ref) string { return LinePrefix(n) },
		Expand: func(h Host, n Ref) tea.Cmd {
			OpenCharacterPicker(h, n)
			return nil
		},
	})
}
