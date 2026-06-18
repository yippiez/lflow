# Fork rename: dnote to lflow

| | |
|---|---|
| **Status** | Shipped |
| **Date** | 2026-06-05 .. 2026-06-12 |
| **Owner** | eren |
| **Related commits** | d1f8af3, c369ce9, 2188ac9, b2cba56, c66d176, 8663a38, 4b7f2c9 |
| **Related ADRs** | — |

## Problem / Context

lflow starts as a fork of dnote, a CLI note app. The goal is to reshape it into a
local-first terminal outline editor, not to write one from scratch. The very first
chat instruction was simply to rename git's `master` branch to `main` on local and
remote; the rest of this epoch removes the dnote identity from the codebase.

## Goals

- Rename the Go module, copyright headers, build scripts, docker config, web assets, email templates and completion scripts from dnote to lflow.
- Remove the inherited GitHub CI workflows.
- Rename git `master` to `main` (local and remote).
- Keep dnote's working bones (SQLite backend, USN sync engine) so they can be adapted rather than rewritten.

## Non-goals

- Rewriting the storage or sync engine from scratch.
- Removing dnote machinery wholesale at this stage (the repo-restructure trim comes later, see 19-repo-restructure).

## Approach / Design

A mechanical rename pass across the tree: Go module path, license/copyright
headers, install/build scripts, docker files, the web assets and email templates,
and shell completion scripts. CI workflows are deleted. Test fixtures that
referenced dnote env vars (e.g. `DNOTE_TMPCONTENT` to `LFLOW_TMPCONTENT`) are
updated so the suite stays green.

## Decisions

- Adapt, don't rewrite: keep dnote's SQLite backend and sync engine. "If it can be adapted, adapt it; if it's too hard to adapt, consider removing — but ask me beforehand."
- Default branch is `main`.

## UX / Behavior

- No user-facing UX yet; this epoch is identity/plumbing only. Binary is still built and installed to `~/.local/bin/lflow`.

## Status & History

- 2026-06-07 `d1f8af3` remove github workflows.
- 2026-06-07 `c369ce9` rename Go module dnote to lflow.
- 2026-06-07 `2188ac9` update build scripts and docker config.
- 2026-06-07 `b2cba56` update docs and install script.
- 2026-06-07 `c66d176` update web assets, email templates, completion scripts.
- 2026-06-07 `8663a38` fix editor test env var references.
- 2026-06-12 `4b7f2c9` rename copyright headers dnote to lflow.
