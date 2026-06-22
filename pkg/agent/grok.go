package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"sync"
)

// grokBackend drives `grok agent stdio` over the Agent Client Protocol (JSON-RPC
// on stdin/stdout, no HTTP), ported from pir/src/grok.ts. A turn is a
// session/prompt request that resolves when the turn ends; Steer issues another
// prompt into the same session.
//
// NOTE: grok run requires authentication (`grok login` / XAI_API_KEY) and is not
// exercised in this environment, so the run path is a faithful port but unverified.
// Model listing works without auth.
type grokBackend struct{}

func (grokBackend) Name() Provider  { return ProviderGrok }
func (grokBackend) Available() bool { return onPath("grok") }

// ListModels parses `grok models`. Output is a header block followed by lines like
// "  - grok-build" and "  * grok-composer-2.5-fast (default)"; grok models have no
// upstream provider, so Upstream stays "".
func (grokBackend) ListModels() ([]Model, error) {
	out, err := exec.Command("grok", "models").CombinedOutput()
	if err != nil {
		// grok models prints an auth notice but still lists models and may exit
		// non-zero; only treat truly empty output as a failure.
		if len(out) == 0 {
			return nil, &Error{Provider: ProviderGrok, Message: "grok models failed", Cause: err}
		}
	}
	var ms []Model
	inList := false
	for _, line := range strings.Split(string(out), "\n") {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "Available models") {
			inList = true
			continue
		}
		if !inList {
			continue
		}
		name := strings.TrimSpace(strings.TrimLeft(t, "-*"))
		name = strings.TrimSpace(strings.TrimSuffix(name, "(default)"))
		if name == "" {
			continue
		}
		ms = append(ms, Model{CLI: ProviderGrok, Name: name})
	}
	return ms, nil
}

func (grokBackend) Run(ctx context.Context, task string, opts RunOptions) (Session, error) {
	ctx, cancel := context.WithCancel(ctx)
	cl := newACP(ctx, opts.Cwd)
	if err := cl.start(opts.Model.FlagValue()); err != nil {
		cancel()
		return nil, err
	}

	s := newSession(1024, nil, func() { cancel(); cl.kill() })
	// route ACP session/update notifications onto the event stream.
	cl.onNotify = func(p acpUpdate) {
		switch p.Update.SessionUpdate {
		case "tool_call":
			variant := p.Update.Tool
			if variant == "" {
				variant = p.Update.Title
			}
			args := p.Update.Arguments
			if len(args) == 0 {
				args = p.Update.RawInput
			}
			s.events <- Event{Kind: EventToolStart, Tool: variant, Detail: toolDetail(args), Args: args}
		case "agent_message_chunk":
			// the answer — this is the harvested deliverable.
			if txt := p.Update.Content.Text; txt != "" {
				s.events <- Event{Kind: EventAgentText, Text: txt}
			}
		case "agent_thought_chunk":
			// reasoning — intentionally dropped so it never pollutes the
			// deliverable (grok streams dozens of thought chunks per turn).
		}
	}
	// total accumulates usage across turns (the editor serializes turns, so no
	// lock is needed) so the worker shows a running session total, matching pi.
	var total Usage
	// steering and the initial prompt both run a turn (prompt → usage → turn end);
	// fire-and-forget so results arrive on the event stream, matching pi/opencode.
	s.steer = func(msg string) error { go grokRunTurn(s, cl, msg, &total); return nil }

	// initial turn, then keep the session alive until the context is cancelled.
	go func() {
		grokRunTurn(s, cl, task, &total)
		<-ctx.Done()
		s.finish(nil, StateStopped)
	}()
	return s, nil
}

// grokRunTurn sends one prompt and translates its outcome — usage from the
// result's _meta (accumulated into total), then a turn-end — onto the event stream.
func grokRunTurn(s *session, cl *acpClient, msg string, total *Usage) {
	s.setState(StateWorking)
	res, err := cl.prompt(msg)
	if err != nil {
		if cl.ctx.Err() == nil {
			s.setState(StateError)
			s.events <- Event{Kind: EventError, Text: err.Error()}
		}
		return
	}
	var r struct {
		Meta struct {
			Model      string `json:"modelId"`
			In         int    `json:"inputTokens"`
			Out        int    `json:"outputTokens"`
			CachedRead int    `json:"cachedReadTokens"`
		} `json:"_meta"`
	}
	if json.Unmarshal(res, &r) == nil && (r.Meta.In > 0 || r.Meta.Out > 0) {
		// grok's ACP reports tokens but no cost — estimate from the price table.
		cost, known := EstimateCost(r.Meta.Model, r.Meta.In, r.Meta.Out, r.Meta.CachedRead)
		total.Model = r.Meta.Model
		total.In += r.Meta.In
		total.Out += r.Meta.Out
		total.Cost += cost
		total.Estimated = total.Estimated || known // grok cost is estimated, not CLI-reported
		u := *total
		s.events <- Event{Kind: EventUsage, Usage: &u}
	}
	s.setState(StateIdle)
	s.events <- Event{Kind: EventTurnEnd, Status: "idle"}
}

// --- minimal ACP JSON-RPC client over `grok agent stdio` --------------------

type acpUpdate struct {
	Update struct {
		SessionUpdate string          `json:"sessionUpdate"`
		Tool          string          `json:"tool"`
		Title         string          `json:"title"`
		Arguments     json.RawMessage `json:"arguments"`
		RawInput      json.RawMessage `json:"rawInput"`
		Content       struct {
			Text string `json:"text"`
		} `json:"content"`
	} `json:"update"`
}

type acpClient struct {
	ctx      context.Context
	cwd      string
	cmd      *exec.Cmd
	stdin    interface{ Write([]byte) (int, error) }
	onNotify func(acpUpdate)

	mu      sync.Mutex
	nextID  int
	pending map[int]chan acpResult
	sid     string
}

type acpResult struct {
	result json.RawMessage
	err    error
}

func newACP(ctx context.Context, cwd string) *acpClient {
	// grok's session/new rejects a non-absolute cwd ("Path is not absolute"), so
	// default an empty cwd to the process working directory.
	if cwd == "" {
		if wd, err := os.Getwd(); err == nil {
			cwd = wd
		}
	}
	return &acpClient{ctx: ctx, cwd: cwd, pending: map[int]chan acpResult{}, nextID: 1}
}

func (c *acpClient) start(model string) error {
	// -m is an option of `grok agent`, not of the `stdio` subcommand, so it must
	// sit before `stdio` (`grok agent -m MODEL stdio`); `grok agent stdio -m …`
	// errors with "unexpected argument '-m'".
	args := []string{"agent", "--always-approve"}
	if model != "" {
		args = append(args, "-m", model)
	}
	args = append(args, "stdio")
	cmd := exec.CommandContext(c.ctx, "grok", args...)
	if c.cwd != "" {
		cmd.Dir = c.cwd
	}
	stdin, _ := cmd.StdinPipe()
	stdout, _ := cmd.StdoutPipe()
	c.cmd, c.stdin = cmd, stdin
	if err := cmd.Start(); err != nil {
		return &Error{Provider: ProviderGrok, Message: "starting grok agent stdio", Cause: err}
	}
	go c.read(stdout)

	if _, err := c.request("initialize", map[string]any{
		"protocolVersion":    "1",
		"clientCapabilities": map[string]any{"fs": map[string]any{"readTextFile": false, "writeTextFile": false}, "terminal": false},
	}); err != nil {
		return &Error{Provider: ProviderGrok, Message: "grok initialize failed", Cause: err}
	}
	res, err := c.request("session/new", map[string]any{"cwd": c.cwd, "mcpServers": []any{}})
	if err != nil {
		return &Error{Provider: ProviderGrok, Message: "grok session/new failed", Cause: err}
	}
	var nw struct {
		SessionID json.RawMessage `json:"sessionId"`
	}
	_ = json.Unmarshal(res, &nw)
	c.sid = strings.Trim(string(nw.SessionID), `"`)
	return nil
}

// read demultiplexes the line-delimited JSON-RPC stream: results resolve pending
// requests, session/update notifications go to onNotify.
func (c *acpClient) read(stdout interface{ Read([]byte) (int, error) }) {
	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 256*1024), 4<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var msg struct {
			ID     *int            `json:"id"`
			Result json.RawMessage `json:"result"`
			Error  json.RawMessage `json:"error"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if json.Unmarshal([]byte(line), &msg) != nil {
			continue
		}
		switch {
		case msg.ID != nil && (len(msg.Result) > 0 || len(msg.Error) > 0):
			c.mu.Lock()
			ch := c.pending[*msg.ID]
			delete(c.pending, *msg.ID)
			c.mu.Unlock()
			if ch != nil {
				if len(msg.Error) > 0 {
					ch <- acpResult{err: &Error{Provider: ProviderGrok, Message: "rpc: " + string(msg.Error)}}
				} else {
					ch <- acpResult{result: msg.Result}
				}
			}
		case msg.Method == "session/update":
			if c.onNotify != nil {
				var up acpUpdate
				if json.Unmarshal(msg.Params, &up) == nil {
					c.onNotify(up)
				}
			}
		}
	}
	// stream closed — fail any in-flight requests so callers unblock.
	c.mu.Lock()
	for id, ch := range c.pending {
		delete(c.pending, id)
		ch <- acpResult{err: &Error{Provider: ProviderGrok, Message: "grok stream closed"}}
	}
	c.mu.Unlock()
}

func (c *acpClient) request(method string, params any) (json.RawMessage, error) {
	c.mu.Lock()
	id := c.nextID
	c.nextID++
	ch := make(chan acpResult, 1)
	c.pending[id] = ch
	c.mu.Unlock()

	body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
	if _, err := c.stdin.Write(append(body, '\n')); err != nil {
		return nil, &Error{Provider: ProviderGrok, Message: "writing rpc", Cause: err}
	}
	select {
	case <-c.ctx.Done():
		return nil, c.ctx.Err()
	case r := <-ch:
		return r.result, r.err
	}
}

// prompt runs one turn; it resolves with the result when grok finishes the turn.
func (c *acpClient) prompt(text string) (json.RawMessage, error) {
	return c.request("session/prompt", map[string]any{
		"sessionId": c.sid,
		"prompt":    []any{map[string]any{"type": "text", "text": text}},
	})
}

func (c *acpClient) kill() {
	if c.cmd != nil && c.cmd.Process != nil {
		_ = c.cmd.Process.Kill()
	}
}
