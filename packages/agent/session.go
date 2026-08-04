package agent

import (
	"encoding/json"
	"strings"
	"sync"
)

// session is the shared, channel-backed Session implementation every backend
// fills in. The producing goroutine sends on events, then calls finish() once to
// record the terminal error/state and close the channel.
type session struct {
	events chan AgentEvent
	stop   func() // cancel the process (idempotent)

	mu    sync.Mutex
	state AgentSessionState
	err   error
	done  bool
}

func newSession(buf int, stop func()) *session {
	return &session{
		events: make(chan AgentEvent, buf),
		stop:   stop,
		state:  AgentStateWorking,
	}
}

func (s *session) Events() <-chan AgentEvent { return s.events }

func (s *session) Stop() {
	s.setState(AgentStateStopped)
	if s.stop != nil {
		s.stop()
	}
}

func (s *session) State() AgentSessionState {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state
}

func (s *session) Err() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.err
}

func (s *session) setState(st AgentSessionState) {
	s.mu.Lock()
	if s.state != AgentStateStopped { // a stop is terminal; later events don't unset it
		s.state = st
	}
	s.mu.Unlock()
}

// finish records the terminal error and closes the event stream exactly once.
func (s *session) finish(err error, st AgentSessionState) {
	s.mu.Lock()
	if s.done {
		s.mu.Unlock()
		return
	}
	s.done = true
	s.err = err
	if s.state != AgentStateStopped {
		s.state = st
	}
	s.mu.Unlock()
	close(s.events)
}

// --- small wire helpers shared by the CLI backends --------------------------

func clip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// lastLine returns the last non-empty line of s.
func lastLine(s string) string {
	var last string
	for _, ln := range strings.Split(s, "\n") {
		if t := strings.TrimSpace(ln); t != "" {
			last = t
		}
	}
	return last
}

// toolDetail pulls a short human detail (file or command) out of a tool's args.
func toolDetail(args json.RawMessage) string {
	var m map[string]any
	if json.Unmarshal(args, &m) != nil {
		return ""
	}
	for _, k := range []string{"path", "file", "file_path", "filename", "command", "cmd", "pattern", "query", "url"} {
		if v, ok := m[k].(string); ok && v != "" {
			return clip(v, 48)
		}
	}
	return ""
}

// resultTail extracts the last non-empty line of a tool's partial result (the
// live output the CLI appends as a tool runs).
func resultTail(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) != nil {
		var obj map[string]any
		if json.Unmarshal(raw, &obj) != nil {
			return ""
		}
		for _, k := range []string{"value", "text", "output", "content", "stdout"} {
			if v, ok := obj[k].(string); ok {
				s = v
				break
			}
		}
	}
	return clip(lastLine(s), 48)
}
