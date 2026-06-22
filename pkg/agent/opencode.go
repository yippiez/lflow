package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"os/exec"
	"strings"
)

// opencodeBackend drives the opencode CLI. Model listing is `opencode models`;
// a turn is `opencode run --format json` (one process per turn — opencode run is
// not a long-lived RPC like pi). Steer continues the session with `--continue`,
// so the conversation persists across turns even though each turn is its own
// process. The TS original (pir/src/opencode.ts) used the in-process SDK, which
// has no Go equivalent, hence the CLI adapter here.
//
// NOTE: the run path is not yet exercised against a live opencode (the verified
// path in lflow is pi). The JSON decode below is intentionally lenient.
type opencodeBackend struct{}

func (opencodeBackend) Name() Provider  { return ProviderOpencode }
func (opencodeBackend) Available() bool { return onPath("opencode") }

// ListModels parses `opencode models` (one "provider/model" per line).
func (opencodeBackend) ListModels() ([]Model, error) {
	out, err := exec.Command("opencode", "models").Output()
	if err != nil {
		return nil, &Error{Provider: ProviderOpencode, Message: "opencode models failed", Cause: err}
	}
	var ms []Model
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		up, name := "", line
		if i := strings.IndexByte(line, '/'); i >= 0 {
			up, name = line[:i], line[i+1:]
		}
		ms = append(ms, Model{CLI: ProviderOpencode, Upstream: up, Name: name})
	}
	return ms, nil
}

func (opencodeBackend) Run(ctx context.Context, task string, opts RunOptions) (Session, error) {
	ctx, cancel := context.WithCancel(ctx)
	turns := make(chan string, 16)
	s := newSession(1024, func(m string) error { turns <- m; return nil }, cancel)
	go opencodeLoop(ctx, task, turns, opts, s)
	return s, nil
}

// opencodeLoop runs the first turn, then one continue-turn per steer message,
// feeding all of them into the single session, until the context is cancelled.
// total accumulates usage across every step and turn so the worker line shows a
// running session total (matching pi), not just the last step.
func opencodeLoop(ctx context.Context, task string, turns <-chan string, opts RunOptions, s *session) {
	var total Usage
	if err := opencodeTurn(ctx, task, false, opts, s, &total); err != nil && ctx.Err() == nil {
		s.events <- Event{Kind: EventError, Text: err.Error()}
	}
	for {
		select {
		case <-ctx.Done():
			s.finish(nil, StateStopped)
			return
		case msg, ok := <-turns:
			if !ok {
				s.finish(nil, StateIdle)
				return
			}
			if err := opencodeTurn(ctx, msg, true, opts, s, &total); err != nil && ctx.Err() == nil {
				s.events <- Event{Kind: EventError, Text: err.Error()}
			}
		}
	}
}

// opencodeTurn runs one `opencode run` invocation and streams its JSON events.
func opencodeTurn(ctx context.Context, msg string, cont bool, opts RunOptions, s *session, total *Usage) error {
	args := []string{"run", "--format", "json"}
	if cont {
		args = append(args, "--continue")
	}
	if v := opts.Model.FlagValue(); v != "" {
		args = append(args, "-m", v)
	}
	args = append(args, msg)

	c := exec.CommandContext(ctx, "opencode", args...)
	if opts.Cwd != "" {
		c.Dir = opts.Cwd
	}
	stdout, _ := c.StdoutPipe()
	stderr, _ := c.StderrPipe()
	if err := c.Start(); err != nil {
		return &Error{Provider: ProviderOpencode, Message: "starting opencode", Cause: err}
	}

	go func() {
		sc := bufio.NewScanner(stderr)
		sc.Buffer(make([]byte, 64*1024), 1<<20)
		for sc.Scan() {
			if line := strings.TrimSpace(sc.Text()); line != "" {
				s.events <- Event{Kind: EventLog, Text: "stderr: " + line, IsErr: true}
			}
		}
	}()

	s.setState(StateWorking)
	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 256*1024), 4<<20)
	for sc.Scan() {
		opencodeDecode(sc.Bytes(), s, total)
	}
	err := c.Wait()
	if ctx.Err() != nil {
		return nil
	}
	if err != nil {
		s.setState(StateError)
		return &Error{Provider: ProviderOpencode, Message: "opencode run failed", Cause: err}
	}
	s.setState(StateIdle)
	s.events <- Event{Kind: EventTurnEnd, Status: "idle"}
	return nil
}

// opencodeDecode maps one line of opencode's `--format json` output onto an Event.
// opencode nests everything under "part": a text part carries part.text, a
// step-finish part carries part.tokens + part.cost, and a tool part carries the
// tool name + input. opencode has no finish_worker, so the editor treats the
// accumulated assistant text as the deliverable.
func opencodeDecode(line []byte, s *session, total *Usage) {
	var ev struct {
		Type string `json:"type"`
		Part struct {
			Type   string          `json:"type"`
			Text   string          `json:"text"`
			Tool   string          `json:"tool"`
			Input  json.RawMessage `json:"input"`
			Tokens *struct {
				Input  int `json:"input"`
				Output int `json:"output"`
			} `json:"tokens"`
			Cost float64 `json:"cost"`
		} `json:"part"`
	}
	if json.Unmarshal(line, &ev) != nil {
		return
	}
	p := ev.Part
	switch {
	case p.Type == "text" && strings.TrimSpace(p.Text) != "":
		s.events <- Event{Kind: EventAgentText, Text: p.Text}
	case strings.HasPrefix(p.Type, "tool") && p.Tool != "":
		s.events <- Event{Kind: EventToolStart, Tool: p.Tool, Detail: toolDetail(p.Input), Args: p.Input}
	case strings.HasPrefix(p.Type, "step-finish") && p.Tokens != nil:
		// opencode reports cost + tokens per STEP; accumulate so the worker shows
		// the running session total, not just the final step.
		total.In += p.Tokens.Input
		total.Out += p.Tokens.Output
		total.Cost += p.Cost
		u := *total
		s.events <- Event{Kind: EventUsage, Usage: &u}
	}
}
