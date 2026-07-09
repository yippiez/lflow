// Package agent is lflow's CLI coding-agent layer: a uniform, steerable Session
// over the pi / opencode / grok command-line agents. It is the Go counterpart of
// work2/pir's Effect-based harness — but idiomatic Go (context for scope, channels
// for the event stream, errors for typed failures; no Effect runtime, which has no
// Go port). The TUI talks to this package instead of shelling out to pi directly.
package agent

import (
	"context"
	"encoding/json"
	"sync"
)

// Provider identifies a CLI coding-agent backend (pir's "opencode" | "pi" | "grok").
type Provider string

const (
	ProviderPi Provider = "pi"
)

// EventKind selects which fields of an Event are set. Richer than pir's three
// variants because lflow's worker line distinguishes tool starts, live tool
// output, usage, turn ends and errors.
type EventKind int

const (
	EventToolStart  EventKind = iota // Tool + Detail + Args: a tool began
	EventToolUpdate                  // Tool + Detail: live tool output tail
	EventAgentText                   // Text: streamed assistant/thinking text
	EventUsage                       // Usage: token/cost delta for a turn
	EventTurnEnd                     // Status: a turn finished ("idle" | "error")
	EventError                       // Text: an error surfaced mid-run
	EventLog                         // Text + IsErr: a raw transcript line (stderr)
)

// Event is one normalized line from a running turn (pir's AgentEvent, flattened
// into a struct to stay close to each CLI's wire decode).
type Event struct {
	Kind   EventKind
	Tool   string          // EventToolStart / EventToolUpdate
	Detail string          // short human detail (file / command / tail)
	Args   json.RawMessage // raw tool args (EventToolStart) — the editor reads
	//                        finish_worker's nodes out of this
	Text   string // assistant text / error message / log line
	Status string // "idle" | "error" for EventTurnEnd
	IsErr  bool   // EventLog: stderr vs stdout
	Usage  *Usage // EventUsage
}

// Usage is a running token/cost total for a session.
type Usage struct {
	Model     string // "provider/model" reported by the CLI
	In, Out   int
	Cost      float64
	Estimated bool // Cost is an estimate (e.g. grok — see EstimateCost), not CLI-reported
}

// SessionState is the live lifecycle of a conversation (pir's SessionState).
type SessionState string

const (
	StateWorking SessionState = "working"
	StateIdle    SessionState = "idle"
	StateError   SessionState = "error"
	StateStopped SessionState = "stopped"
)

// Session is a live, steerable agent conversation. The underlying process stays
// alive across turns; Steer pushes a follow-up message into the same conversation
// (pir's AgentSession.steer). Events is closed when the process exits, after which
// Err reports the terminal error (nil = clean exit).
type Session interface {
	Events() <-chan Event       // normalized stream; closed when the process exits
	Steer(message string) error // follow-up into the running conversation
	Stop()                      // intentional cancel (not an error)
	State() SessionState
	Err() error
}

// RunOptions configures a fresh turn. Fields a given backend does not support are
// ignored (e.g. Extensions is pi-only).
type RunOptions struct {
	Model        Model    // which CLI + model to run (Model.CLI selects the backend)
	Thinking     string   // "", "off", "low", "medium", "high"
	Tools        []string // tool allowlist
	SystemPrompt string   // appended system prompt
	Extensions   []string // pi --extension paths (pi only)
	Skills       []string // pi --skill paths (pi only): skill files or directories
	Cwd          string   // working directory ("" = inherit)
	// SessionID is a stable, resumable conversation id. Sessions are the default:
	// re-running with the same id RESUMES the real on-disk conversation (the agent
	// keeps its memory) instead of starting over. The editor uses the worker node's
	// uuid. SessionDir pins where the backend stores sessions (pi --session-dir).
	SessionID  string
	SessionDir string
}

// Run starts a fresh turn on provider p (pir's run()). The provided context scopes
// the process: cancelling it (or Session.Stop) terminates the agent.
func Run(ctx context.Context, p Provider, task string, opts RunOptions) (Session, error) {
	b, ok := Get(p)
	if !ok {
		return nil, &Error{Provider: p, Message: "unknown provider"}
	}
	return b.Run(ctx, task, opts)
}

var (
	modelsMu     sync.Mutex
	modelsCached []Model
)

// ListModels aggregates the selectable models across all available backends,
// degrading gracefully when a CLI is missing or fails (pir's parallel listModels).
// The result is cached after the first non-empty fetch so the model picker can
// filter per-keystroke without re-shelling the CLIs; call RefreshModels to clear.
func ListModels() []Model {
	modelsMu.Lock()
	defer modelsMu.Unlock()
	if modelsCached != nil {
		return modelsCached
	}
	var out []Model
	for _, b := range Backends() {
		if !b.Available() {
			continue
		}
		ms, err := b.ListModels()
		if err != nil {
			continue
		}
		out = append(out, ms...)
	}
	if len(out) > 0 {
		modelsCached = out
	}
	return out
}

// RefreshModels clears the ListModels cache so the next call re-queries the CLIs.
func RefreshModels() {
	modelsMu.Lock()
	modelsCached = nil
	modelsMu.Unlock()
}

// Error is a typed provider failure (pir's ProviderError).
type Error struct {
	Provider Provider
	Message  string
	Cause    error
}

func (e *Error) Error() string {
	s := string(e.Provider) + ": " + e.Message
	if e.Cause != nil {
		s += ": " + e.Cause.Error()
	}
	return s
}

func (e *Error) Unwrap() error { return e.Cause }
