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
// Cwd/SkillDir, when set, override the process globals — the daemon runs
// turns on a client's behalf and passes the client's environment through.
type CLIClient struct {
	Provider agent.AgentProvider
	Cwd      string
	SkillDir string
}

// skillDir is the materialized lflow pi skill (see pi-tag at the repo root);
// the editor sets it at start and every turn passes it via --skill.
var skillDir string

// SetSkillDir records the lflow skill path for pi turns.
func SetSkillDir(dir string) { skillDir = dir }

// SkillDir returns the recorded lflow skill path — the editor forwards it to
// the daemon with each turn so a daemon-side run uses the same skill.
func SkillDir() string { return skillDir }

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
		"Siblings in <NodeContext> are always chronological — oldest first, the " +
		"last one is the newest. Note that in the user's outline a conversation " +
		"may DISPLAY inverted (priority-up nodes stack new messages and your " +
		"replies on top), so `lflow node list` can show the same children " +
		"newest-first; trust <NodeContext> for conversation order. " +
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
		"Attachments: special nodes hang as children under your reply comment — " +
		"not conversation bullets. Use them for code, images, json, quotes, logs, " +
		"runnable shell, and other typed content. Inline form (body must not " +
		"contain `}` — use the block form when it does):\n" +
		"  {{attach:bash|go test ./...}}\n" +
		"  {{attach:image|caption}} or {{attach:image|/abs/path.png|caption}}\n" +
		"  {{attach:quote|ship when green}}\n" +
		"Block form for multi-line or braced bodies:\n" +
		"  {{attach:code}}\n" +
		"  package main\n" +
		"  func main() {}\n" +
		"  {{/attach}}\n" +
		"  {{attach:json}}\n" +
		"  {\"env\": \"prod\"}\n" +
		"  {{/attach}}\n" +
		"Types: code, image, bash (lands as a runnable $ chip child), json, quote, " +
		"log, todo, h1–h3, query, or any other node type key. Keep the comment " +
		"short; put the payload in attachments.\n" +
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
	return c.run(ctx, agentName, cliSystemPrompt(agentName), turnPrompt(thread), placement)
}

// SendPrompt runs one RAW turn — system + prompt as-is, no thread framing.
// The NLPCompute code generator speaks this: it wants a single answer to a
// bespoke instruction, not a chat reply into an outline.
func (c *CLIClient) SendPrompt(ctx context.Context, agentName, system, prompt string) (<-chan Event, error) {
	return c.run(ctx, agentName, system, prompt, "thread")
}

// run is the shared turn engine: spawn, stream, one pre-work retry.
func (c *CLIClient) run(ctx context.Context, agentName, system, prompt, placement string) (<-chan Event, error) {
	opts := agent.AgentRunOptions{
		NoSession:    true, // launch-and-forget: the thread IS the memory
		SystemPrompt: system,
	}
	// pin the agent process to the caller's cwd at send time — same "pwd where
	// run" rule as $ chips (startBash). A daemon-side run carries the CLIENT's
	// cwd in c.Cwd; a local run falls back to this process's. Empty → inherit.
	opts.Cwd = c.Cwd
	if opts.Cwd == "" {
		if pwd, err := os.Getwd(); err == nil {
			opts.Cwd = pwd
		}
	}
	if sd := firstNonEmpty(c.SkillDir, skillDir); sd != "" {
		opts.Skills = []string{sd} // the lflow skill: CLI + chips
	}
	// No in-app model picker: each provider carries a baked-in default (see
	// agent.ProviderDefault). c.Provider is the agent's hardcoded backend
	// (@Pi → pi, @Grok → grok), set by ClientFor; its default model + thinking
	// ride every turn.
	opts.Model, opts.Thinking = agent.AgentProviderDefault(c.Provider)
	sess, err := agent.AgentRun(ctx, c.Provider, prompt, opts)
	if err != nil {
		return nil, err
	}
	// upstream errors often arrive as a bare "Internal error" — prefix the
	// model so the bar says where it came from
	model := opts.Model.FlagValue()

	out := make(chan Event, 16)
	go func() {
		defer close(out)

		// One transparent retry: a provider hiccup (HTTP 500 "Internal error",
		// rate limit) that kills the turn before ANY work happened — no tool
		// ran, no reply text — re-runs once instead of surfacing straight away.
		// A turn that already did work never retries: its tools may have edited
		// the outline or the filesystem.
		for attempt := 0; ; attempt++ {
			errText, sawWork := pumpTurn(sess, out, placement, agentName)
			sess.Stop() // one turn, one process — nothing to come back to
			if errText == "" {
				return
			}
			if ctx.Err() == nil && !sawWork && attempt == 0 {
				out <- Event{Op: "tool", Tool: "retry", Text: firstNonEmpty(errText, "transient agent error")}
				s2, err := agent.AgentRun(ctx, c.Provider, prompt, opts)
				if err == nil {
					sess = s2
					continue
				}
				errText = err.Error()
			}
			if model != "" && !strings.Contains(errText, model) {
				errText = model + ": " + errText
			}
			out <- Event{Op: "error", Text: errText}
			return
		}
	}()
	return out, nil
}

// pumpTurn drains one session's events into tag events. It returns the
// terminal error text ("" on a clean turn) and whether the turn did
// observable work (a tool ran or reply text streamed) — the retry gate: a
// worked turn must never re-run.
func pumpTurn(sess agent.AgentSession, out chan<- Event, placement, agentName string) (errText string, sawWork bool) {
	var reply strings.Builder
	var interim string // last narration dropped at a tool start — fallback only
	for ev := range sess.Events() {
		switch ev.Kind {
		case agent.AgentEventToolStart, agent.AgentEventToolUpdate:
			sawWork = true
			// text emitted before a tool call is process narration ("I'll look
			// up..."), not the answer — keep it out of the reply node; only what
			// follows the LAST tool call lands. The latest cut survives as a
			// fallback for turns that end without any closing text.
			if ev.Kind == agent.AgentEventToolStart && reply.Len() > 0 {
				interim = reply.String()
				reply.Reset()
			}
			// live "what it's doing now" — the editor shows the last one as a
			// muted band under the running mention. Nothing lands in the outline.
			out <- Event{Op: "tool", Tool: ev.Tool, Text: ev.Detail}
		case agent.AgentEventText:
			if t := strings.TrimSpace(ev.Text); t != "" {
				sawWork = true
				if reply.Len() > 0 {
					reply.WriteString("\n")
				}
				reply.WriteString(t)
				// the model is reasoning/answering past its last tool — reset
				// the live band to "Thinking…" so it doesn't freeze on the tool.
				out <- Event{Op: "thinking"}
			}
		case agent.AgentEventError:
			return firstNonEmpty(ev.Text, agentName+" turn failed"), sawWork
		case agent.AgentEventTurnEnd:
			if ev.Status == "error" {
				return firstNonEmpty(strings.TrimSpace(reply.String()), agentName+" turn failed"), sawWork
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
			return "", sawWork
		}
	}
	// stream closed without a turn end — surface pi's terminal error
	if e := sess.Err(); e != nil {
		return e.Error(), sawWork
	}
	out <- Event{Op: "done"}
	return "", sawWork
}

// turnPrompt is the message the agent actually receives each turn — full XML:
// the instruction in its own tag, the rendered thread in <NodeContext>, so the
// outline never mixes with the framing around it. Type-keyed extra lines (see
// agentTypeInstructions) append inside <instructions> when the @chip's host
// node is a type that asks for them — e.g. a todo that should be completed.
func turnPrompt(thread []ThreadNode) string {
	instr := "Answer the <asked> node in NodeContext, as one short chat message."
	if extra := agentTypeInstructions(thread); extra != "" {
		instr += "\n" + extra
	}
	return "<instructions>\n" +
		instr + "\n" +
		"</instructions>\n" +
		"\n" +
		"<NodeContext>\n" + renderThread(thread) + "</NodeContext>"
}

// chipHost is the node the @chip sits on — the thread root, first non-Parent
// line. The ambient <parent> is where the thread sits in the outline, not the
// chip host; type-based agent rules key off this host only for now.
func chipHost(thread []ThreadNode) (ThreadNode, bool) {
	for _, n := range thread {
		if !n.Parent {
			return n, true
		}
	}
	return ThreadNode{}, false
}

// agentTypeInstructions returns extra per-turn instruction text drawn from the
// @chip host's node type. Ambient parent-type rules are reserved for later;
// for now only an incomplete todo on the chip host itself contributes: the
// agent must shell-run `lflow node edit <id> --state complete` after the work
// succeeds (not on PASS), as a tool call before final reply text.
func agentTypeInstructions(thread []ThreadNode) string {
	host, ok := chipHost(thread)
	if !ok {
		return ""
	}
	switch host.Type {
	case "todo":
		// already done — nothing to ask for
		if strings.Contains(host.XMLAttrs, `done="true"`) {
			return ""
		}
		id := host.UUID
		if id == "" {
			id = "<todo-id>"
		}
		// Hard, scannable rule: agents were missing the soft one-liner (forgot
		// after long work, put the command in the reply as a chip, completed on
		// PASS, or printed the reply before the edit so it got discarded).
		return "Host todo incomplete (id " + id + "). " +
			"If you answer this turn (not PASS): after the work succeeds, shell-run exactly " +
			"`lflow node edit " + id + " --state complete` as a tool call — same turn, " +
			"before your final reply text. " +
			"Skip only on PASS. Do not complete early, leave it for a later turn, " +
			"write the command into the reply, or narrate the completion."
	}
	return ""
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
