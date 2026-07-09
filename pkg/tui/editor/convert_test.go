package editor

import "testing"

// convertBySign is registry-driven: any type whose descriptor sets `sign`
// converts when that sign is typed at the node start. Built-in signs are
// covered here; a mod-declared sign is covered in nodemod_test.go.
func TestConvertBySign(t *testing.T) {
	cases := []struct {
		name, sign, body, wantType string
	}{
		{"bash", "$", "ls -la", "bash"},
		{"query", "⌕", "todo today", "query"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			it := &item{uuid: "n", typ: "bullets", name: c.sign + c.body}
			tr := &tree{byUUID: map[string]*item{"n": it}}
			m := &Model{tree: tr, caret: len([]rune(c.sign))}
			if !m.convertBySign(it) {
				t.Fatalf("convertBySign should fire for %q", c.sign)
			}
			if it.typ != c.wantType {
				t.Errorf("type = %q, want %q", it.typ, c.wantType)
			}
			if it.name != c.body {
				t.Errorf("name = %q, want %q", it.name, c.body)
			}
			if m.caret != 0 {
				t.Errorf("caret = %d, want 0", m.caret)
			}
		})
	}

	// an unknown sign leaves the node untouched
	it := &item{uuid: "n", typ: "bullets", name: "~x"}
	tr := &tree{byUUID: map[string]*item{"n": it}}
	m := &Model{tree: tr, caret: 1}
	if m.convertBySign(it) {
		t.Fatal("convertBySign must not fire for an unregistered sign")
	}
}
