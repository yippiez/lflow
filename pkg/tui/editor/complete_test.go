package editor

import (
	"testing"

	"github.com/lflow/lflow/pkg/tui/database"
)

func TestComplItemsQueryCmdFilter(t *testing.T) {
	m := &Model{compl: complState{kind: complQueryCmd}}
	items := m.complItems("aft")
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
	m.compl = complState{kind: complTag}
	items := m.complItems("log")
	if len(items) != 2 {
		t.Fatalf("tag filter 'log' = %v, want #log and #logic", items)
	}
	if items = m.complItems("logi"); len(items) != 1 || items[0].value != "logic" {
		t.Fatalf("tag filter 'logi' = %v, want [logic]", items)
	}
}

func TestApplyCompletionQueryCmd(t *testing.T) {
	it := &item{uuid: "n", typ: database.TypeQuery, name: "deploy :af"}
	tr := &tree{byUUID: map[string]*item{"n": it}}
	m := &Model{tree: tr, caret: len([]rune("deploy :af"))}
	m.compl = complState{kind: complQueryCmd, start: len([]rune("deploy "))}
	items := m.complItems("af")
	m.applyCompletion(it, pickerItem{value: items[0].value})
	if it.name != "deploy :after:" {
		t.Fatalf("name = %q, want %q", it.name, "deploy :after:")
	}
	if m.caret != len([]rune("deploy :after:")) {
		t.Errorf("caret = %d, want end of token", m.caret)
	}
}

func TestQueryTypeCompletionChainsToValues(t *testing.T) {
	it := &item{uuid: "n", typ: database.TypeQuery, name: ":ty"}
	m := &Model{tree: &tree{byUUID: map[string]*item{"n": it}}, caret: 3,
		compl: complState{kind: complQueryCmd, start: 0}}
	if !m.applyCompletion(it, pickerItem{value: ":type:"}) {
		t.Fatal("selecting :type: must keep completion open for its value")
	}
	if m.compl.kind != complQueryType || m.compl.start != len([]rune(":type:")) {
		t.Fatalf("type value completion state = %+v", m.compl)
	}
	items := m.complItems("to")
	if len(items) == 0 || items[0].value != database.TypeTodo {
		t.Fatalf("type values for 'to' = %v", items)
	}
	m.applyCompletion(it, pickerItem{value: database.TypeTodo})
	if it.name != ":type:todo" {
		t.Fatalf("completed query = %q", it.name)
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
	m.compl = complState{kind: complTag, start: len([]rune("note "))}
	// highlight the existing #log tag
	items := m.complItems("lo")
	m.applyCompletion(it, pickerItem{value: items[0].value})
	if !hasAnchor(it.name) {
		t.Fatalf("tag completion should splice a chip anchor, got %q", it.name)
	}
	// the resolved text should read "note #log"
	if got := database.ExpandAnchors(it.name, m.chips); got != "note #log" {
		t.Errorf("expanded = %q, want %q", got, "note #log")
	}
}
