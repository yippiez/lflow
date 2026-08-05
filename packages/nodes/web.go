package nodes

import "github.com/lflow/lflow/packages/database"

// A web-search node: the sibling of a Query node that searches the WEB
// instead of the outline. The two share a shape — the name is the search,
// alt+r runs it, the hits hang under it as REAL child nodes — but a web node
// has no query language: the whole name is the term, handed to the user's
// SearxNG instance (see packages/integrations). The first webResultLimit
// hits become TypeWebResult rows — a link chip whose label is the title and
// whose target is the URL. Re-running replaces the rows the last run made and
// touches nothing else, so a note filed under the search, or a hit moved out
// from under it, survives.
func init() {
	Register(Plugin{
		Key:            database.TypeWeb,
		Label:          "Web Search",
		InlineEditable: true,
		DisableChips:   true,
		Prefix:         webPrefix,
	})
}

// webPrefix mirrors the query node's ⌕ but tinted cyan, so the two search
// nodes read as siblings: the same shape, a different domain.
func webPrefix(Ref) string { return Theme().Cyan + "⌕" + Theme().Reset + " " }
