package editor

import (
	"testing"

	"github.com/lflow/lflow/pkg/tui/database"
)

func TestComplItemsQueryCmdFilter(t *testing.T) {
	m := &Model{compl: complState{kind: complQueryCmd, query: "aft"}}
	items := m.complItems()
	if len(items) != 1 || items[0].value != ":after:" {
		t.Fatalf("query-cmd filter 'aft' = %v, want [:after:]", items)
	}
}

func TestComplItemsTagsFromChips(t *testing.T) {
	m := &Model{chips: map[string]database.Chip{
		"1": {ID: "1", Kind: "tag", Value: "log"},
		"2": {ID: "2", Kind: "tag", Value: "logic"},
		"3": {ID: "3", Kind: "path", Value: "/x"},
	}}
	m.compl = complState{kind: complTag, query: "log"}
	items := m.complItems()
	if len(items) != 2 {
		t.Fatalf("tag filter 'log' = %v, want #log and #logic", items)
	}
	m.compl.query = "logi"
	if items = m.complItems(); len(items) != 1 || items[0].value != "logic" {
		t.Fatalf("tag filter 'logi' = %v, want [logic]", items)
	}
}

func TestApplyCompletionQueryCmd(t *testing.T) {
	it := &item{uuid: "n", typ: database.TypeQuery, name: "deploy :af"}
	tr := &tree{byUUID: map[string]*item{"n": it}}
	m := &Model{tree: tr, caret: len([]rune("deploy :af"))}
	m.compl = complState{kind: complQueryCmd, start: len([]rune("deploy ")), query: "af"}
	m.applyCompletion(it, m.complItems())
	if it.name != "deploy :after:" {
		t.Fatalf("name = %q, want %q", it.name, "deploy :after:")
	}
	if m.caret != len([]rune("deploy :after:")) {
		t.Errorf("caret = %d, want end of token", m.caret)
	}
}

func TestApplyCompletionTagInsertsChip(t *testing.T) {
	it := &item{uuid: "n", typ: database.TypeBullets, name: "note #lo"}
	tr := &tree{byUUID: map[string]*item{"n": it}}
	m := &Model{
		tree:   tr,
		rows:   []row{{it: it}}, // cursor on the node being typed (excluded from tag list)
		cursor: 0,
		caret:  len([]rune("note #lo")),
		chips:  map[string]database.Chip{"c": {ID: "c", Kind: "tag", Value: "log"}},
	}
	m.compl = complState{kind: complTag, start: len([]rune("note ")), query: "lo"}
	// highlight the existing #log tag
	items := m.complItems()
	m.applyCompletion(it, items)
	if !hasAnchor(it.name) {
		t.Fatalf("tag completion should splice a chip anchor, got %q", it.name)
	}
	// the resolved text should read "note #log"
	if got := database.ExpandAnchors(it.name, m.chips); got != "note #log" {
		t.Errorf("expanded = %q, want %q", got, "note #log")
	}
}
