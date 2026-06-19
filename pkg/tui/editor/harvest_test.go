package editor

import "testing"

func newParseTree() *tree {
	root := &item{uuid: "root"}
	return &tree{
		root:          root,
		snapshots:     map[string]snapshot{},
		externalNames: map[string]string{},
		byUUID:        map[string]*item{"root": root},
	}
}

func TestParseDeliverableSingleNode(t *testing.T) {
	tr := newParseTree()
	items := parseDeliverable(tr, `[{"text":"Red is a primary color.","note":"hex #FF0000"}]`)
	if len(items) != 1 {
		t.Fatalf("want 1 node, got %d", len(items))
	}
	if items[0].name != "Red is a primary color." {
		t.Fatalf("name = %q", items[0].name)
	}
	if items[0].note != "hex #FF0000" {
		t.Fatalf("note = %q", items[0].note)
	}
}

func TestParseDeliverableNested(t *testing.T) {
	tr := newParseTree()
	md := `[{"text":"groceries","children":[{"text":"milk"},{"text":"eggs"}]},{"text":"chores"}]`
	items := parseDeliverable(tr, md)
	if len(items) != 2 {
		t.Fatalf("want 2 roots, got %d", len(items))
	}
	if items[0].name != "groceries" || len(items[0].children) != 2 {
		t.Fatalf("groceries should have 2 children, got name=%q kids=%d", items[0].name, len(items[0].children))
	}
	if items[0].children[0].name != "milk" || items[0].children[1].name != "eggs" {
		t.Fatalf("children = %q, %q", items[0].children[0].name, items[0].children[1].name)
	}
	if items[1].name != "chores" {
		t.Fatalf("second root = %q", items[1].name)
	}
}

func TestParseDeliverableToleratesBareObject(t *testing.T) {
	tr := newParseTree()
	items := parseDeliverable(tr, `{"text":"just one"}`)
	if len(items) != 1 || items[0].name != "just one" {
		t.Fatalf("bare object: got %d items", len(items))
	}
}

func TestParseDeliverableRejectsGarbage(t *testing.T) {
	tr := newParseTree()
	if items := parseDeliverable(tr, "not json at all"); items != nil {
		t.Fatalf("garbage should yield nil, got %d", len(items))
	}
}

func TestParseOutlineTextIndentNesting(t *testing.T) {
	tr := newParseTree()
	items := parseOutlineText(tr, "groceries\n  milk\n  eggs\nchores")
	if len(items) != 2 {
		t.Fatalf("want 2 roots, got %d", len(items))
	}
	if items[0].name != "groceries" || len(items[0].children) != 2 {
		t.Fatalf("groceries should nest 2, got %d", len(items[0].children))
	}
	if items[0].children[0].name != "milk" {
		t.Fatalf("first child = %q", items[0].children[0].name)
	}
}
