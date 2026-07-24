// Package compute runs the one-shot code-generation turn used by NLPCompute.
package compute

import (
	"context"
	"os"
	"strings"

	"github.com/lflow/lflow/pkg/agent"
)

// Event is one streamed generation event.
type Event struct {
	Op   string
	Text string
	Tool string
}

var skillDir string

// SetSkillDir records the materialized lflow skill path.
func SetSkillDir(dir string) { skillDir = dir }

// SkillDir returns the materialized lflow skill path.
func SkillDir() string { return skillDir }

// Run starts one fresh Pi generation turn. The supplied context cancels the
// underlying CLI process; no provider session is persisted.
func Run(ctx context.Context, system, prompt, cwd, skills string) (<-chan Event, error) {
	provider := agent.AgentProviderPi
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	opts := agent.AgentRunOptions{
		NoSession:    true,
		SystemPrompt: system,
		Cwd:          cwd,
	}
	if skills == "" {
		skills = skillDir
	}
	if skills != "" {
		opts.Skills = []string{skills}
	}
	opts.Model, opts.Thinking = agent.AgentProviderDefault(provider)

	sess, err := agent.AgentRun(ctx, provider, prompt, opts)
	if err != nil {
		return nil, err
	}
	model := opts.Model.FlagValue()
	out := make(chan Event, 16)
	go func() {
		defer close(out)
		for attempt := 0; ; attempt++ {
			errText, sawWork := pump(sess, out)
			sess.Stop()
			if errText == "" {
				return
			}
			if ctx.Err() == nil && !sawWork && attempt == 0 {
				out <- Event{Op: "tool", Tool: "retry", Text: firstNonEmpty(errText, "transient compute error")}
				next, runErr := agent.AgentRun(ctx, provider, prompt, opts)
				if runErr == nil {
					sess = next
					continue
				}
				errText = runErr.Error()
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

func pump(sess agent.AgentSession, out chan<- Event) (errText string, sawWork bool) {
	var reply strings.Builder
	var interim string
	for ev := range sess.Events() {
		switch ev.Kind {
		case agent.AgentEventToolStart, agent.AgentEventToolUpdate:
			sawWork = true
			if ev.Kind == agent.AgentEventToolStart && reply.Len() > 0 {
				interim = reply.String()
				reply.Reset()
			}
			out <- Event{Op: "tool", Tool: ev.Tool, Text: ev.Detail}
		case agent.AgentEventText:
			if text := strings.TrimSpace(ev.Text); text != "" {
				sawWork = true
				if reply.Len() > 0 {
					reply.WriteByte('\n')
				}
				reply.WriteString(text)
				out <- Event{Op: "thinking"}
			}
		case agent.AgentEventError:
			return firstNonEmpty(ev.Text, "compute turn failed"), sawWork
		case agent.AgentEventTurnEnd:
			if ev.Status == "error" {
				return firstNonEmpty(strings.TrimSpace(reply.String()), "compute turn failed"), sawWork
			}
			text := strings.TrimSpace(reply.String())
			if text == "" {
				text = strings.TrimSpace(interim)
			}
			if text != "" {
				out <- Event{Op: "message", Text: text}
			}
			out <- Event{Op: "done"}
			return "", sawWork
		}
	}
	if err := sess.Err(); err != nil {
		return err.Error(), sawWork
	}
	out <- Event{Op: "done"}
	return "", sawWork
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
