package tag

import (
	"context"
	"strings"
	"time"
)

// MockClient is the offline stand-in for a Pi service: deterministic canned
// turns with the same event stream a live service produces, including a real
// artifact-generation path — so the whole @mention feature (and its tests and
// demos) works with no network and no credentials.
type MockClient struct {
	Delay time.Duration // pause before each event; 0 in tests, human-ish by default
}

func (m *MockClient) delay() time.Duration {
	if m.Delay > 0 {
		return m.Delay
	}
	return 400 * time.Millisecond
}

// Send answers one turn. A thread asking to create an artifact/node type gets
// a generated artifact installed; anything else gets a short read of the
// thread plus a canned suggestion.
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
		// Placement: the mention node is the channel (thread root, depth 0) —
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

		if key, label, source, ok := artifactFor(asked.Name); ok {
			if !emit(Event{Op: "artifact", Key: key, Source: source}) {
				return
			}
			emit(Event{Op: "message", Placement: "thread", Text: "Installed the " + label + " artifact — set it on any node via /type; alt+r runs it. Tagged #" + key + " so it stays findable."})
			emit(Event{Op: "done"})
			return
		}

		switch {
		case asked.Type == "bash" || asked.Type == "code":
			// an unprompted review comment, attached to the code as its thread
			emit(Event{Op: "message", Placement: "thread", Text: "Review: this retries on every exit code — gate it on the transient curl exits (52, 56) so hard failures fail fast, and cap the attempts."})
		case parentType(thread, asked) == "agent":
			// continuing a conversation inside a message's reply thread
			emit(Event{Op: "message", Placement: "below", Text: "Yes — 3 attempts with exponential backoff (2s, 4s, 8s) is plenty; past that it's an outage, not flakiness. Tag it #decided."})
		case mentioned && asked.Depth == 0:
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
// the most recent user THREAD node; the Screen section is ambient, never the
// subject) — and whether it mentions the agent.
func askedNode(thread []ThreadNode, agent string) (ThreadNode, bool) {
	var asked ThreadNode
	for i := len(thread) - 1; i >= 0; i-- {
		if thread[i].Asked {
			asked = thread[i]
			break
		}
		if asked.UUID == "" && thread[i].Role == "user" && !thread[i].Screen {
			asked = thread[i]
		}
	}
	return asked, strings.Contains(asked.Name, "@"+agent)
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

// artifactFor recognises an artifact request and picks a template. The mock
// keeps a tiny catalogue; a live service would generate the JS instead.
func artifactFor(prompt string) (key, label, source string, ok bool) {
	p := strings.ToLower(prompt)
	if !strings.Contains(p, "artifact") && !strings.Contains(p, "node type") {
		return "", "", "", false
	}
	switch {
	case strings.Contains(p, "dice"):
		return "dice", "Dice", artifactDice, true
	case strings.Contains(p, "weather"):
		return "weather", "Weather", artifactWeather, true
	default:
		return "stamp", "Stamp", artifactStamp, true
	}
}

const artifactDice = `// Dice — a roll-on-demand node: alt+r rolls, output lands under the node.
lflow.registerType({
    key: "dice", label: "Dice", sign: "⚂ ", inlineEditable: true,
    glyph: function (node) { return ["⚂", node.color || "yellow"]; },
    run: function (node) {
        var n = parseInt(node.name, 10) || 6;
        return "echo rolled $(( (RANDOM % " + n + ") + 1 )) of " + n;
    },
});
`

const artifactWeather = `// Weather — node text is the location; alt+r fetches a one-line report.
lflow.registerType({
    key: "weather", label: "Weather", sign: "☂ ", inlineEditable: true,
    glyph: function (node) { return ["☂", node.color || "cyan"]; },
    run: function (node) {
        return "curl -s 'wttr.in/" + encodeURIComponent(node.name) + "?format=3'";
    },
});
`

const artifactStamp = `// Stamp — a bullet that wears its creation time on its sleeve.
lflow.registerType({
    key: "stamp", label: "Stamp", inlineEditable: true,
    glyph: function (node) { return ["◷", node.color || "cyan"]; },
    prefix: function (node) {
        return lflow.style("(" + lflow.time(node.addedOn) + ") ", "dim");
    },
});
`
