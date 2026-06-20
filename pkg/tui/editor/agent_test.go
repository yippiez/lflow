package editor

import (
	"strings"
	"testing"
)

func TestAgentInlineTextSanitizes(t *testing.T) {
	// newlines and tabs collapse to spaces (single-row safe), control bytes drop
	got := agentInlineText("line one\nline two\tcol\x07\x1b")
	if strings.ContainsAny(got, "\n\t\x07\x1b") {
		t.Fatalf("agentInlineText left control/whitespace bytes: %q", got)
	}
	if got != "line one line two col" {
		t.Fatalf("agentInlineText = %q", got)
	}
}

func TestAgentBlockTextKeepsNewlines(t *testing.T) {
	// notes keep real newlines but still drop tabs/control bytes
	got := agentBlockText("a\nb\tc\x07")
	if got != "a\nb c" {
		t.Fatalf("agentBlockText = %q", got)
	}
}

func TestAgentNodeLinesWrap(t *testing.T) {
	long := strings.Repeat("word ", 40)
	lines := agentNodeLines("", 0, long, 30)
	if len(lines) < 2 {
		t.Fatalf("expected a long row to wrap to multiple lines, got %d", len(lines))
	}
}
