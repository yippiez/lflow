package tag

import (
	"context"
	"fmt"
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
func (m *MockClient) Send(ctx context.Context, agent, sessionID string, thread []ThreadNode) (<-chan Event, error) {
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

		if sessionID == "" {
			sessionID = fmt.Sprintf("s_%x", time.Now().UnixNano())
			if !emit(Event{Op: "session", ID: sessionID}) {
				return
			}
		}

		// exactly ONE reply node per turn — while the agent works, the status
		// bar's small "agent thinking…" text is the only progress signal; no
		// narration/thinking nodes ever land in the thread.
		prompt, mentioned := askedText(thread, agent)
		if key, label, source, ok := artifactFor(prompt); ok {
			if !emit(Event{Op: "artifact", Key: key, Label: label, Source: source}) {
				return
			}
			emit(Event{Op: "message", Placement: "thread", Text: "Installed the " + label + " artifact — set it on any node via /type; alt+r runs it. Tagged #" + key + " so it stays findable."})
			emit(Event{Op: "done"})
			return
		}

		// an untagged follow-up only earns a reply when it reads like a
		// question — a plain note keeps the agent silent (discretion, not
		// auto-reply). Mentions always answer, nested in the asked node's
		// thread; detected questions answer "below", message-board style.
		if !mentioned && !looksLikeQuestion(prompt) {
			emit(Event{Op: "done"})
			return
		}
		placement := "thread"
		if !mentioned {
			placement = "below"
		}
		followup := time.Now().AddDate(0, 0, 7).Format("2006-01-02")
		emit(Event{Op: "message", Placement: placement, Text: "My take: log the decision here, tag the open questions #followup, and mention me again on " + followup + " if the retries are still flaky."})
		emit(Event{Op: "done"})
	}()
	return ch, nil
}

// askedText is the turn's prompt — the marked asked node (falling back to the
// most recent user node) — and whether it mentions the agent.
func askedText(thread []ThreadNode, agent string) (string, bool) {
	prompt := ""
	for i := len(thread) - 1; i >= 0; i-- {
		if thread[i].Asked {
			prompt = thread[i].Name
			break
		}
		if prompt == "" && thread[i].Role == "user" {
			prompt = thread[i].Name
		}
	}
	return prompt, strings.Contains(prompt, "@"+agent)
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
