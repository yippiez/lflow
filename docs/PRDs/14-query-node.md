# Query node: live notes query mirroring hits as children

| | |
|---|---|
| **Status** | Shipped |
| **Date** | 2026-06-18 |
| **Owner** | eren |
| **Related commits** | b2aea1a, c5f9957 |
| **Related ADRs** | — |

## Problem / Context

The user wants a live-query node: "takes a query input, queries the whole node
database and live-mirrors the hits below it, updating as lazily as possible (when I
see it on screen)." Matches should appear as read-only mirror children and reconcile
in place — not wipe-and-rebuild.

## Goals

- A query node whose matches appear as first-order read-only mirror children (red `◆`).
- Reconcile by source UUID: update matching children in place, append new hits at the bottom, remove stale ones; a child moved out (shift+tab) regenerates at the bottom on the next refresh.
- Refresh lazily when visible.
- A query language: `:AND :OR`, nesting, `:IS type`, `:BEFORE/:AFTER date`, `>` scoped drill-down; syntax errors render the node red.

## Non-goals

- Recursively expanding hit subtrees (hits are flat first-order children, capped).
- Searching the codebase via ripgrep (tried first, then replaced — see pivot).

## Approach / Design

- `pkg/tui/editor/query.go`: `runQuery` calls `database.SearchNodes` over the user's notes, then `reconcileQueryMirrors` rebuilds the node's first-order mirror children.
- Reconcile: drop previous derived mirrors (keyed off `c.derived`), preserve any real children, then mirror each current match (skipping self, tombstones, empty rows), capped at `queryMaxHits` (50). Each derived child is `collapsed` so a hit shows as one line, not its whole subtree.
- Mirror children are derived/ephemeral — never persisted or synced — regenerated on each run, so moved/renamed findings stay correct.
- Registered in `registry.go` with `sign: "⌕ "`, `inlineEditable: true`, `run: runQuery`. Syntax errors render the node red.

## Decisions

- Query hits are first-order, read-only mirror children only — never recursively expanded (the nesting limit).
- Reconcile by stable source UUID, not wipe-and-rebuild; a child moved out regenerates at the bottom next refresh.
- Derived children never persist or sync.
- Search the lflow node DB, not the codebase.

## UX / Behavior

- Query node rendered with a `⌕ ` sign; alt+r runs the search.
- Matches appear as red `◆` mirror children, collapsed to one line each, capped at 50.
- Refresh is lazy (on visibility); invalid query syntax renders the node red.
- Supported language: `:AND :OR` with nesting, `:IS type`, `:BEFORE/:AFTER date`, `>` scoped drill-down.

## Status & History

- 2026-06-18 `b2aea1a` codebase live-query node — alt+r searches via ripgrep.
- 2026-06-18 `c5f9957` query node searches notes and mirrors matches (not ripgrep).

### Pivot

First implemented against the codebase via ripgrep (`b2aea1a`), then changed to
search the lflow notes/DB and mirror matching nodes (`c5f9957`) — the codebase
variant gave way to the node-query semantics the user actually specified.
