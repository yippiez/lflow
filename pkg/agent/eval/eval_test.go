package eval

import (
	"context"
	"testing"

	"github.com/lflow/lflow/pkg/agent"
)

func TestParseSingleNode(t *testing.T) {
	cases := []struct {
		out        string
		wantCol    bool
		wantText   string
	}{
		{"COLLAPSE\nThe answer is 42.", true, "The answer is 42."},
		{"collapse\nlower works\nsecond line", true, "lower works\nsecond line"},
		{"KEEP", false, ""},
		{"KEEP\nblah", false, ""},
		{"", false, ""},
		{"COLLAPSE", false, ""},   // no body → keep
		{"COLLAPSE\n   ", false, ""}, // blank body → keep
		{"random noise", false, ""},
	}
	for _, c := range cases {
		text, col := parseSingleNode(c.out)
		if col != c.wantCol || text != c.wantText {
			t.Errorf("parseSingleNode(%q) = (%q,%v), want (%q,%v)", c.out, text, col, c.wantText, c.wantCol)
		}
	}
}

func TestModelFor(t *testing.T) {
	t.Setenv("LFLOW_EVAL_MODEL", "")
	if got := ModelFor("anthropic/claude-opus"); got != "anthropic/claude-haiku-4-5" {
		t.Errorf("claude → cheap haiku, got %q", got)
	}
	if got := ModelFor("anthropic/claude-haiku-4-5"); got != "anthropic/claude-haiku-4-5" {
		t.Errorf("haiku judges with itself, got %q", got)
	}
	if got := ModelFor("grok:grok-build"); got != "" {
		t.Errorf("unknown family opts out, got %q", got)
	}
	t.Setenv("LFLOW_EVAL_MODEL", "opencode:cheap/model")
	if got := ModelFor("anthropic/claude-opus"); got != "opencode:cheap/model" {
		t.Errorf("env override wins, got %q", got)
	}
}

func TestDrainText(t *testing.T) {
	ch := make(chan agent.Event, 8)
	ch <- agent.Event{Kind: agent.EventAgentText, Text: "hello "}
	ch <- agent.Event{Kind: agent.EventToolStart, Tool: "read"} // ignored
	ch <- agent.Event{Kind: agent.EventAgentText, Text: "world"}
	ch <- agent.Event{Kind: agent.EventTurnEnd, Status: "idle"}
	ch <- agent.Event{Kind: agent.EventAgentText, Text: "AFTER"} // not drained (turn ended)
	close(ch)
	if got := drainText(ch); got != "hello world" {
		t.Errorf("drainText = %q, want %q", got, "hello world")
	}
}

func TestSingleNodeUsesRunnerAndModel(t *testing.T) {
	t.Setenv("LFLOW_EVAL_MODEL", "test:cheap/model")
	orig := runner
	defer func() { runner = orig }()

	var gotModel, gotPrompt string
	runner = func(_ context.Context, model, system, prompt string) (string, error) {
		gotModel, gotPrompt = model, prompt
		return "COLLAPSE\nOne concise node.", nil
	}
	cond, col := SingleNode(context.Background(), "anthropic/claude-opus", "explain X", "a\n  b\n  c")
	if !col || cond != "One concise node." {
		t.Fatalf("got (%q,%v)", cond, col)
	}
	if gotModel != "test:cheap/model" {
		t.Errorf("eval model = %q, want the configured one", gotModel)
	}
	if !contains(gotPrompt, "explain X") || !contains(gotPrompt, "a\n  b\n  c") {
		t.Errorf("prompt missing task/deliverable: %q", gotPrompt)
	}
}

func TestSingleNodeSkipsWhenNoModel(t *testing.T) {
	t.Setenv("LFLOW_EVAL_MODEL", "")
	called := false
	orig := runner
	defer func() { runner = orig }()
	runner = func(context.Context, string, string, string) (string, error) { called = true; return "", nil }

	if _, col := SingleNode(context.Background(), "grok:grok-build", "t", "d"); col {
		t.Error("no eval model for grok → should keep")
	}
	if called {
		t.Error("runner must not be called when no eval model resolves")
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})()
}
