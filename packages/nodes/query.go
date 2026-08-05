package nodes

import "github.com/lflow/lflow/packages/database"

// A live-query node: its name is a search; alt+r searches the user's notes —
// both saved nodes (full live set) and unsaved ones currently in memory — and
// reconciles MIRROR children of the matches (first-order only). The mirrors
// are REAL persisted nodes, so they survive a relaunch; re-running reconciles
// them in place (add new matches, drop stale ones) to minimize churn.
func init() {
	Register(Plugin{
		Key:            database.TypeQuery,
		Label:          "Query",
		InlineEditable: true,
		DisableChips:   true,
		Prefix:         queryPrefix,
		SpanColor:      querySpanColor,
	})
}

// queryPrefix is deliberately scope-neutral: query scope is expressed by the
// persisted :in: parameter, whose omitted value is the outline root.
func queryPrefix(Ref) string { return Theme().Dim + "⌕" + Theme().Reset + " " }

// querySpanColor tints the semantic-search delimiters. A "quoted" phrase is
// the query language's one operator that changes HOW matching works, so — as
// with the math node's yellow operators — the marks carry the color and the
// phrase inside stays ordinary text. An unterminated opening quote is tinted
// too: the atom is already live while the closing quote is still being typed.
func querySpanColor(runes []rune) map[int]string {
	var quotes []int
	for i, r := range runes {
		if r == '"' {
			quotes = append(quotes, i)
		}
	}
	out := make(map[int]string, len(quotes))
	for _, i := range quotes {
		out[i] = Theme().Yellow
	}
	return out
}
