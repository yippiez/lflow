package editor

import (
	"strings"
	"testing"

	"github.com/lflow/lflow/pkg/tui/database"
)

// TestQuitViewIsolatesLines guards the ctrl+c scrollback dump against SGR
// leaking in from the last live frame. The dump must reset the pen at the start
// of every line: otherwise a blue UI element left active by the previous frame
// (a focused json node's keys, the finder's ▸ mark, a truncated-away reset)
// bleeds its color onto the first dumped rows, so plain log/bullet nodes show
// up blue in scrollback even though the live editor renders them gray.
func TestQuitViewIsolatesLines(t *testing.T) {
	root := &item{uuid: "root"}
	log := &item{uuid: "l", name: "deployed thing", typ: database.TypeLog} // no /color
	bul := &item{uuid: "b", name: "a bullet", typ: database.TypeBullets}
	log.parent, bul.parent = root, root
	root.children = []*item{log, bul}

	tr := &tree{root: root, byUUID: map[string]*item{"root": root, "l": log, "b": bul}}
	m := &Model{width: 80, height: 24}
	m.tree = tr
	m.quitting = true

	out := m.View()
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) < 2 {
		t.Fatalf("expected at least the two node rows, got %d: %q", len(lines), out)
	}
	for i, l := range lines {
		if !strings.HasPrefix(l, cReset) {
			t.Errorf("quit-dump line %d does not start with a reset (color can leak in): %q", i, l)
		}
		if !strings.HasSuffix(l, cReset) {
			t.Errorf("quit-dump line %d does not end with a reset (color can leak out): %q", i, l)
		}
	}

	// an uncolored log must stay muted gray in the dump, never tinted (e.g. blue).
	const blue = "\x1b[38;2;86;156;214m"
	if strings.Contains(out, blue) {
		t.Errorf("uncolored nodes unexpectedly carry the accent/blue color in the quit dump: %q", out)
	}
}
