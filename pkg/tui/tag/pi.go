package tag

import (
	"context"
	"strings"

	"github.com/lflow/lflow/pkg/agent"
)

// PiClient bridges @mentions to the local `pi` coding agent (pkg/agent's RPC
// backend). Launch-and-forget: each Send spawns a FRESH pi turn fed the whole
// rendered thread and saves nothing — the thread in the outline is the only
// conversation state, so edits to past nodes are always honored.
type PiClient struct{}

// modsDir is where the NodeMod files live (<config>/lflow/mods) — the editor
// sets it at start so the system prompt can tell pi to create and edit mods
// directly.
var modsDir string

// SetModsDir records the mods directory for the system prompt.
func SetModsDir(dir string) { modsDir = dir }

// skillDir is the materialized lflow pi skill (see pi-tag at the repo root);
// the editor sets it at start and every turn passes it via --skill.
var skillDir string

// SetSkillDir records the lflow skill path for pi turns.
func SetSkillDir(dir string) { skillDir = dir }

// piSystemPrompt frames pi as the note-app assistant: plain concise replies,
// how to speak in chips (the inline structured tokens lflow renders), and
// where the node-type files live so pi can write them itself.
func piSystemPrompt() string {
	p := "You are Pi, an assistant living inside a terminal outline note-taking " +
		"app. A user mentioned you with @Pi in one of their outline nodes. You are " +
		"given the conversation as an indented outline: the mentioned node and " +
		"everything beneath it; the line marked [ASKED] is the one to address. " +
		"Below the thread, a 'visible on screen' section lists what else the user " +
		"is looking at right now — ambient context only, not part of the " +
		"conversation. That is all you are handed — for anything else in the " +
		"outline, search it yourself with the " +
		"lflow CLI: `lflow node grep <text>` finds nodes, `lflow node list <node>` " +
		"reads a subtree (details in the lflow skill). Reply with a single, concise " +
		"answer — plain text, no markdown headings or code fences, at most a few " +
		"sentences. Do not repeat the question back.\n" +
		"\n" +
		"Chips: you may embed these inline tokens anywhere in your reply; the app " +
		"renders each as a structured chip.\n" +
		"  {{cmd:ls -la}} — a runnable shell command; the user runs it in place with " +
		"alt+r. When asked for a command, answer with a cmd chip, not prose.\n" +
		"  {{path:/etc/hosts}} — a file or directory path.\n" +
		"  {{link:label|https://example.com}} — a link; the label is optional " +
		"({{link:https://example.com}}).\n" +
		"  #tags and YYYY-MM-DD dates become chips automatically — write them plainly.\n" +
		"Never wrap a chip token in quotes or backticks.\n" +
		"\n" +
		"Not every turn needs an answer. When the [ASKED] line does not mention " +
		"@you, the user may still be mid-thought — writing a multi-part answer to " +
		"your question, or notes that need no reply. If the line is clearly " +
		"addressed to you and complete, answer; otherwise reply with exactly PASS " +
		"and nothing else, and wait for the next turn."
	if modsDir != "" {
		p += "\n\nCustom node types (NodeMods) live in " + modsDir + ": <type>.js " +
			"defines the node type <type>, and a <type>/ directory with a mod.json " +
			"({name, description, entry}) holds a git-installed mod. A .disabled " +
			"suffix on either turns it off. When asked for a new node type, write " +
			"the file yourself, mirroring the lflow.registerType calls in the " +
			"existing files there — the app reloads the directory when your turn ends."
	}
	return p
}

// Send runs one fresh pi turn over the thread and streams the reply as tag
// events. No session id, --no-session: nothing accumulates in pi's storage,
// and pi's context every turn is exactly the thread as it reads right now.
func (c *PiClient) Send(ctx context.Context, agentName string, thread []ThreadNode) (<-chan Event, error) {
	// Where the reply lands mirrors the ask: a fresh @mention owns its thread,
	// so the reply nests under it as a child; an untagged follow-up inside the
	// thread reads as chat, so the reply posts BELOW it (next sibling) and the
	// conversation stays flat — until another @mention opens a sub-thread.
	placement := "below"
	for _, n := range thread {
		if n.Asked && strings.Contains(n.Name, "@"+agentName) {
			placement = "thread"
			break
		}
	}

	opts := agent.RunOptions{
		NoSession:    true, // launch-and-forget: the thread IS the memory
		SystemPrompt: piSystemPrompt(),
	}
	if skillDir != "" {
		opts.Skills = []string{skillDir} // the lflow skill: CLI, chips, NodeMods
	}
	// No in-app model picker: each provider carries a baked-in default (see
	// agent.ProviderDefault). Prefer pi; fall back to grok when only grok is
	// installed. The provider's default model + thinking ride every turn.
	provider := agent.ProviderPi
	if !agent.Available(agent.ProviderPi) && agent.Available(agent.ProviderGrok) {
		provider = agent.ProviderGrok
	}
	opts.Model, opts.Thinking = agent.ProviderDefault(provider)
	sess, err := agent.Run(ctx, provider, renderThread(thread), opts)
	if err != nil {
		return nil, err
	}

	out := make(chan Event, 16)
	go func() {
		defer close(out)
		defer sess.Stop() // one turn, one process — nothing to come back to

		var reply strings.Builder
		for ev := range sess.Events() {
			switch ev.Kind {
			case agent.EventToolStart, agent.EventToolUpdate:
				// live "what it's doing now" — the editor shows the last one as a
				// muted band under the running mention. Nothing lands in the outline.
				out <- Event{Op: "tool", Tool: ev.Tool, Text: ev.Detail}
			case agent.EventAgentText:
				if t := strings.TrimSpace(ev.Text); t != "" {
					if reply.Len() > 0 {
						reply.WriteString("\n")
					}
					reply.WriteString(t)
				}
			case agent.EventError:
				out <- Event{Op: "error", Text: ev.Text}
				return
			case agent.EventTurnEnd:
				if ev.Status == "error" {
					out <- Event{Op: "error", Text: firstNonEmpty(strings.TrimSpace(reply.String()), "pi turn failed")}
					return
				}
				// "PASS" is pi declining a discretionary turn (the user is
				// mid-thought) — the turn ran, no reply node lands
				if txt := strings.TrimSpace(reply.String()); txt != "" && txt != "PASS" {
					out <- Event{Op: "message", Placement: placement, Text: txt}
				}
				out <- Event{Op: "done"}
				return
			}
		}
		// stream closed without a turn end — surface pi's terminal error
		if e := sess.Err(); e != nil {
			out <- Event{Op: "error", Text: e.Error()}
			return
		}
		out <- Event{Op: "done"}
	}()
	return out, nil
}

// renderThread flattens the context into an indented outline prompt for pi:
// the thread first, then the ambient "visible on screen" section.
func renderThread(thread []ThreadNode) string {
	var b, scr strings.Builder
	for _, n := range thread {
		dst := &b
		if n.Screen {
			dst = &scr
		}
		dst.WriteString(strings.Repeat("  ", n.Depth))
		dst.WriteString("- ")
		if n.Asked {
			dst.WriteString("[ASKED] ")
		}
		if n.Role == "agent" {
			dst.WriteString("(Pi earlier) ")
		}
		dst.WriteString(strings.TrimSpace(n.Name))
		dst.WriteByte('\n')
	}
	if scr.Len() > 0 {
		b.WriteString("\nAlso visible on the user's screen right now (ambient context, not part of the thread):\n")
		b.WriteString(scr.String())
	}
	return b.String()
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// piAvailable reports whether any local CLI backend can serve a real agent
// (pi, grok, … — see pkg/agent's registry).
func piAvailable() bool {
	for _, b := range agent.Backends() {
		if b.Available() {
			return true
		}
	}
	return false
}
