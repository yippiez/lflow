package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"strings"
)

// grokBackend drives the grok CLI over ACP (`grok agent stdio`: JSON-RPC on
// stdin/stdout, line-delimited) — the same protocol pir's grok.ts speaks.
// The flow is initialize → session/new → session/prompt; tool calls and text
// arrive as session/update notifications, and the session/prompt RESPONSE
// marks the end of the turn. Every run is one fresh process and one fresh
// grok session — launch-and-forget; SessionID/resume is not wired up (grok
// mints its own session ids, so a stable caller-chosen id cannot name one).
type grokBackend struct{}

func (grokBackend) Name() AgentProvider  { return AgentProviderGrok }
func (grokBackend) Available() bool { return onPath("grok") }

// ListModels parses `grok models` ("  * grok-4.5 (default)" / "  - name"
// rows), default first. The command works unauthenticated, so the picker can
// always show grok's options.
func (grokBackend) ListModels() ([]Model, error) {
	out, err := exec.Command("grok", "models").Output()
	if err != nil {
		return nil, &Error{Provider: AgentProviderGrok, Message: "grok models failed", Cause: err}
	}
	var ms []Model
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Fields(line)
		if len(f) < 2 || (f[0] != "*" && f[0] != "-") {
			continue
		}
		m := Model{CLI: AgentProviderGrok, Name: f[1]}
		if f[0] == "*" {
			ms = append([]Model{m}, ms...) // the default model leads the list
		} else {
			ms = append(ms, m)
		}
	}
	return ms, nil
}

// acpRequest / acpResponse / acpNotify are the JSON-RPC frames on the wire.
type acpFrame struct {
	ID     *int64          `json:"id"`
	Method string          `json:"method"`
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Message string `json:"message"`
	} `json:"error"`
	Params struct {
		Update struct {
			SessionUpdate string          `json:"sessionUpdate"`
			Tool          string          `json:"tool"`
			Title         string          `json:"title"`
			RawInput      json.RawMessage `json:"rawInput"`
			Arguments     json.RawMessage `json:"arguments"`
			Content       struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"update"`
	} `json:"params"`
}

// Run spawns `grok agent stdio` and pumps its ACP stream into a Session.
// SystemPrompt is folded into the prompt text (the stdio agent takes no
// system-prompt flag); Skills/Extensions are pi-only and ignored.
func (grokBackend) Run(ctx context.Context, task string, opts AgentRunOptions) (AgentSession, error) {
	ctx, cancel := context.WithCancel(ctx)

	args := []string{"agent", "--always-approve"}
	if v := opts.Model.FlagValue(); v != "" {
		args = append(args, "-m", v)
	}
	if opts.Thinking != "" && opts.Thinking != "off" && opts.Thinking != "default" {
		args = append(args, "--reasoning-effort", opts.Thinking)
	}
	args = append(args, "stdio")

	c := exec.CommandContext(ctx, "grok", args...)
	if opts.Cwd != "" {
		c.Dir = opts.Cwd
	}
	stdin, _ := c.StdinPipe()
	stdout, _ := c.StdoutPipe()

	s := newSession(1024, cancel)
	if err := c.Start(); err != nil {
		cancel()
		return nil, &Error{Provider: AgentProviderGrok, Message: "starting grok", Cause: err}
	}

	prompt := task
	if opts.SystemPrompt != "" {
		prompt = opts.SystemPrompt + "\n\n---\n\n" + task
	}

	go grokPump(c, stdin, stdout, ctx, prompt, s)
	return s, nil
}

// grokPump runs the ACP handshake, sends the prompt, and translates the
// stream into Events until the turn's response (or the process's exit) ends it.
func grokPump(c *exec.Cmd, stdin io.WriteCloser, stdout io.Reader, ctx context.Context, prompt string, s *session) {
	defer func() {
		stdin.Close()
		c.Wait()
	}()

	send := func(id int64, method string, params any) {
		frame := map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params}
		b, _ := json.Marshal(frame)
		stdin.Write(append(b, '\n'))
	}
	const idInit, idNew, idPrompt = 1, 2, 3
	send(idInit, "initialize", map[string]any{
		"protocolVersion": "1",
		"clientCapabilities": map[string]any{
			"fs": map[string]any{"readTextFile": false, "writeTextFile": false}, "terminal": false,
		},
	})

	fail := func(msg string) {
		s.events <- AgentEvent{Kind: AgentEventError, Text: msg}
		s.events <- AgentEvent{Kind: AgentEventTurnEnd, Status: "error"}
		s.finish(&Error{Provider: AgentProviderGrok, Message: msg}, AgentStateError)
	}

	var reply strings.Builder
	// flush emits the text accumulated since the last flush as one event.
	// Called before each tool call and at turn end, so text interleaves with
	// tools in stream order rather than arriving as one end-of-turn blob —
	// the reply extractor in pkg/tui/tag relies on that ordering to drop
	// pre-tool narration ("I'll look at...") from the reply node.
	flush := func() {
		if txt := strings.TrimSpace(reply.String()); txt != "" {
			s.events <- AgentEvent{Kind: AgentEventText, Text: txt}
		}
		reply.Reset()
	}
	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 64*1024), 1<<20)
	for sc.Scan() {
		var fr acpFrame
		if json.Unmarshal(sc.Bytes(), &fr) != nil {
			continue
		}
		switch {
		case fr.ID != nil && fr.Error != nil:
			fail(fr.Error.Message)
			return
		case fr.ID != nil && *fr.ID == idInit:
			send(idNew, "session/new", map[string]any{"cwd": cwdOf(c), "mcpServers": []any{}})
		case fr.ID != nil && *fr.ID == idNew:
			var res struct {
				SessionID string `json:"sessionId"`
			}
			if json.Unmarshal(fr.Result, &res) != nil || res.SessionID == "" {
				fail("grok session/new returned no session id")
				return
			}
			s.setState(AgentStateWorking)
			send(idPrompt, "session/prompt", map[string]any{
				"sessionId": res.SessionID,
				"prompt":    []map[string]any{{"type": "text", "text": prompt}},
			})
		case fr.ID != nil && *fr.ID == idPrompt:
			// the prompt response IS the turn end
			flush()
			s.setState(AgentStateIdle)
			s.events <- AgentEvent{Kind: AgentEventTurnEnd, Status: "idle"}
			s.finish(nil, AgentStateIdle)
			return
		case fr.Method == "session/update":
			u := fr.Params.Update
			switch u.SessionUpdate {
			case "tool_call":
				flush() // text so far precedes this tool — emit it in order
				name := u.Tool
				if name == "" {
					name = u.Title
				}
				args := u.Arguments
				if len(args) == 0 {
					args = u.RawInput
				}
				s.events <- AgentEvent{Kind: AgentEventToolStart, Tool: name, Detail: toolDetail(args), Args: args}
			case "tool_call_update":
				s.events <- AgentEvent{Kind: AgentEventToolUpdate, Tool: u.Tool, Detail: clip(lastLine(u.Content.Text), 48)}
			case "agent_message_chunk":
				reply.WriteString(u.Content.Text)
			}
		}
	}

	if ctx.Err() != nil { // intentional stop — not an error
		s.finish(nil, AgentStateStopped)
		return
	}
	fail("grok exited before the turn ended (is `grok login` done?)")
}

// cwdOf resolves the working directory the ACP session should open in — grok
// wants an absolute path, so an unset Dir falls back to the process cwd.
func cwdOf(c *exec.Cmd) string {
	if c.Dir != "" {
		return c.Dir
	}
	if wd, err := os.Getwd(); err == nil {
		return wd
	}
	return "/"
}
