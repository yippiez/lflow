package nodes

import (
	"sort"
	"testing"

	"github.com/lflow/lflow/packages/database"
	"github.com/lflow/lflow/packages/editor"
	_ "github.com/lflow/lflow/packages/fileeditor" // python/rust statement nodes register here
)

// TestRegistryParity: database.TypeOrder and the editor's registry (built-ins
// + the plugins this package and packages/fileeditor register at init) are
// hand-maintained sets that must agree — a key on only one side means a type
// the editor renders but the CLI rejects, or a stored type that silently
// degrades to bullets. This package is the one that links all three sides, so
// the check lives here.
func TestRegistryParity(t *testing.T) {
	reg := map[string]bool{}
	for _, k := range editor.RegisteredTypeKeys() {
		reg[k] = true
	}
	var missing, extra []string
	for _, k := range database.TypeOrder {
		if !reg[k] {
			missing = append(missing, k)
		}
	}
	for k := range reg {
		if !database.ValidTypes[k] {
			extra = append(extra, k)
		}
	}
	sort.Strings(missing)
	sort.Strings(extra)
	if len(missing) > 0 {
		t.Errorf("in database.TypeOrder but not registered in the editor: %v", missing)
	}
	if len(extra) > 0 {
		t.Errorf("registered in the editor but not in database.TypeOrder: %v", extra)
	}
}
