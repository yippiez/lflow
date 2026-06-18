# Bash node: shell command node, alt+r runs, streamed output band

| | |
|---|---|
| **Status** | Shipped |
| **Date** | 2026-06-18 |
| **Owner** | eren |
| **Related commits** | 38b84e6, 15305e9, 205880c |
| **Related ADRs** | — |

## Problem / Context

The user wants a node whose text is a shell command, run on demand. "I want to add
bash node type... `o $ bash command here`... not auto run, alt+r to run if cursor on
that node... we need a system for async exec." The user explicitly rejected
checkmarks and a separate results pane, and demanded no database changes for the
output.

## Goals

- A bash node rendered `○ $ command` (bullet kept, `$` gray prefix — not a glyph replacement).
- alt+r runs it async; a second alt+r cancels.
- Output streams into a self-contained gray band under the same node: stdout white, stderr red, capped to ~3 lines with a transparent muted-gray bottom line `exit N · ⋯ N more · alt+e expand`.
- A pulsing `◐` spinner while running, reverting to plain `○` when done — no ✓/✗.
- alt+e opens a full-width-rule reader.

## Non-goals

- Auto-running on load/sync/paste/import.
- A separate results pane / "NodeGhostLand" (designed, then dropped).
- Checkmark/cross status glyphs.
- Any DB schema change per feature.
- A colored chip on the command text (command text stays plain gray).

## Approach / Design

- `pkg/tui/editor/bash.go`: `runBash` spawns `bash -c <cmd>` via `exec.CommandContext`, streaming each stdout/stderr line onto a channel as `bashLineMsg{uuid,text,err}`, ending with `bashDoneMsg{uuid,exit}`. A second alt+r calls the stored cancel func.
- Output is keyed by uuid in in-memory maps (`runOut`, `runCh`, `runCancel`); the band renders from there.
- Persistence: none. Run output is ephemeral and in-memory only (the `runOut` map), never persisted or synced. A per-node JSON blob `NodeInternalData` holding `BashRun` records was discussed — "have a NodeInternalData string and make this an internal json each node can have, that way we won't have to save it as nodes" — but it was never implemented; no such struct or column exists.
- The bash node registers in `registry.go` with `sign: "$ "`, `inlineEditable: true`, `run: runBash`.

## Decisions

- No checkmark, no cross: a pulsing `◐` spinner shows working, reverting to `○` when done. "I like spinner circle to showcase working but don't checkmark and cross."
- Command text is plain gray; only the output box is colored.
- Output is ephemeral in-memory only (never persisted/synced), not new nodes and not a new DB column. The planned `NodeInternalData` JSON blob was never implemented.
- Never auto-run; alt+r only, second alt+r cancels.

## UX / Behavior

- Rendered `○ $ command`; `$` is a gray prefix, the bullet stays.
- alt+r runs (async); second alt+r cancels; `◐` pulses while running.
- Output band beneath the node: stdout white, stderr red, ~3 lines max, bottom line `exit N · ⋯ N more · alt+e expand` in muted gray.
- alt+e opens a full-width-rule reader of the full output.

## Status & History

- 2026-06-18 `38b84e6` bash node — alt+r runs the command, streamed output.
- 2026-06-18 `15305e9` output box / temp panel divider work (shared commit, see 18-temporary-domain).
- 2026-06-16/17 `205880c` build plan; spec in `docs/SPECs`.

### Pivot

A "NodeGhostLand" — a second results pane below the footer for bash output and future
agents — was designed and mocked, then dropped: "I think it should just be a node
instead of this custom results thing." The plan evolved from child nodes to one node
per run to an intended `NodeInternalData` JSON blob on the node itself, but the
shipped code keeps output ephemeral in-memory and persists nothing.
