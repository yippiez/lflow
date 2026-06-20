# Outline editor core: node model + inline editor + CLI surface

| | |
|---|---|
| **Status** | Shipped |
| **Date** | 2026-06-12 |
| **Owner** | eren |
| **Related commits** | cd4b8d6, 1706337, 42bfe32, e0cdf32, e026299, 899daa7, 07bd92d |
| **Related ADRs** | — |

## Problem / Context

dnote's model is books-and-notes. lflow needs a single unified outline model where
"everything is a node," modeled on Workflowy and the pi-prompt-chain node editor.
The user wants two things at once: a Workflowy-style outliner where "you basically
pick a node to work on and only that node's content is displayed," and a scriptable
tool — "I find the node and I append the results from some bash commands from stdin
into it."

## Goals

- A unified `node` data model (everything is a node; `type` is a free string) on top of dnote's SQLite + USN sync.
- Migrate dnote books to h1 roots and notes to child nodes.
- A best-match resolver so commands act on the closest hit by name.
- One-shot, pipe-friendly CLI commands: add/append/list/find/edit/rm/mv/complete/export.
- An inline bubbletea editor (`lflow node open <name>`) drawing in terminal scrollback, with a slash menu, fuzzy finder, and zoom.
- Server-side node model with `/api/v3/nodes` CRUD and end-to-end sync.

## Non-goals

- Alternate-screen UI (explicitly avoided; later lint-enforced).
- Rich node types beyond plain text/bullets (those come later via the registry).

## Approach / Design

- Data model: `pkg/tui/database` — one `nodes` table with fts5; a migration converts dnote books/notes into the node tree.
- Sync: the dnote USN engine and API client are adapted to operate on nodes (`1706337`); server gains the node model, `/api/v3/nodes` CRUD and sync fragments (`e0cdf32`).
- Resolver: best-match by name so commands target the closest node without exact ids (`42bfe32`).
- Editor: `pkg/tui/editor` — scrollback-mode bubbletea editor with slash menu, fuzzy finder, and zoom (`899daa7`). It renders into normal scrollback, never the alternate screen.
- Logging: debug routed to stderr so stdout stays clean for pipes (`07bd92d`).

## Decisions

- Local SQLite first; remote sync is optional.
- Everything-is-a-node with a free-string `type` so future node types need no DB migration.
- Editor never uses the alternate screen (later enforced by an ast-grep lint rule, see 06-editor-hardening).
- Debug to stderr, results to stdout, so commands stay pipeable.

## UX / Behavior

- `lflow node open <name>` opens the editor on the best match; with no arg it opens root; an id opens directly.
- One-shot commands print plain pipeable output to stdout.
- Editor: slash menu for commands, fuzzy finder to jump to nodes, zoom to focus a subtree.

## Status & History

- 2026-06-12 `cd4b8d6` node data model, wf_mirrors + fts5, dnote books/notes migration.
- 2026-06-12 `1706337` adapt USN sync engine and API client to nodes.
- 2026-06-12 `42bfe32` best-match resolver + one-shot command surface.
- 2026-06-12 `e0cdf32` server node model + /api/v3/nodes CRUD + sync fragments.
- 2026-06-12 `e026299` node sync end-to-end suite.
- 2026-06-12 `899daa7` inline outline editor (scrollback, slash menu, fuzzy finder, zoom).
- 2026-06-12 `07bd92d` route debug output to stderr.
