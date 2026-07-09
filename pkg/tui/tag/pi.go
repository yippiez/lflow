package tag

import (
	"context"
	"strings"

	"github.com/lflow/lflow/pkg/agent"
)

// PiClient bridges @mentions to the local `pi` coding agent (pkg/agent's RPC
// backend). Each Send runs exactly one turn: pi resumes the on-disk conversation
// by session id, so the transport stays connectionless between turns like
// WSClient — the editor closes and a later mention picks the thread back up.
type PiClient struct {
	Cwd string // working directory for pi ("" = inherit the editor's)
}

// nodesDir is where the genui node-type files live (<config>/lflow/nodes) —
// the editor sets it at start so the system prompt can tell pi to create and
// edit node types directly.
var nodesDir string

// SetNodesDir records the genui nodes directory for the system prompt.
func SetNodesDir(dir string) { nodesDir = dir }

// Model and thinking preferences from /settings. "" or "default" leaves the
// choice to pi's own config (~/.pi settings); anything else is passed through
// on every turn.
var (
	modelPref    string
	thinkingPref string
)

// SetModelPref records the /settings agent.model choice ("upstream/model").
func SetModelPref(v string) { modelPref = v }

// SetThinkingPref records the /settings agent.thinking choice.
func SetThinkingPref(v string) { thinkingPref = v }

// piSystemPrompt frames pi as the note-app assistant: plain concise replies,
// how to speak in chips (the inline structured tokens lflow renders), and
// where the node-type files live so pi can write them itself.
func piSystemPrompt() string {
	p := "You are Pi, an assistant living inside a terminal outline note-taking " +
		"app. A user mentioned you with @Pi in one of their outline nodes. You are " +
		"given that node and its subtree as an indented outline; the line marked " +
		"[ASKED] is the one to address. Reply with a single, concise answer — plain " +
		"text, no markdown headings or code fences, at most a few sentences. Do not " +
		"repeat the question back.\n" +
		"\n" +
		"Chips: you may embed these inline tokens anywhere in your reply; the app " +
		"renders each as a structured chip.\n" +
		"  {{cmd:ls -la}} — a runnable shell command; the user runs it in place with " +
		"alt+r. When asked for a command, answer with a cmd chip, not prose.\n" +
		"  {{path:/etc/hosts}} — a file or directory path.\n" +
		"  {{link:label|https://example.com}} — a link; the label is optional " +
		"({{link:https://example.com}}).\n" +
		"  #tags and YYYY-MM-DD dates become chips automatically — write them plainly.\n" +
		"Never wrap a chip token in quotes or backticks."
	if nodesDir != "" {
		p += "\n\nCustom node types are JS files in " + nodesDir + ": <type>.js " +
			"defines the node type <type>; renaming it <type>.js.disabled turns it " +
			"off. When asked for a new node type, write the file yourself, mirroring " +
			"the lflow.registerType calls in the existing files there — the app " +
			"reloads the directory when your turn ends."
	}
	return p
}

// Send runs one pi turn over the thread and streams the reply as tag events.
func (c *PiClient) Send(ctx context.Context, agentName, sessionID string, thread []ThreadNode) (<-chan Event, error) {
	sid := sessionID
	if sid == "" && len(thread) > 0 {
		sid = thread[0].UUID // stable, resumable id = the thread root's uuid
	}
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
		SessionID:    sid,
		SystemPrompt: piSystemPrompt(),
		Cwd:          c.Cwd,
	}
	if modelPref != "" && modelPref != "default" {
		opts.Model = agent.ParseModel(modelPref)
	}
	if thinkingPref != "" && thinkingPref != "default" {
		opts.Thinking = thinkingPref
	}
	sess, err := agent.Run(ctx, agent.ProviderPi, renderThread(thread), opts)
	if err != nil {
		return nil, err
	}

	out := make(chan Event, 16)
	go func() {
		defer close(out)
		defer sess.Stop() // one turn; release pi (the session id resumes it later)

		if sessionID == "" && sid != "" {
			out <- Event{Op: "session", ID: sid}
		}
		var reply strings.Builder
		for ev := range sess.Events() {
			switch ev.Kind {
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
				if txt := strings.TrimSpace(reply.String()); txt != "" {
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

// renderThread flattens the thread into an indented outline prompt for pi.
func renderThread(thread []ThreadNode) string {
	var b strings.Builder
	for _, n := range thread {
		b.WriteString(strings.Repeat("  ", n.Depth))
		b.WriteString("- ")
		if n.Asked {
			b.WriteString("[ASKED] ")
		}
		if n.Role == "agent" {
			b.WriteString("(Pi earlier) ")
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

// piAvailable reports whether the local pi CLI can serve a real agent.
func piAvailable() bool {
	b, ok := agent.Get(agent.ProviderPi)
	return ok && b.Available()
}
