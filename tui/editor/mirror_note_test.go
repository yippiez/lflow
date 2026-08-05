package editor

import (
	"strings"
	"testing"

	"github.com/lflow/lflow/tui/database"
)

// TestReadonlyRegionMirrorNoteAcrossTempFocus is the stashed-main-outline
// scenario: the Temporary Domain is focused, so m.tree is the temp tree (db ==
// nil, since the domain is never persisted) while the read-only region above
// it renders the STASHED main tree through renderRow(tr, ...). A mirror row in
// that region whose source carries a note used to resolve the note through
// m.tree instead of tr — missing the source in the temp tree's byUUID and
// falling through to database.GetNode on a nil *database.DB, panicking the
// editor. The note must resolve through tr, exactly like the row's glyph
// already does (see TestRenderRowParityReadonlyRegion).
func TestReadonlyRegionMirrorNoteAcrossTempFocus(t *testing.T) {
	root := &item{uuid: "root", typ: database.TypeBullets}
	tr := &tree{
		root:          root,
		byUUID:        map[string]*item{root.uuid: root},
		externalNames: map[string]string{},
	} // db is nil here too — GetNode must never be reached for this tree either
	add := func(it *item) {
		it.parent = root
		root.children = append(root.children, it)
		tr.byUUID[it.uuid] = it
	}
	src := &item{uuid: "src", typ: database.TypeBullets, name: "source node", note: "the note text"}
	add(src)
	mir := &item{uuid: "mir", typ: database.TypeBullets, mirrorOf: "src"}
	add(mir)

	m := &Model{tree: tr, viewStack: []*item{root}}
	m.refreshRows()

	// enterTemp stashes the main tree and points m.tree at a fresh scratch tree
	// whose db is nil — exactly the state the crash needs.
	m.enterTemp()

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("readonlyRegionLines panicked resolving a mirror's note across the stashed tree: %v", r)
		}
	}()
	mainRoot := m.mainStash.viewStack[len(m.mainStash.viewStack)-1]
	lines := m.readonlyRegionLines(m.mainStash.tree, mainRoot, m.mainStash.cursor, 20, 80, false, -1)
	plain := stripSGR(strings.Join(lines, "\n"))
	if !strings.Contains(plain, "the note text") {
		t.Errorf("stashed-region mirror note missing: %q", plain)
	}
}
