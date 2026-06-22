package agent

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

// TestLiveAllBackends exercises every installed CLI backend end-to-end against
// the real provider: it confirms each yields a streamed answer and a clean turn
// end — the events the worker transcript is built from. It is gated behind
// LFLOW_LIVE=1 (and skips any CLI that is not on PATH) so the normal suite never
// shells out or spends tokens. Run with: LFLOW_LIVE=1 go test --tags fts5 -run
// TestLiveAllBackends -v ./pkg/agent
func TestLiveAllBackends(t *testing.T) {
	if os.Getenv("LFLOW_LIVE") != "1" {
		t.Skip("set LFLOW_LIVE=1 to run live backend tests")
	}
	for _, p := range []Provider{ProviderPi, ProviderOpencode, ProviderGrok} {
		p := p
		t.Run(string(p), func(t *testing.T) {
			b, ok := Get(p)
			if !ok || !b.Available() {
				t.Skipf("%s not available", p)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()

			sess, err := Run(ctx, p, "Reply with exactly the single word: pong", RunOptions{
				SessionID: "lflow-live-" + string(p), SessionDir: t.TempDir(),
			})
			if err != nil {
				t.Fatalf("%s Run: %v", p, err)
			}

			var text strings.Builder
			gotTurnEnd := false
			for ev := range sess.Events() {
				switch ev.Kind {
				case EventAgentText:
					text.WriteString(ev.Text)
				case EventTurnEnd:
					gotTurnEnd = true
					sess.Stop() // one turn is enough; close the stream
				case EventError:
					t.Logf("%s error event: %s", p, ev.Text)
				}
			}
			if e := sess.Err(); e != nil {
				t.Logf("%s terminal err: %v", p, e)
			}
			answer := strings.TrimSpace(text.String())
			t.Logf("%s → turnEnd=%v answer=%q", p, gotTurnEnd, answer)
			if !gotTurnEnd {
				t.Errorf("%s never reached a clean turn end", p)
			}
			// How a turn's answer reaches the transcript differs by backend:
			//   - opencode / grok stream it as assistant text → flushDeliverable →
			//     the deliverable, so they MUST produce non-empty text here.
			//   - pi (rpc) never emits raw assistant text; a worker delivers via the
			//     finish_worker tool, so a clean turn end is the contract here.
			if p != ProviderPi && answer == "" {
				t.Errorf("%s produced no assistant text — transcript would be empty", p)
			}
		})
	}
}
