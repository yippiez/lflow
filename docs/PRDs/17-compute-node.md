# Compute node: NL to snippet (spec'd, deferred)

| | |
|---|---|
| **Status** | Planned |
| **Date** | 2026-06-18 |
| **Owner** | eren |
| **Related commits** | f6e75e2, 205880c (spec/plan only, no implementation) |
| **Related ADRs** | — |

## Problem / Context

The user reasoned aloud about "compute/cell nodes" vs a "collab collection node" and
settled on a compute node: "a natural-language description of a code snippet I can
toggle between... a dash arrow... so it won't interfere with the notes too much."
Generation first, execution later. This is spec'd but explicitly deferred until
bash/query/worker feel right.

## Goals

- A `→`-prefixed natural-language description of a code snippet.
- alt+r generates code into `.pi/snippets/`.
- Toggle the node between the NL description and the generated code block.

## Non-goals (for now)

- Python execution (local/Colab) — a follow-on after generation lands.
- Any implementation in this epoch (deferred).

## Approach / Design

- Spec only, in `docs/SPECs` (`f6e75e2`) and the build plan (`205880c`). No implementation commit.
- When built, the compute node will follow the registry pattern: a `→` sign, a `run` func that generates code into local files (`.pi/snippets/`, never the synced DB), and a toggle between the NL prompt and the generated code block (likely an `expand` / inline-toggle).

## Decisions

- Explicitly deferred: "compute is deferred until bash/query/worker feel right."
- `→` is reserved for the compute node — taken back from the worker, which now has no prefix (see 16).
- Generation first, execution later.
- Generated snippets live in local files, not the synced DB (storage discipline).

## UX / Behavior (planned)

- Compute node rendered with a `→` sign showing the NL description.
- alt+r generates the code snippet into `.pi/snippets/`.
- A toggle flips the node between the NL description and the generated code block.

## Status & History

- 2026-06-18 `f6e75e2`, `205880c` spec + plan only. No implementation; status remains Planned.
