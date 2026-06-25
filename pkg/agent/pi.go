package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"strings"
)

// piBackend drives pi in RPC mode (`pi --mode rpc`): a JSON-RPC stream over the
// process's stdin/stdout, kept alive across turns so Steer pushes follow-up
// messages into the same conversation. Ported from the editor's startWorker.
type piBackend struct{}

func (piBackend) Name() Provider  { return ProviderPi }
func (piBackend) Available() bool { return onPath("pi") }

// ListModels parses `pi --list-models` ("provider model …" rows, skipping the
// header). Empty on error so the picker just shows nothing.
func (piBackend) ListModels() ([]Model, error) {
	out, err := exec.Command("pi", "--list-models").Output()
	if err != nil {
		return nil, &Error{Provider: ProviderPi, Message: "pi --list-models failed", Cause: err}
	}
	var ms []Model
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Fields(line)
		if len(f) < 2 || f[0] == "provider" {
			continue
		}
		ms = append(ms, Model{CLI: ProviderPi, Upstream: f[0], Name: f[1]})
	}
	return ms, nil
}

// piEvent is one RPC event line from pi's stdout.
type piEvent struct {
	Type          string          `json:"type"`
	Message       *piMessage      `json:"message"`
	ToolName      string          `json:"toolName"`
	Args          json.RawMessage `json:"args"`
	PartialResult json.RawMessage `json:"partialResult"`
	Error         string          `json:"error"`
}

type piMessage struct {
	Role     string `json:"role"`
	Provider string `json:"provider"`
	Model    string `json:"model"`
	Content  []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	Usage *piUsage `json:"usage"`
}

type piUsage struct {
	Input  int `json:"input"`
	Output int `json:"output"`
	Cost   *struct {
		Total float64 `json:"total"`
	} `json:"cost"`
}

func (msg *piMessage) text() string {
	if msg == nil {
		return ""
	}
	var b strings.Builder
	for _, c := range msg.Content {
		if c.Type == "text" {
			b.WriteString(c.Text)
		}
	}
	return strings.TrimSpace(b.String())
}

// Run spawns pi and returns a steerable Session whose Events() carries the
// normalized stream. The pi process stays alive after a turn ends (agent_end) so
// Steer can re-prompt the same conversation.
func (piBackend) Run(ctx context.Context, task string, opts RunOptions) (Session, error) {
	ctx, cancel := context.WithCancel(ctx)

	// Sessions are the default — never ephemeral. --session-id resumes the real
	// on-disk conversation if it already exists (creating it if missing), so a
	// worker keeps its memory across editor restarts. --session-dir pins storage.
	args := []string{"--mode", "rpc", "--approve", "--no-extensions"}
	if opts.SessionID != "" {
		args = append(args, "--session-id", opts.SessionID)
		if opts.SessionDir != "" {
			args = append(args, "--session-dir", opts.SessionDir)
		}
	}
	if opts.SystemPrompt != "" {
		args = append(args, "--append-system-prompt", opts.SystemPrompt)
	}
	if len(opts.Tools) > 0 {
		args = append(args, "--tools", strings.Join(opts.Tools, ","))
	}
	for _, ext := range opts.Extensions {
		if ext != "" {
			args = append(args, "--extension", ext)
		}
	}
	if v := opts.Model.FlagValue(); v != "" {
		args = append(args, "--model", v)
	}
	if opts.Thinking != "" && opts.Thinking != "off" {
		args = append(args, "--thinking", opts.Thinking)
	}

	c := exec.CommandContext(ctx, "pi", args...)
	if opts.Cwd != "" {
		c.Dir = opts.Cwd
	}
	stdin, _ := c.StdinPipe()
	stdout, _ := c.StdoutPipe()
	stderr, _ := c.StderrPipe()

	steerCh := make(chan string, 16)
	s := newSession(1024, func(m string) error { steerCh <- m; return nil }, cancel)

	if err := c.Start(); err != nil {
		cancel()
		return nil, &Error{Provider: ProviderPi, Message: "starting pi", Cause: err}
	}
	piSendPrompt(stdin, task)

	// forward steering messages until the conversation is stopped (ctx cancelled);
	// select on ctx.Done so this goroutine never leaks waiting on an idle worker.
	go func() {
		for {
			select {
			case msg, ok := <-steerCh:
				if !ok {
					stdin.Close()
					return
				}
				piSendPrompt(stdin, msg)
			case <-ctx.Done():
				stdin.Close()
				return
			}
		}
	}()

	// collect stderr — pi errors (rate limits, crashes, bad config) land here and
	// were previously dropped, leaving the worker stuck "idle" when it had failed.
	stderrCh := make(chan string, 1)
	go func() {
		var b strings.Builder
		sc := bufio.NewScanner(stderr)
		sc.Buffer(make([]byte, 64*1024), 1<<20)
		for sc.Scan() {
			if line := strings.TrimSpace(sc.Text()); line != "" {
				b.WriteString(line + "\n")
				s.events <- Event{Kind: EventLog, Text: "stderr: " + line, IsErr: true}
			}
		}
		stderrCh <- b.String()
	}()

	go piPump(c, stdout, stderrCh, ctx, s)
	return s, nil
}

// piPump translates pi's event stream into agent.Events until the process exits,
// then finishes the session with the terminal error/state.
func piPump(c *exec.Cmd, stdout io.Reader, stderrCh <-chan string, ctx context.Context, s *session) {
	var use Usage
	sawError := false
	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 64*1024), 1<<20)
	for sc.Scan() {
		var ev piEvent
		if json.Unmarshal(sc.Bytes(), &ev) != nil {
			continue
		}
		switch ev.Type {
		case "tool_execution_start":
			s.setState(StateWorking)
			s.events <- Event{Kind: EventToolStart, Tool: ev.ToolName, Detail: toolDetail(ev.Args), Args: ev.Args}
		case "tool_execution_update":
			detail := toolDetail(ev.Args)
			if tail := resultTail(ev.PartialResult); tail != "" {
				if detail != "" {
					detail += " · " + tail
				} else {
					detail = tail
				}
			}
			s.events <- Event{Kind: EventToolUpdate, Tool: ev.ToolName, Detail: detail}
		case "message_end":
			if ev.Message == nil {
				break
			}
			// surface the ASSISTANT's text so headless callers (the eval harness) can
			// read a no-tool answer; the worker ignores it post-finish_worker (gotFinish).
			// Only assistant messages — never echo the user turn back into the stream.
			if r := ev.Message.Role; r == "" || r == "assistant" {
				if txt := ev.Message.text(); txt != "" {
					s.events <- Event{Kind: EventAgentText, Text: txt}
				}
			}
			if ev.Message.Usage == nil {
				break
			}
			u := ev.Message.Usage
			use.In += u.Input
			use.Out += u.Output
			if u.Cost != nil {
				use.Cost += u.Cost.Total
			}
			if ev.Message.Model != "" {
				use.Model = ev.Message.Provider + "/" + ev.Message.Model
			}
			snapshot := use
			s.events <- Event{Kind: EventUsage, Usage: &snapshot}
		case "agent_end":
			st := "idle"
			s.setState(StateIdle)
			if sawError {
				st = "error"
				s.setState(StateError)
			}
			s.events <- Event{Kind: EventTurnEnd, Status: st}
		default:
			// any error-shaped event (error / agent_error / model_error …) — surface
			// it instead of silently leaving the worker idle
			if strings.Contains(ev.Type, "error") {
				sawError = true
				s.setState(StateError)
				msg := ev.Error
				if msg == "" && ev.Message != nil {
					msg = ev.Message.text()
				}
				if msg == "" {
					msg = ev.Type
				}
				s.events <- Event{Kind: EventError, Text: msg}
			}
		}
	}

	err := c.Wait()
	stderrText := <-stderrCh
	if ctx.Err() != nil { // intentional stop (cancel) — not an error
		s.finish(nil, StateStopped)
		return
	}
	if err != nil || sawError {
		last := lastLine(stderrText)
		if last == "" {
			last = "pi exited unexpectedly"
		}
		s.finish(&Error{Provider: ProviderPi, Message: last}, StateError)
		return
	}
	s.finish(nil, StateIdle)
}

// piSendPrompt writes one RPC prompt line to pi's stdin.
func piSendPrompt(stdin io.Writer, msg string) {
	if strings.TrimSpace(msg) == "" {
		return
	}
	j, _ := json.Marshal(msg)
	io.WriteString(stdin, fmt.Sprintf(`{"id":"1","type":"prompt","message":%s}`+"\n", j))
}
