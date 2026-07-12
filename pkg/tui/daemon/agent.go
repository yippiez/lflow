package daemon

import (
	"context"
	"encoding/json"
	"net"
	"os/exec"

	"github.com/lflow/lflow/pkg/tui/tag"
	"github.com/lflow/lflow/pkg/tui/wire"
	"github.com/lflow/lflow/pkg/utils/dirs"
	"github.com/pkg/errors"
)

// Agent turns run ON the daemon — the editor is only a client. A turn owns
// its connection: the client sends one OpAgent request, the daemon streams
// AgentEv frames back until done, and the client closing the conn cancels the
// CLI. Dependency truth lives here too (OpDeps): the process that would exec
// pi/grok/ffmpeg is the one that says whether it can.

// probeBins reports which CLI binaries this process can exec.
func probeBins(bins []string) map[string]bool {
	out := make(map[string]bool, len(bins))
	for _, b := range bins {
		if b == "" {
			continue
		}
		_, err := exec.LookPath(b)
		out[b] = err == nil
	}
	return out
}

// prepAgent resolves an OpAgent request into a runnable client + thread. All
// failures land in the request's ack (Unknown agent, Missing dependency, bad
// payload) so the conn never enters streaming mode on a dead turn.
func prepAgent(req wire.Req) (tag.Client, []tag.ThreadNode, error) {
	var ag *tag.Agent
	for _, a := range tag.LoadAgents(dirs.ConfigHome) {
		if a.Name == req.Agent {
			ag = &a
			break
		}
	}
	if ag == nil {
		return nil, nil, errors.Errorf("Unknown agent @%s", req.Agent)
	}
	cl, err := tag.ClientFor(*ag)
	if err != nil {
		return nil, nil, err // e.g. "Missing dependency: pi"
	}
	// a CLI-backed turn runs with the CLIENT's environment choices
	if c, ok := cl.(*tag.CLIClient); ok {
		c.Cwd, c.SkillDir = req.Cwd, req.SkillDir
	}
	var thread []tag.ThreadNode
	if err := json.Unmarshal(req.Thread, &thread); err != nil {
		return nil, nil, errors.Wrap(err, "decoding thread")
	}
	return cl, thread, nil
}

// agentTurn streams one turn on this conn. It returns when the turn ends or
// the client hangs up (which cancels the CLI through the ctx).
func (sv *server) agentTurn(conn net.Conn, dec *json.Decoder, enc *json.Encoder, sess *session, req wire.Req, cl tag.Client, thread []tag.ThreadNode) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// the only reads left are hangup detection — a closed conn kills the CLI
	dead := make(chan struct{})
	go func() {
		var x json.RawMessage
		for {
			if err := dec.Decode(&x); err != nil {
				close(dead)
				return
			}
		}
	}()
	go func() {
		select {
		case <-dead:
			cancel()
		case <-ctx.Done():
		}
	}()

	sv.logf("→ agent @%s turn · %q", req.Agent, sess.name)
	ch, err := cl.Send(ctx, req.Agent, thread)
	if err != nil {
		_ = enc.Encode(wire.Msg{Agent: &wire.AgentEv{Op: "error", Text: err.Error()}})
		_ = enc.Encode(wire.Msg{Agent: &wire.AgentEv{Done: true}})
		return
	}
	for ev := range ch {
		frame := wire.AgentEv{Op: ev.Op, Text: ev.Text, Tool: ev.Tool, Placement: ev.Placement}
		if err := enc.Encode(wire.Msg{Agent: &frame}); err != nil {
			cancel() // client gone mid-turn: stop the CLI, drain the stream
			for range ch {
			}
			return
		}
	}
	_ = enc.Encode(wire.Msg{Agent: &wire.AgentEv{Done: true}})
	sv.logf("→ agent @%s done · %q", req.Agent, sess.name)
}
