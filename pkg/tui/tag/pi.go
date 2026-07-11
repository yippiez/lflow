package tag

import (
	"context"
	"strings"

	"github.com/lflow/lflow/pkg/agent"
)

// CLIClient bridges @mentions to a local CLI coding agent — pi or grok, via
// pkg/agent's backend registry. Launch-and-forget: each Send spawns a FRESH
// turn fed the whole rendered thread and saves nothing — the thread in the
// outline is the only conversation state, so edits to past nodes are always
// honored. Provider is the agent's hardcoded backend (pi or grok), set by
// ClientFor — there is no cross-backend fallback: @Grok runs grok or nothing.
type CLIClient struct {
	Provider agent.Provider
}

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

// cliSystemPrompt frames the agent as the note-app assistant: plain concise
// replies, how to speak in chips (the inline structured tokens lflow renders),
// and where the node-type files live so the agent can write them itself. name
// is the mentioned agent (@Pi, @Grok, …) so the prompt addresses it correctly.
func cliSystemPrompt(name string) string {
	p := "You are " + name + ", an assistant living inside a terminal outline note-taking " +
		"app. A user mentioned you with @" + name + " in one of their outline nodes. You are " +
		"given the conversation as a branched tree (│ ├─ ╰─ connectors, the way " +
		"the outline draws): the mentioned node and everything beneath it; the " +
		"line marked [ASKED] is the one to address. The top line marked [PARENT] " +
		"is the mention's parent node — where the thread sits in the outline, " +
		"ambient context only, not part of the conversation. That is all you are " +
		"handed — for anything else in the outline, search it yourself with the " +
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
func (c *CLIClient) Send(ctx context.Context, agentName string, thread []ThreadNode) (<-chan Event, error) {
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
		SystemPrompt: cliSystemPrompt(agentName),
	}
	if skillDir != "" {
		opts.Skills = []string{skillDir} // the lflow skill: CLI, chips, NodeMods
	}
	// No in-app model picker: each provider carries a baked-in default (see
	// agent.ProviderDefault). c.Provider is the agent's hardcoded backend
	// (@Pi → pi, @Grok → grok), set by ClientFor; its default model + thinking
	// ride every turn.
	opts.Model, opts.Thinking = agent.ProviderDefault(c.Provider)
	sess, err := agent.Run(ctx, c.Provider, renderThread(agentName, thread), opts)
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
					// the model is reasoning/answering past its last tool — reset
					// the live band to "Thinking…" so it doesn't freeze on the tool.
					out <- Event{Op: "thinking"}
				}
			case agent.EventError:
				out <- Event{Op: "error", Text: ev.Text}
				return
			case agent.EventTurnEnd:
				if ev.Status == "error" {
					out <- Event{Op: "error", Text: firstNonEmpty(strings.TrimSpace(reply.String()), agentName+" turn failed")}
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

// Branch glyphs for the rendered thread — the connector language the outline
// itself draws, so the agent sees the thread shaped the way the user does.
const (
	treeTee   = "├─ "
	treeElbow = "╰─ "
	treeGuide = "│  "
	treeBlank = "   "
)

// renderThread draws the context as a branched tree prompt for the agent: the
// Parent line (when present) as the top, the mention and its subtree hanging
// beneath it with │ ├─ ╰─ connectors. name labels the agent's own earlier
// replies so it recognizes its prior turns.
func renderThread(name string, thread []ThreadNode) string {
	var b strings.Builder
	// lastAt[d] — whether the node last emitted at depth d closed its sibling
	// run; the guide column under a closed run goes blank instead of │
	var lastAt []bool
	for i, n := range thread {
		last := true
		for j := i + 1; j < len(thread); j++ {
			if thread[j].Depth < n.Depth {
				break
			}
			if thread[j].Depth == n.Depth {
				last = false
				break
			}
		}
		for len(lastAt) <= n.Depth {
			lastAt = append(lastAt, false)
		}
		lastAt[n.Depth] = last
		for lvl := 1; lvl < n.Depth; lvl++ {
			if lastAt[lvl] {
				b.WriteString(treeBlank)
			} else {
				b.WriteString(treeGuide)
			}
		}
		if n.Depth > 0 {
			if last {
				b.WriteString(treeElbow)
			} else {
				b.WriteString(treeTee)
			}
		}
		if n.Parent {
			b.WriteString("[PARENT] ")
		}
		if n.Asked {
			b.WriteString("[ASKED] ")
		}
		if n.Role == "agent" {
			b.WriteString("(" + name + " earlier) ")
		}
		b.WriteString(strings.TrimSpace(n.Name))
		b.WriteByte('\n')
	}
	return b.String()
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
