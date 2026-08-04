package nodes

import (
	"sort"
	"testing"

	"github.com/lflow/lflow/packages/database"
	"github.com/lflow/lflow/packages/editor"
)

// TestRegistryParity: database.TypeOrder and the editor's registry (built-ins
// + the plugins this package registers at init) are hand-maintained sets that
// must agree — a key on only one side means a type the editor renders but the
// CLI rejects, or a stored type that silently degrades to bullets. This
// package is the one that links both sides, so the check lives here.
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

// TestCodecTypeSets: every codec's Allowed() set and DefaultType() must stay
// inside the accepted type set — a stray key would let a file session offer
// (or create) nodes the rest of the system rejects.
func TestCodecTypeSets(t *testing.T) {
	for _, c := range fileCodecs {
		for k := range c.Allowed() {
			if !database.ValidTypes[k] {
				t.Errorf("%s: Allowed() contains unknown type %q", c.Name(), k)
			}
		}
		if d := c.DefaultType(); d != "" && !database.ValidTypes[d] {
			t.Errorf("%s: DefaultType() %q is not a valid type", c.Name(), d)
		}
		if d := c.DefaultType(); d != "" && c.Allowed() != nil && !c.Allowed()[d] {
			t.Errorf("%s: DefaultType() %q is outside its own Allowed() set", c.Name(), d)
		}
	}
}
