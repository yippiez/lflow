// Package nlp is lflow's natural-language layer: each exported function maps a
// natural-language intent to a concrete result — Match resolves a query to the
// outline nodes it refers to, Compute turns an instruction into code. Nothing
// here shells out to a provider yet; both functions are stubs until the
// semantic engine lands.
package nlp

import (
	"context"
	"errors"

	"github.com/lflow/lflow/packages/database"
)

// ErrNotImplemented is returned by functions whose engine has not landed yet.
var ErrNotImplemented = errors.New("nlp: not implemented yet")

// Event is one live-progress frame streamed to Compute's callback (nil callback
// = no live view).
type Event struct {
	Op   string // "tool" | "thinking" | "message" | "error" | "done"
	Text string
	Tool string
}

// MatchResult is one node the semantic query resolved to, best match first.
type MatchResult struct {
	UID   string  // node uuid
	Text  string  // the matched node text, for display
	Score float64 // 0..1; 1 = exact
}

// Match resolves a natural-language query against the outline DB and returns
// the nodes it refers to (best match first). Cancelling ctx aborts the search.
func Match(ctx context.Context, db *database.DB, query string) ([]MatchResult, error) {
	return nil, ErrNotImplemented
}

// Compute runs one natural-language instruction turn and returns the code it
// produced. onEvent receives live progress frames while the turn runs (may be
// nil). Cancelling ctx stops the turn.
func Compute(ctx context.Context, prompt string, onEvent func(Event)) (string, error) {
	return "", ErrNotImplemented
}
