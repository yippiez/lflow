package editor

import (
	"testing"

	"github.com/lflow/lflow/tui/database"
)

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
