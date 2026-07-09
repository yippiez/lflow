package editor

import "testing"

// convertBySign is the one keyboard sign: "$" at the node start becomes bash.
// No other prefix converts — log and every other mod are set via /type only.
func TestConvertBySign(t *testing.T) {
	// "$" converts to bash and drops the sign
	it := &item{uuid: "n", typ: "bullets", name: "$ls -la"}
	tr := &tree{byUUID: map[string]*item{"n": it}}
	m := &Model{tree: tr, caret: 1}
	if !m.convertBySign(it) {
		t.Fatal("'$' must convert to bash")
	}
	if it.typ != "bash" || it.name != "ls -la" || m.caret != 0 {
		t.Fatalf("bash convert = typ %q name %q caret %d", it.typ, it.name, m.caret)
	}

	// "->" / "→" are plain text now: log owns no keyboard trigger
	for _, sign := range []string{"->", "→"} {
		it := &item{uuid: "n", typ: "bullets", name: sign + "deployed"}
		tr := &tree{byUUID: map[string]*item{"n": it}}
		m := &Model{tree: tr, caret: len([]rune(sign))}
		if m.convertBySign(it) {
			t.Fatalf("%q must not convert — log is a plain mod", sign)
		}
	}
}
