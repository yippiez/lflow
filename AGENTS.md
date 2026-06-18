# AGENTS.md — operating guide for AI agents working in lflow

## What lflow is

lflow is a local-first **terminal outline editor** (Go + bubbletea), forked from
dnote and reshaped into a Workflowy-style outliner. The whole tree lives in one
SQLite file. Every CLI command is one-shot and pipe-friendly; `lflow node open`
drops into an inline editor that draws in the terminal **scrollback**, never the
alternate screen. Everything is a node with a free-string `type`, and node types
are an extensible registry (one descriptor per type in `pkg/tui/editor/registry.go`).

## REQUIRED: record every non-trivial decision in docs/ADR.md

**This is the most important rule in this file.** Whenever you make a non-trivial
choice — whether you decided it autonomously or the user directed it — append an
entry to [docs/ADR.md](docs/ADR.md) using the exact format documented there:

```
---
title: <short decision title>

Why
<1-3 sentences: reasoning / problem solved / alternatives rejected>

When
<date — and commit hash(es) if applicable>
---
```

Append newest at the BOTTOM. Never edit past entries; if you reverse a decision,
add a new entry that says so. Read docs/ADR.md before starting so you do not
re-litigate settled choices (e.g. no inline markup, no emojis, no alt-screen).

## Hard build / test / run conventions

- **Build with the fts5 tag, always.** SQLite FTS5 is required by node triggers.
  ```sh
  go build --tags fts5 ./pkg/tui
  ```
- **Install the dev binary after every change** so the user can test the real thing:
  ```sh
  go build --tags fts5 -ldflags "-X main.versionTag=0.1.0-dev" -o ~/.local/bin/lflow ./pkg/tui
  ```
  (Go 1.25.3 lives at ~/sdk.)
- **Test in isolation — never against the real outline.** Drive all live/tmux
  tests against a throwaway HOME and XDG dirs with a seeded DB. The real DB is
  `~/.local/share/lflow/lflow.db`; keystroke automation has deleted a real node
  before. SQLite surgery must go through a `-tags fts5` Go program — the `sqlite3`
  CLI lacks fts5.
- **Run `go test --tags fts5 ./...`** before installing.
- **Commit each logical change** as its own `label: description` commit and push to
  the feature branch. Do not batch work at the end.
- **No emojis, ever.** Signs/glyphs are plain Unicode text symbols
  (○ ◆ ▸ ● ▁▂▇ ⌕ $ {} → ◌). No ✓/✗, no media-control glyphs (▶⏸⏹⏺), no VS16
  selectors. CLI output uses `→` and `·`, never tick emoji or parenthesized asides.

## Core invariants (do not break — see docs/ADR.md for the why)

- Inline scrollback only, never the alt-screen (ast-grep lint-enforced).
- No markup leaks into stored text: styling, dates, links are per-node attributes
  or rendered chips — never inline markers that pollute the name, FTS, or export.
- The status bar must be the last rendered line (renderer / `rowBudget` constraint).
- Everything is a node; `nodes.type` is a free string, so new types need no DB
  migration. Add a self-contained type file in `pkg/tui/editor/` and register it
  in the NodeType descriptor table — do not scatter `switch typ` logic.
- Rich/run output (e.g. bash runs) is ephemeral and in-memory only — never
  persisted or synced; a generic per-node `NodeInternalData` JSON blob is a
  planned-but-unimplemented store, not a current one. Big/binary content (voice,
  snippets, transcripts) lives in local files under
  `~/.local/share/lflow/<kind>/<uuid>` with derived data recomputed on demand.
  Do not add a per-feature DB column.
- Never auto-run: bash/query/worker/voice execute only on alt+r (second alt+r
  cancels). The server stores runnable types as opaque strings, never executes.
- Never synced: local view-state (collapsed), derived/generated content, the
  Temporary Domain, voice/binary files, and **secrets**. Today secrets live in
  local config stores — Workflowy keys in `~/.lflow/settings.json`, Pi settings in
  `~/.pi/agent/settings.json`; a consolidated `~/.config/lflow/credentials.json`
  (mode 0600) is the planned target for `lflow auth`, not yet implemented. Either
  way, secrets are never in argv, shell history, the synced DB, or logs.
- Derived children (query hits, generated) are a bounded flat list of direct
  children, reconciled by stable source ID — never recursive, never wipe-rebuild.
- The worker invokes `pi` directly via exec (RPC mode); an `AgentProvider` Go
  interface for other CLI agents is planned, not yet built. `→` is reserved for the
  deferred compute node.

## Where things live

- **docs/ADR.md** — the Agent Decision Record log. Read it first; append to it as
  you work.
- **docs/PRDs/** — the PRD library for features. Copy
  [docs/PRDs/00-template.md](docs/PRDs/00-template.md) to start a new feature's PRD.
- **docs/SPECs/** — folder-per-spec for runnable node types; implement one type at
  a time against its spec.
- **docs/COMMANDS.md**, **docs/SELF_HOSTING.md** — CLI and server reference.
- **pkg/tui/editor/** — the inline editor and all node types (registry.go is the
  extension seam; one file per type).
- **rules/**, **rule-tests/**, **sgconfig.yml** — ast-grep design-invariant lint.

## Design / review workflow

Features are validated via dark, minimal, self-contained HTML picker reports with
real tmux-captured terminal snapshots; the user reviews in a browser and pastes
back a synthesized decision. Those reports/ADRs/images live in `/tmp/lflow-design/`,
never in the repo — only the capture tooling stays in-repo. Verify UI claims by
reading the rendered PNGs, not by assertion.
