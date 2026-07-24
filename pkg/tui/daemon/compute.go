package daemon

import (
	"context"
	"encoding/json"
	"net"
	"os/exec"

	"github.com/lflow/lflow/pkg/tui/compute"
	"github.com/lflow/lflow/pkg/tui/wire"
)

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

// computeTurn streams one NLPCompute generation on this dedicated connection.
// Closing the client connection cancels the underlying Pi process.
func (sv *server) computeTurn(conn net.Conn, dec *json.Decoder, enc *json.Encoder, sess *session, req wire.Req) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

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

	sv.logf("→ compute turn · %q", sess.name)
	ch, err := compute.Run(ctx, req.System, req.Prompt, req.Cwd, req.SkillDir)
	if err != nil {
		_ = enc.Encode(wire.Msg{Compute: &wire.ComputeEv{Op: "error", Text: err.Error()}})
		_ = enc.Encode(wire.Msg{Compute: &wire.ComputeEv{Done: true}})
		return
	}
	for ev := range ch {
		frame := wire.ComputeEv{Op: ev.Op, Text: ev.Text, Tool: ev.Tool}
		if err := enc.Encode(wire.Msg{Compute: &frame}); err != nil {
			cancel()
			for range ch {
			}
			return
		}
	}
	_ = enc.Encode(wire.Msg{Compute: &wire.ComputeEv{Done: true}})
	sv.logf("→ compute done · %q", sess.name)
}
