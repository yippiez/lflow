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

func TestParseMarkdownProseIsOneNode(t *testing.T) {
	tr := newParseTree()
	items := parseMarkdownItems(tr, "A binary search tree is a node-based structure.\nEach node has two children.")
	if len(items) != 1 {
		t.Fatalf("prose: want 1 node, got %d", len(items))
	}
	if items[0].name != "A binary search tree is a node-based structure." {
		t.Fatalf("name = %q", items[0].name)
	}
	if items[0].note != "Each node has two children." {
		t.Fatalf("note = %q", items[0].note)
	}
}

func TestParseMarkdownBulletsIntoSubtree(t *testing.T) {
	tr := newParseTree()
	md := "- groceries\n  - milk\n  - eggs\n- chores"
	items := parseMarkdownItems(tr, md)
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

func TestParseMarkdownHeadingNestsBullets(t *testing.T) {
	tr := newParseTree()
	md := "# Summary\n- point one\n- point two"
	items := parseMarkdownItems(tr, md)
	if len(items) != 1 || items[0].name != "Summary" {
		t.Fatalf("want one Summary root, got %d (%q)", len(items), func() string {
			if len(items) > 0 {
				return items[0].name
			}
			return ""
		}())
	}
	if len(items[0].children) != 2 {
		t.Fatalf("Summary should nest 2 bullets, got %d", len(items[0].children))
	}
}
