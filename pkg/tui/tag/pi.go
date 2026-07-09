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

// piSystemPrompt frames pi as the note-app assistant. Kept deliberately small;
// the richer "base" skill (how to speak in chips) is future work.
const piSystemPrompt = "You are Pi, an assistant living inside a terminal " +
	"outline note-taking app. A user mentioned you with @Pi in one of their " +
	"outline nodes. You are given that node and its subtree as an indented " +
	"outline; the line marked [ASKED] is the one to address. Reply with a single, " +
	"concise, plain-text answer — no markdown headings or code fences, at most a " +
	"few sentences. Do not repeat the question back."

// Send runs one pi turn over the thread and streams the reply as tag events.
func (c *PiClient) Send(ctx context.Context, agentName, sessionID string, thread []ThreadNode) (<-chan Event, error) {
	sid := sessionID
	if sid == "" && len(thread) > 0 {
		sid = thread[0].UUID // stable, resumable id = the thread root's uuid
	}
	sess, err := agent.Run(ctx, agent.ProviderPi, renderThread(thread), agent.RunOptions{
		SessionID:    sid,
		SystemPrompt: piSystemPrompt,
		Cwd:          c.Cwd,
	})
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
					// the reply nests UNDER the asked node — the mention owns its
					// conversation as children, it never spills into the parent level
					out <- Event{Op: "message", Placement: "thread", Text: txt}
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
