package editor

import "testing"

func TestConvertBySign(t *testing.T) {
	cases := []struct {
		name, sign, wantType, wantName string
	}{
		{"bash", "$", "bash", "ls -la"},
		{"log dash", "->", "log", "deployed"},
		{"log arrow", "→", "log", "deployed"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			body := "ls -la"
			if c.wantType == "log" {
				body = "deployed"
			}
			it := &item{uuid: "n", typ: "bullets", name: c.sign + body}
			tr := &tree{byUUID: map[string]*item{"n": it}}
			m := &Model{tree: tr, caret: len([]rune(c.sign))}
			if !m.convertBySign(it) {
				t.Fatalf("convertBySign should fire for %q", c.sign)
			}
			if it.typ != c.wantType {
				t.Errorf("type = %q, want %q", it.typ, c.wantType)
			}
			if it.name != c.wantName {
				t.Errorf("name = %q, want %q", it.name, c.wantName)
			}
			if m.caret != 0 {
				t.Errorf("caret = %d, want 0", m.caret)
			}
		})
	}
}
