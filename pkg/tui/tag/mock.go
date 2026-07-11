package tag

import (
	"context"
	"strings"
	"time"
)

// MockClient is the offline stand-in for a Pi service: deterministic canned
// turns with the same event stream a live service produces — so the whole
// @mention feature (and its tests and demos) works with no network and no
// credentials.
type MockClient struct {
	Delay time.Duration // pause before each event; 0 in tests, human-ish by default
}

func (m *MockClient) delay() time.Duration {
	if m.Delay > 0 {
		return m.Delay
	}
	return 400 * time.Millisecond
}

// Send answers one turn: a short read of the thread plus a canned suggestion.
func (m *MockClient) Send(ctx context.Context, agent string, thread []ThreadNode) (<-chan Event, error) {
	ch := make(chan Event, 16)
	go func() {
		defer close(ch)
		emit := func(ev Event) bool {
			select {
			case <-time.After(m.delay()):
			case <-ctx.Done():
				return false
			}
			select {
			case ch <- ev:
				return true
			case <-ctx.Done():
				return false
			}
		}

		// exactly ONE reply node per turn — while the agent works, the status
		// bar's "N thinking" count is the only progress signal; no narration
		// nodes ever land in the conversation.
		//
		// Placement: the mention node is the channel (the thread root) —
		// replies to it nest as its children ("thread"). Deeper in, detected
		// questions get board replies ("below"), a review comment on committed
		// code nests on the code node ("thread"), inside a reply thread the
		// conversation continues "below", and plain notes stay silent.
		asked, mentioned := askedNode(thread, agent)

		// stream a little tool activity first, so the running mention shows the
		// live band (Read → Grep → Thinking…) before any reply lands.
		for _, tc := range []Event{
			{Op: "tool", Tool: "read", Text: asked.Name},
			{Op: "tool", Tool: "grep", Text: "importer retries"},
			{Op: "thinking"},
		} {
			if !emit(tc) {
				return
			}
		}

		switch {
		case asked.Type == "bash" || asked.Type == "code":
			// an unprompted review comment, attached to the code as its thread
			emit(Event{Op: "message", Placement: "thread", Text: "Review: this retries on every exit code — gate it on the transient curl exits (52, 56) so hard failures fail fast, and cap the attempts."})
		case parentType(thread, asked) == "agent":
			// continuing a conversation inside a message's reply thread
			emit(Event{Op: "message", Placement: "below", Text: "Yes — 3 attempts with exponential backoff (2s, 4s, 8s) is plenty; past that it's an outage, not flakiness. Tag it #decided."})
		case mentioned && asked.UUID == threadRoot(thread).UUID:
			// answering the thread root itself: nest under the mention
			followup := time.Now().AddDate(0, 0, 7).Format("2006-01-02")
			emit(Event{Op: "message", Placement: "thread", Text: "Retry only the transient failures, log each attempt as a log node, and revisit on " + followup + " if it is still flaky."})
		case mentioned || looksLikeQuestion(asked.Name):
			followup := time.Now().AddDate(0, 0, 7).Format("2006-01-02")
			emit(Event{Op: "message", Placement: "below", Text: "Retry only the transient failures, log each attempt as a log node, and revisit on " + followup + " if it is still flaky."})
		}
		emit(Event{Op: "done"})
	}()
	return ch, nil
}

// askedNode is the turn's subject — the marked asked node (falling back to
// the most recent user THREAD node; the Parent line is ambient, never the
// subject) — and whether it mentions the agent.
func askedNode(thread []ThreadNode, agent string) (ThreadNode, bool) {
	var asked ThreadNode
	for i := len(thread) - 1; i >= 0; i-- {
		if thread[i].Asked {
			asked = thread[i]
			break
		}
		if asked.UUID == "" && thread[i].Role == "user" && !thread[i].Parent {
			asked = thread[i]
		}
	}
	return asked, strings.Contains(asked.Name, "@"+agent)
}

// threadRoot is the mention node — the first non-Parent line (the Parent
// line, when present, sits above the thread as ambient context).
func threadRoot(thread []ThreadNode) ThreadNode {
	for _, n := range thread {
		if !n.Parent {
			return n
		}
	}
	return ThreadNode{}
}

// parentType reconstructs the asked node's parent from the depth-first thread
// (the nearest preceding node one level shallower) — how the mock knows a
// commit happened inside an agent message's reply thread.
func parentType(thread []ThreadNode, asked ThreadNode) string {
	idx := -1
	for i, n := range thread {
		if n.UUID == asked.UUID {
			idx = i
			break
		}
	}
	for i := idx - 1; i >= 0; i-- {
		if thread[i].Role == "context" {
			break
		}
		if thread[i].Depth == asked.Depth-1 {
			return thread[i].Type
		}
	}
	return ""
}

// looksLikeQuestion is the mock's stand-in for a model's judgement call on
// whether an untagged note wants an answer.
func looksLikeQuestion(text string) bool {
	t := strings.ToLower(strings.TrimSpace(text))
	if strings.HasSuffix(t, "?") {
		return true
	}
	for _, w := range []string{"how ", "what ", "where ", "why ", "when ", "should ", "can ", "could ", "is ", "are ", "do ", "does "} {
		if strings.HasPrefix(t, w) {
			return true
		}
	}
	return false
}

