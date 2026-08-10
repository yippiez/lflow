// Package integrations is home to every external service lflow talks to,
// one file set per service:
//
//   - SearxNG (searxng.go) — the search backend behind the websearch node
//     type. It asks a SearxNG instance for a query and hands back plain
//     (title, url) pairs. SearxNG is the only backend, deliberately: it is
//     the user's own metasearch — self-hosted or one they trust — so the
//     query goes exactly where they pointed it and nowhere else, its JSON
//     output is a real contract rather than scraped markup, and it needs no
//     account and no API key.
//   - archive.org (archiveorg.go) — the search backend behind the archive
//     node type. It asks the Internet Archive's advanced-search API about the
//     archive's own ITEMS (scanned books, recordings, films, software) and
//     hands back the same plain (title, url) pairs, each url an item's
//     /details/ page. Deliberately not the Wayback Machine: a search node
//     starts from words, not from a URL the user already has. One public
//     host, no account and no key, so nothing is configured.
//   - Zotero (zotero*.go) — reads the local Zotero library (the desktop
//     app's zotero.sqlite) and turns its entries into citable references
//     (the editor's zotero chip) and mirrored subtrees (the zotero node).
//     Nothing here writes: the database is opened through a throwaway
//     snapshot copy, so a running Zotero is never disturbed and the outline
//     never depends on Zotero's schema staying still.
//   - Workflowy (workflowy*.go) — a thin client for the official Workflowy
//     REST API plus the translate layer that turns Workflowy nodes into
//     lflow node fields, behind the editor's wf node type. Pulling a subtree
//     in is the implemented direction; pushing edits back is a future step.
//
// WARNING (invariant): credentials.json is local-only config — it is never
// written into the outline DB and never synced. Each service reads its own
// block of the consolidated ~/.config/lflow/credentials.json.
package integrations
