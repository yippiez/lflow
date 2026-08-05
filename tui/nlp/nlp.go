// Package nlp is lflow's natural-language layer: each exported function maps a
// natural-language intent to a concrete result — Match resolves a query to the
// outline nodes it refers to, Compute turns an instruction into code, and
// Calculate evaluates an arithmetic expression embedded in a query. Nothing
// here shells out to a provider yet; Match and Compute are stubs until the
// semantic engine lands.
package nlp

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/lflow/lflow/tui/database"
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

// Calculate evaluates the arithmetic expression embedded in a natural-language
// query and returns the result as a string: Calculate(ctx, "what is sqrt(16)?")
// returns "4". Bare expressions work unchanged. Common question wrappers
// ("what is", "compute", ...) and trailing punctuation are tolerated; unknown
// variables, invalid expressions, and division by zero are reported as errors.
// Cancelling ctx aborts the attempt.
func Calculate(ctx context.Context, query string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	var fallbackErr error // last "unknown variable" error, in case no candidate resolves
	for _, candidate := range candidates(query) {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		out, err := calculate(candidate, "")
		if err == nil {
			return out, nil
		}
		msg := err.Error()
		switch {
		case strings.Contains(msg, " at position "):
			// Parse failure: not an expression, try a simpler reading.
		case strings.Contains(msg, "unknown variable"):
			// Implicit multiplication makes "what is 2+2?" parse as a product
			// of variables; keep looking for the expression underneath.
			fallbackErr = err
		default:
			// Division by zero, domain error, ...: a real expression was found.
			return "", fmt.Errorf("nlp: %w", err)
		}
	}
	if fallbackErr != nil {
		return "", fmt.Errorf("nlp: %w", fallbackErr)
	}
	return "", fmt.Errorf("nlp: no arithmetic expression found in %q", query)
}

// nlPrefixes are question/intent wrappers stripped from a query to reveal the
// expression underneath.
var nlPrefixes = []string{
	"what is the value of ",
	"what's the value of ",
	"the value of ",
	"the answer to ",
	"the result of ",
	"what is ",
	"what's ",
	"whats ",
	"compute ",
	"calculate ",
	"evaluate ",
	"solve ",
	"find ",
	"value of ",
	"answer to ",
	"result of ",
}

// candidates lists progressively simpler readings of a query: raw, minus
// trailing punctuation, and minus a leading question wrapper. Deduplicated.
func candidates(query string) []string {
	var out []string
	seen := map[string]bool{}
	add := func(s string) {
		s = strings.TrimSpace(s)
		if s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	lower := strings.ToLower(query)
	add(query)
	add(strings.TrimRight(query, "=?!.;:, \t\n\r"))
	for _, p := range nlPrefixes {
		if strings.HasPrefix(lower, p) {
			add(query[len(p):])
			add(strings.TrimRight(query[len(p):], "=?!.;:, \t\n\r"))
		}
	}
	return out
}
