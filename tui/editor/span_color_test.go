package editor

import (
	"strings"
	"testing"
)

// TestKeywordSpanColor: keywords tint, names do not, comments dim to the end.
func TestKeywordSpanColor(t *testing.T) {
	sc := keywordSpanColor(pythonKeywords, "#")
	runes := []rune("for x in items:  # loop")
	colors := sc(runes)
	if colors[0] == "" || colors[2] == "" {
		t.Fatal("`for` should be colored")
	}
	if colors[4] != "" {
		t.Fatal("`x` should not be colored")
	}
	hash := strings.IndexRune(string(runes), '#')
	for i := hash; i < len(runes); i++ {
		if colors[i] == "" {
			t.Fatalf("comment rune %d should be dim", i)
		}
	}
}
