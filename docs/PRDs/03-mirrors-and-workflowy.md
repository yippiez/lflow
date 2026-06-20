# Mirrors and Workflowy: local node mirrors + Workflowy pull

| | |
|---|---|
| **Status** | Shipped |
| **Date** | 2026-06-12 .. 2026-06-14 |
| **Owner** | eren |
| **Related commits** | 57a1a2d, c0b5b40, b90b256, cea98f6, a72cf91, df4313e, c4f4eb7, af6b159, 66322bc |
| **Related ADRs** | — |

> **Update (2026-06-20):** the Workflowy integration was removed entirely — the
> `wf` client/sync package, the `/mirror:wf` editor pull, the background mirror
> scheduler, the `wf_mirrors` table, and the `workflowy` config block are all gone.
> The local **mirror node** concept (red `◆`, read-only view of a source) remains;
> only the Workflowy source backend was dropped. The Workflowy details below are
> kept as historical record.

## Problem / Context

Workflowy is one optional backend, not the backend. The user's framing: "local sync
first, workflowy integration in the form of: I can mirror nodes from there to here."
The agent repeatedly over-reached toward full bidirectional Workflowy sync and was
corrected: "workflowy sync is misunderstood — I can pull in nodes from workflowy,
not fully."

## Goals

- A mirror node concept: a node that is a live view of a source (the source is the single source of truth), rendered with a red `◆`.
- Pull Workflowy items in as mirrors.
- A background scheduler refreshing visible mirrors every 5s, off-screen ones every 60s, and once on appearance.
- Concurrency-safe sync (SQLite busy timeout).

## Non-goals

- Full bidirectional Workflowy sync.
- A per-node countdown timer glyph (designed, then dropped).
- A standalone `wf` command group (removed; pull moved into the editor).
- A `--every` flag for mirror cadence (cadence is a constant, not user-facing).

## Approach / Design

- Mirror engine + Workflowy internal-api client with workflowy-wins conflict policy and a journal (`57a1a2d`).
- Background mirror scheduler: 5s visible / 60s hidden / sync-on-appear (`b90b256`); SQLite busy timeout for concurrent background sync (`cea98f6`).
- Auth evolved: session id in config (`df4313e`) then the official Workflowy v1 API with a nested `workflowy.apiKey` config (`c4f4eb7`).
- Editor integration: `/pull:wf` lives in the editor; `/mirror_to` dropped, `/move_to` renamed to `/move` (`af6b159`); the `wf` command group removed entirely (`66322bc`).

## Decisions

- Workflowy is an optional pull-only backend; local sync is primary.
- Mirror cadence is a constant (5s visible / 60s hidden), no `--every` flag, no timer glyph. "Every 5 minutes isn't something said to the user."
- A mirror is rendered as a red `◆` and is read-only against its source.
- Auth/secrets live in config, never as a `login` command in shell history.

## UX / Behavior

- Mirror nodes render with a red `◆` glyph.
- `/pull:wf` in the editor pulls Workflowy hits in as mirrors.
- `/move` moves a node; `/copy_link` + paste creates a mirror (see 05-wysiwyg-rows).
- Mirrors refresh silently in the background; no countdown or timer is shown.

## Status & History

- 2026-06-12 `57a1a2d` Workflowy internal-api client + mirror sync engine (workflowy-wins + journal).
- 2026-06-12 `c0b5b40` `wf` command group (login, mirror, list, pull/push, unmirror).
- 2026-06-12 `b90b256` background mirror scheduler (5s visible / 60s hidden, sync-on-appear).
- 2026-06-12 `cea98f6` SQLite busy timeout for concurrent background sync.
- 2026-06-12 `a72cf91` syncer suite against a fake Workflowy server.
- 2026-06-14 `df4313e` set Workflowy session id in config, drop the login command.
- 2026-06-14 `c4f4eb7` official Workflowy v1 API with nested `workflowy.apiKey`.
- 2026-06-14 `af6b159` drop `/mirror_to`, rename `/move_to` to `/move`, add `/pull:wf`.
- 2026-06-14 `66322bc` remove the `wf` command group; pull lives in the editor.

### Pivots

- Earliest design floated a per-node timer glyph `(5)→(4)` counting down to a sync. Dropped: "let's not have timer in mirrors, just the diamond."
- `/mirror` originally swapped the outline for a fuzzy-finder command palette. Dropped.
- The whole `wf` command group was removed; Workflowy pull moved entirely into the editor as `/pull:wf`.
- Auth moved login command to config session id to official v1 API key.
