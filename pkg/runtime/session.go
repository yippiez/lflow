// Package runtime embeds a persistent Colab compute session into lflow.
//
// Unlike the compute CLI — which assigns a runtime, opens a fresh Jupyter
// kernel, runs code, then tears the kernel down on every invocation (so Python
// state is lost between runs) — this package keeps ONE kernel open across many
// Execute calls. Variables defined in one Execute survive into the next, for as
// long as the Session is alive:
//
//	sess, _ := runtime.SessionStart(ctx, runtime.Opts{Store: store})
//	sess.Execute(ctx, "x = 41")
//	out, _ := sess.Execute(ctx, "print(x + 1)") // -> "42"
//	sess.Stop(ctx)
package runtime

import (
	"context"
	"fmt"
	"sync"
)

// Opts configures a session.
type Opts struct {
	// Store loads and persists the Google OAuth token. Required.
	Store TokenStore

	// GPU names the accelerator (e.g. "t4", "a100"). Ignored when CPU is true.
	GPU string

	// CPU requests a CPU-only runtime. When neither GPU nor CPU is set, the
	// session defaults to CPU.
	CPU bool
}

// Session is a single live compute session: one Colab runtime with one open
// Jupyter kernel. It is safe for concurrent Execute calls — they are
// serialized, since a kernel processes one execute_request at a time.
type Session struct {
	client *ColabClient
	rt     *Runtime
	kc     *KernelClient

	cancel context.CancelFunc // tears down keepalive + ping loops on Stop

	mu      sync.Mutex // serializes Execute against Stop
	stopped bool
}

// SessionStart authenticates, acquires a Colab runtime, and opens a single
// kernel. The returned Session keeps that kernel (and the runtime keepalive)
// alive until Stop is called. ctx governs only startup; the session itself
// outlives it.
func SessionStart(ctx context.Context, opts Opts) (*Session, error) {
	if opts.Store == nil {
		return nil, fmt.Errorf("runtime: Opts.Store is required")
	}
	cpu := opts.CPU
	if opts.GPU == "" && !opts.CPU {
		cpu = true
	}

	// The keepalive and ping loops must outlive the startup ctx, so bind them to
	// a session-scoped context that Stop cancels.
	sessCtx, cancel := context.WithCancel(context.Background())

	client := NewClient(opts.Store)

	rt, err := client.EnsureRuntime(sessCtx, opts.GPU, cpu, replaceMismatchedRuntime)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("acquire runtime: %w", err)
	}

	kc, err := newKernelClient(sessCtx, rt)
	if err != nil {
		_ = client.UnassignRuntime(context.Background(), rt)
		cancel()
		return nil, fmt.Errorf("open kernel: %w", err)
	}

	return &Session{client: client, rt: rt, kc: kc, cancel: cancel}, nil
}

// Execute runs code on the session's kernel and returns the combined output.
// State from previous Execute calls is preserved.
func (s *Session) Execute(ctx context.Context, code string) (string, error) {
	return s.ExecuteStream(ctx, code, nil)
}

// ExecuteStream runs code, streaming output via onOutput as it arrives. If
// onOutput is nil the output is buffered and returned.
func (s *Session) ExecuteStream(ctx context.Context, code string, onOutput func(stream, text string)) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopped {
		return "", fmt.Errorf("runtime: session stopped")
	}
	return s.kc.ExecuteStream(ctx, code, onOutput)
}

// Accelerator reports the runtime's accelerator (e.g. "NONE", "T4").
func (s *Session) Accelerator() string {
	return s.rt.Accelerator
}

// Stop closes the kernel and releases the Colab runtime. It is idempotent.
func (s *Session) Stop(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopped {
		return nil
	}
	s.stopped = true

	closeErr := s.kc.Close()
	unassignErr := s.client.UnassignRuntime(ctx, s.rt)
	s.cancel()

	if unassignErr != nil {
		return fmt.Errorf("release runtime: %w", unassignErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close kernel: %w", closeErr)
	}
	return nil
}
