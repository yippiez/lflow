# Collapse persistence + JSON node: local collapsed column + json type

| | |
|---|---|
| **Status** | Shipped |
| **Date** | 2026-06-17 |
| **Owner** | eren |
| **Related commits** | 5a90f18, 0b2af6f, 382f14e, 939f6f8, 72c7a04 |
| **Related ADRs** | — |

## Problem / Context

Fold state should survive a restart but never sync (it is local view-state). And the
JSON node is the reference implementation for the new registry — the proven pattern
the user wants all rich types to follow: a `{}` sign, a truncated inline preview,
inline-non-editable, and a custom alt+e expanded view.

## Goals

- A local-only `collapsed` column that persists fold state across restarts and never syncs.
- A `json` node type: `{}` sign + truncated inline preview, red invalid-state line, inline-non-editable, alt+e full-panel pretty syntax-colored editor.

## Non-goals

- Syncing collapsed state or any local view-state.
- Inline editing of JSON nodes (alt+e only).

## Approach / Design

- A local-only `collapsed` column (schema 19, `5a90f18`); persisted across restarts (`0b2af6f`) with a save-time backstop (`382f14e`). Local-only means it is never included in sync fragments.
- JSON node (`pkg/tui/editor/json.go`, registered in `registry.go` with `inlineEditable: false`, `render: renderJSONPreview`, `expand: openJSON`): shows a `{}` sign + truncated inline preview, renders red `· JSON parsing failed` when invalid, and opens a full-panel pretty, syntax-colored editor on alt+e (`939f6f8`, `72c7a04`).

## Decisions

- Local view-state (collapsed) is local-only and never syncs — the model for all later derived/view state.
- JSON is the reference rich type: `{}` sign, truncated preview, inline-non-editable, custom alt+e expanded view. All later rich types follow this pattern.

## UX / Behavior

- Collapsed state restores on reopen.
- JSON node renders `{}` + a truncated one-line preview; invalid JSON shows a red `· JSON parsing failed`.
- Typing/backspace/enter on a JSON node is a no-op; alt+e opens a full-panel pretty, syntax-colored editor.

## Status & History

- 2026-06-17 `5a90f18` add local-only collapsed column (schema 19).
- 2026-06-17 `0b2af6f` persist collapsed state across restarts.
- 2026-06-17 `382f14e` persist collapse on save as a backstop.
- 2026-06-17 `939f6f8` JSON node type: inline `{}` preview + invalid state.
- 2026-06-17 `72c7a04` alt+e JSON editor — full-panel, pretty, syntax-colored.
