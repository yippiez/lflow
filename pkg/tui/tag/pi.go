package tag

import (
	"context"
	"os"
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

// skillDir is the materialized lflow pi skill (see pi-tag at the repo root);
// the editor sets it at start and every turn passes it via --skill.
var skillDir string

// SetSkillDir records the lflow skill path for pi turns.
func SetSkillDir(dir string) { skillDir = dir }

// cliSystemPrompt frames the agent as the note-app assistant: plain concise
// replies, and how to speak in chips (the inline structured tokens lflow
// renders). name is the mentioned agent (@Pi, @Grok, …) so the prompt
// addresses it correctly.
func cliSystemPrompt(name string) string {
	return "You are " + name + ", an assistant living inside a terminal outline note-taking " +
		"app. A user mentioned you with @" + name + " in one of their outline nodes. Each " +
		"turn hands you a <NodeContext> block: the conversation as nested XML " +
		"mirroring the outline — one element per node, children nested inside " +
		"their parent. <asked> is the node to address, <answer> is one of your own " +
		"earlier replies, <node> is any other node, and an outermost <parent> is " +
		"the mention's parent node — where the thread sits in the outline, ambient " +
		"context only, not part of the conversation. Typed nodes wear their type " +
		"as the element instead of <node> — <todo done=\"true\">, <log time=\"…\">, " +
		"<code>, <json> (its document as the element body), <h1>…<h3>, <quote>, " +
		"<image> (caption only; pixels never travel) — same nesting rules. " +
		"That is all you are handed — for anything else in the outline, search it " +
		"yourself with the lflow CLI: `lflow node grep <text>` finds nodes, " +
		"`lflow node list <node>` reads a subtree (details in the lflow skill).\n" +
		"\n" +
		"Your reply lands as one node in a narrow terminal outline — long replies " +
		"wrap badly and bury the thread. Write it like a Slack reply, not a " +
		"report: plain text, no markdown headings or code fences, at most 3 " +
		"sentences unless the user explicitly asks for detail. Never narrate your process — no \"I'll look " +
		"into...\" openers and no \"Done, I changed X and Y\" reports; when you did " +
		"work, one line stating the outcome is enough. Do not open with \"Here " +
		"is...\", \"Based on...\", or a restatement of the question, and do not " +
		"close with a recap or a trailing offer like \"would you like me to...\", " +
		"\"if you want, I can...\", or \"let me know if...\". Text you print before " +
		"your last tool call is shown transiently while you work and then " +
		"discarded — put the whole reply after it.\n" +
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
		"Not every turn needs an answer. When the <asked> node does not mention " +
		"@you, the user may still be mid-thought — writing a multi-part answer to " +
		"your question, or notes that need no reply. If the node is clearly " +
		"addressed to you and complete, answer; otherwise reply with exactly PASS " +
		"and nothing else, and wait for the next turn."
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
	// pin the agent process to the editor process cwd at send time — same
	// "pwd where run" rule as $ chips (startBash). Empty → inherit.
	if pwd, err := os.Getwd(); err == nil && pwd != "" {
		opts.Cwd = pwd
	}
	if skillDir != "" {
		opts.Skills = []string{skillDir} // the lflow skill: CLI + chips
	}
	// No in-app model picker: each provider carries a baked-in default (see
	// agent.ProviderDefault). c.Provider is the agent's hardcoded backend
	// (@Pi → pi, @Grok → grok), set by ClientFor; its default model + thinking
	// ride every turn.
	opts.Model, opts.Thinking = agent.ProviderDefault(c.Provider)
	sess, err := agent.Run(ctx, c.Provider, turnPrompt(thread), opts)
	if err != nil {
		return nil, err
	}

	out := make(chan Event, 16)
	go func() {
		defer close(out)
		defer sess.Stop() // one turn, one process — nothing to come back to

		var reply strings.Builder
		var interim string // last narration dropped at a tool start — fallback only
		for ev := range sess.Events() {
			switch ev.Kind {
			case agent.EventToolStart, agent.EventToolUpdate:
				// text emitted before a tool call is process narration ("I'll look
				// up..."), not the answer — keep it out of the reply node; only what
				// follows the LAST tool call lands. The latest cut survives as a
				// fallback for turns that end without any closing text.
				if ev.Kind == agent.EventToolStart && reply.Len() > 0 {
					interim = reply.String()
					reply.Reset()
				}
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
				txt := strings.TrimSpace(reply.String())
				if txt == "" {
					txt = strings.TrimSpace(interim) // tools ran, no closing text — better than silence
				}
				if txt != "" && txt != "PASS" {
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

// turnPrompt is the message the agent actually receives each turn — full XML:
// the instruction in its own tag, the rendered thread in <NodeContext>, so the
// outline never mixes with the framing around it.
func turnPrompt(thread []ThreadNode) string {
	return "<instructions>\n" +
		"Answer the <asked> node in NodeContext, as one short chat message.\n" +
		"</instructions>\n" +
		"\n" +
		"<NodeContext>\n" + renderThread(thread) + "</NodeContext>"
}

// renderThread draws the context as nested XML mirroring the outline: one
// element per node, children nested inside their parent, two-space indent per
// level. The element name carries the role — <parent> is the mention's parent
// (ambient), <asked> is the node this turn addresses, <answer> is one of the
// agent's own earlier replies. Everything else wears its type's element when
// the type declares one (XMLTag/XMLAttrs/XMLBody, the editor registry's
// toContext hook — <todo done="true">, <log time="…">, a <json> with its
// multi-line document as body) and falls back to <node>. Attributes ride
// whichever element wins, so an asked todo still reads <asked done="false">.
func renderThread(thread []ThreadNode) string {
	tagFor := func(n ThreadNode) string {
		switch {
		case n.Parent:
			return "parent"
		case n.Asked:
			return "asked"
		case n.Role == "agent":
			return "answer"
		}
		if n.XMLTag != "" {
			return n.XMLTag
		}
		return "node"
	}

	var b strings.Builder
	type open struct {
		tag   string
		depth int
	}
	var stack []open
	closeTo := func(depth int) {
		for len(stack) > 0 && stack[len(stack)-1].depth >= depth {
			top := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			b.WriteString(strings.Repeat("  ", top.depth) + "</" + top.tag + ">\n")
		}
	}
	for i, n := range thread {
		closeTo(n.Depth)
		tag := tagFor(n)
		ind := strings.Repeat("  ", n.Depth)
		openTag := "<" + tag
		if n.XMLAttrs != "" {
			openTag += " " + n.XMLAttrs
		}
		openTag += ">"
		hasKids := i+1 < len(thread) && thread[i+1].Depth > n.Depth
		if n.XMLBody != "" {
			// a multi-line body owns the element's content: open tag on its own
			// line, body indented one level, children (if any) follow inside.
			b.WriteString(ind + openTag + "\n")
			for _, bl := range strings.Split(strings.TrimRight(n.XMLBody, "\n"), "\n") {
				b.WriteString(ind + "  " + bl + "\n")
			}
			if hasKids {
				stack = append(stack, open{tag, n.Depth})
			} else {
				b.WriteString(ind + "</" + tag + ">\n")
			}
			continue
		}
		line := ind + openTag + strings.TrimSpace(n.Name)
		if hasKids { // has children — stays open
			b.WriteString(line + "\n")
			stack = append(stack, open{tag, n.Depth})
		} else {
			b.WriteString(line + "</" + tag + ">\n")
		}
	}
	closeTo(0)
	return b.String()
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
