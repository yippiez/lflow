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

// AgentProvider identifies a CLI coding-agent backend (pir's "opencode" | "pi" | "grok").
type AgentProvider string

const (
	AgentProviderPi   AgentProvider = "pi"
	AgentProviderGrok AgentProvider = "grok"
)

// AgentEventKind selects which fields of an Event are set. Richer than pir's three
// variants because lflow's worker line distinguishes tool starts, live tool
// output, usage, turn ends and errors.
type AgentEventKind int

const (
	AgentEventToolStart  AgentEventKind = iota // Tool + Detail + Args: a tool began
	AgentEventToolUpdate                  // Tool + Detail: live tool output tail
	AgentEventText                   // Text: streamed assistant/thinking text
	AgentEventUsage                       // Usage: token/cost delta for a turn
	AgentEventTurnEnd                     // Status: a turn finished ("idle" | "error")
	AgentEventError                       // Text: an error surfaced mid-run
	AgentEventLog                         // Text + IsErr: a raw transcript line (stderr)
)

// AgentEvent is one normalized line from a running turn (pir's AgentEvent, flattened
// into a struct to stay close to each CLI's wire decode).
type AgentEvent struct {
	Kind   AgentEventKind
	Tool   string          // EventToolStart / EventToolUpdate
	Detail string          // short human detail (file / command / tail)
	Args   json.RawMessage // raw tool args (EventToolStart) — the editor reads
	//                        finish_worker's nodes out of this
	Text   string // assistant text / error message / log line
	Status string // "idle" | "error" for EventTurnEnd
	IsErr  bool   // EventLog: stderr vs stdout
	AgentUsage  *Usage // EventUsage
}

// Usage is a running token/cost total for a session.
type Usage struct {
	AgentModel   string // "provider/model" reported by the CLI
	In, Out int
	Cost    float64
}

// AgentSessionState is the live lifecycle of a conversation (pir's AgentSessionState).
type AgentSessionState string

const (
	AgentStateWorking AgentSessionState = "working"
	AgentStateIdle    AgentSessionState = "idle"
	AgentStateError   AgentSessionState = "error"
	AgentStateStopped AgentSessionState = "stopped"
)

// AgentSession is one live agent run. Events is closed when the process exits,
// after which Err reports the terminal error (nil = clean exit).
type AgentSession interface {
	Events() <-chan AgentEvent // normalized stream; closed when the process exits
	Stop()                // intentional cancel (not an error)
	State() AgentSessionState
	Err() error
}

// AgentRunOptions configures a fresh turn. Fields a given backend does not support are
// ignored (e.g. Extensions is pi-only).
type AgentRunOptions struct {
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
	// NoSession runs the turn launch-and-forget (pi --no-session): nothing is
	// written to backend session storage and there is nothing to resume. It wins
	// over SessionID.
	NoSession bool
}

// AgentRun starts a fresh turn on provider p (pir's run()). The provided context scopes
// the process: cancelling it (or Session.Stop) terminates the agent.
func AgentRun(ctx context.Context, p AgentProvider, task string, opts AgentRunOptions) (AgentSession, error) {
	b, ok := AgentBackendFor(p)
	if !ok {
		return nil, &Error{Provider: p, Message: "unknown provider"}
	}
	return b.Run(ctx, task, opts)
}

var (
	modelsMu     sync.Mutex
	modelsCached []Model
)

// AgentListModels aggregates the selectable models across all available backends,
// degrading gracefully when a CLI is missing or fails (pir's parallel listModels).
// The result is cached after the first non-empty fetch so the model picker can
// filter per-keystroke without re-shelling the CLIs.
func AgentListModels() []Model {
	modelsMu.Lock()
	defer modelsMu.Unlock()
	if modelsCached != nil {
		return modelsCached
	}
	var out []Model
	for _, b := range AgentBackends() {
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


// Error is a typed provider failure (pir's ProviderError).
type Error struct {
	Provider AgentProvider
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
