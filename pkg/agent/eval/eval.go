// Package eval runs small, cheap-model checks on agent output — e.g. whether a
// worker over-structured its deliverable when one node would do. Each eval is a
// single headless model turn (no tools); any failure degrades to "no opinion" so
// an eval never blocks or breaks the worker it judges.
package eval

import (
	"context"
	"os"
	"strings"

	"github.com/lflow/lflow/pkg/agent"
)

// ModelFor returns the model an eval should run on for a worker using workerModel,
// or "" to skip (no cheap model resolvable). Resolution: $LFLOW_EVAL_MODEL wins;
// otherwise a known family maps to its cheap tier (so Claude workers get an eval
// out of the box). Evals add a call per worker, so unknown models opt out rather
// than spend the worker's own (expensive) model.
func ModelFor(workerModel string) string {
	if v := strings.TrimSpace(os.Getenv("LFLOW_EVAL_MODEL")); v != "" {
		return v
	}
	m := agent.ParseModel(workerModel)
	n := strings.ToLower(m.Name)
	switch {
	case strings.Contains(n, "haiku"):
		return workerModel // already cheap — judge with itself
	case strings.Contains(n, "claude") || strings.Contains(n, "opus") || strings.Contains(n, "sonnet"):
		// cheap Claude tier on the same backend; a wrong id just fails the eval,
		// which degrades to "keep the original deliverable".
		m.Upstream, m.Name = "anthropic", "claude-haiku-4-5"
		return m.String()
	}
	return ""
}

// runner runs one headless turn and returns the final assistant text. It is a var
// so tests can stub the model call without a live backend.
var runner = runOnce

func runOnce(ctx context.Context, model, system, prompt string) (string, error) {
	mdl := agent.ParseModel(model)
	sess, err := agent.Run(ctx, mdl.CLI, prompt, agent.RunOptions{
		Model:        mdl,
		Thinking:     "off",
		SystemPrompt: system,
	})
	if err != nil {
		return "", err
	}
	defer sess.Stop()
	return drainText(sess.Events()), sess.Err()
}

// drainText collects assistant text from an event stream until the first turn
// ends (one turn is enough for an eval) or the stream closes.
func drainText(events <-chan agent.Event) string {
	var b strings.Builder
	for ev := range events {
		switch ev.Kind {
		case agent.EventAgentText:
			b.WriteString(ev.Text)
		case agent.EventTurnEnd:
			return strings.TrimSpace(b.String())
		}
	}
	return strings.TrimSpace(b.String())
}
