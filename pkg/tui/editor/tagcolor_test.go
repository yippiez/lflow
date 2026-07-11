package editor

import (
	"strings"
	"testing"

	"github.com/lflow/lflow/pkg/tui/database"
)

// TestTagColorAssignAndRender drives the tag color feature: alt+e on a tag
// resolves the word, picking a color persists it, and both render paths (plain
// span and chip) paint the pill; "none" clears back to muted gray.
func TestTagColorAssignAndRender(t *testing.T) {
	db := database.InitTestMemoryDB(t)
	defer func() { tagColors = map[string]string{} }()

	root := &item{uuid: "root"}
	n := &item{uuid: "n1", name: "review #urgent now", parent: root}
	root.children = []*item{n}
	tr := &tree{db: db, root: root, byUUID: map[string]*item{"root": root, "n1": n},
		externalNames: map[string]string{}, snapshots: map[string]snapshot{}}
	m := &Model{db: db, tree: tr, viewStack: []*item{root}, width: 100, height: 30,
		chips: map[string]database.Chip{}}
	m.refreshRows()

	// the caret inside the plain-text "#urgent" resolves the tag word
	m.caret = strings.Index("review #urgent now", "#urgent") + 2
	word, ok := m.tagWordAtCaret(n)
	if !ok || word != "urgent" {
		t.Fatalf("tagWordAtCaret = %q,%v want urgent", word, ok)
	}

	// picking red persists and paints
	m.openTagColor(word)
	tagColorSource{}.onSelect(m, pickerItem{value: "red"})
	if tagColors["urgent"] != "red" {
		t.Fatalf("tagColors = %v", tagColors)
	}
	saved, err := database.AllTagColors(db)
	if err != nil || saved["urgent"] != "red" {
		t.Fatalf("persisted colors = %v (%v)", saved, err)
	}
	if tagColorSGR("urgent") == "" || !strings.Contains(tagColorSGR("urgent"), "[48;") {
		t.Fatal("assigned tag must render a background pill")
	}
	// the plain-span render path carries the pill
	body := renderBody(n, n.name, -1, false, m.chips, false)
	if !strings.Contains(body, "[48;2;244;71;71m") {
		t.Fatalf("plain tag span must wear the red pill, got %q", body)
	}

	// a tag CHIP paints the same pill
	c := database.Chip{ID: "t1", Kind: chipKindTag, Value: "urgent"}
	m.chips[c.ID] = c
	n.name = "review " + "￼" + "t1" + "￼" + " now"
	body = renderBody(n, n.name, -1, false, m.chips, false)
	if !strings.Contains(body, "[48;2;244;71;71m") {
		t.Fatalf("tag chip must wear the red pill, got %q", body)
	}

	// "none" clears the assignment
	m.openTagColor("urgent")
	tagColorSource{}.onSelect(m, pickerItem{value: "none"})
	if _, ok := tagColors["urgent"]; ok {
		t.Fatal("none must clear the in-memory color")
	}
	if saved, _ := database.AllTagColors(db); len(saved) != 0 {
		t.Fatalf("none must clear the persisted color, got %v", saved)
	}
	if tagColorSGR("urgent") != "" {
		t.Fatal("cleared tag must fall back to muted gray")
	}
}
