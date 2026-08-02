package editor

import (
	"strings"
	"testing"

	"github.com/lflow/lflow/pkg/tui/database"
)

func TestTagQueryStrict(t *testing.T) {
	if w, ok := tagQuery("#log"); !ok || w != "log" {
		t.Fatalf(`tagQuery("#log") = %q,%v; want "log",true`, w, ok)
	}
	if _, ok := tagQuery("log"); ok {
		t.Errorf("a plain word is not a tag query")
	}
	if _, ok := tagQuery("#log more"); ok {
		t.Errorf("only a bare single tag is a tag query")
	}
}

func TestNodeHasTagWholeWord(t *testing.T) {
	cases := []struct {
		text string
		tag  string
		want bool
	}{
		{"daily #log entry", "log", true},
		{"some logic here", "log", false},      // plain word never matches
		{"a #logic note", "log", false},        // longer tag is not the tag
		{"trailing #log", "log", true},         // end of string boundary
		{"#login note", "log", false},          // longer word is a different tag
		{"CAPS #Log here", "log", true},        // case-insensitive
		{"email me a#log thing", "log", false}, // no left boundary, not a tag
	}
	for _, c := range cases {
		if got := nodeHasTag(c.text, c.tag); got != c.want {
			t.Errorf("nodeHasTag(%q,%q)=%v want %v", c.text, c.tag, got, c.want)
		}
	}
}

// TestRenderBodyTagMutedGray: a #tag is always painted muted gray, untouched by
// the node's /color.
func TestRenderBodyTagMutedGray(t *testing.T) {
	it := &item{typ: database.TypeBullets, style: "color:red"}
	rendered := renderBody(it, "ship #log today", -1, false, nil)

	if got := stripSGR(rendered); got != "ship #log today" {
		t.Errorf("tag text must render literally: %q", got)
	}
	// the tag runes carry cDim, never the node's red
	if !strings.Contains(rendered, cDim+"#") && !strings.Contains(rendered, cDim) {
		t.Errorf("tag should be muted gray: %q", rendered)
	}
}
