package editor

import (
	"sort"
	"testing"

	"github.com/lflow/lflow/packages/database"
)

// TestRegistryParity: database.TypeOrder (the CLI/DB-side node definitions) and
// the editor's registry (every type file registering its descriptor at init)
// are hand-maintained sets that must agree — a key on only one side means a
// type the editor renders but the CLI rejects, or a stored type that silently
// degrades to bullets. This package owns both sides, so the check lives here.
func TestRegistryParity(t *testing.T) {
	reg := map[string]bool{}
	for _, k := range RegisteredTypeKeys() {
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
