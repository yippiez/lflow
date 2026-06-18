# Date chips: format-detected date pills, no stored brackets

| | |
|---|---|
| **Status** | Shipped |
| **Date** | 2026-06-13 .. 2026-06-14 |
| **Owner** | eren |
| **Related commits** | 8bd710f, a962798, b0b5b1d, b794f12, 0009e03 |
| **Related ADRs** | — |

## Problem / Context

Dates should be rendered chips, not bracketed text. "Dates should not have brackets
too — they are specially rendered, if they match the format they get color even if
node is colored." This continues the WYSIWYG principle that no markup leaks into the
stored node name.

## Goals

- Recognized date formats render automatically as chips, with no brackets stored in the node name.
- The chip colors only the background and survives even when the node text is colored.
- `ctrl+t` converts the date phrase at the caret into a chip.

## Non-goals

- Storing any bracket or marker syntax for dates in the node text.

## Approach / Design

- Date detection and chip rendering in `pkg/tui/editor/date.go` (tested in `date_test.go`); both Turkish and English phrases are first-class.
- Chips are detected at render time from the stored plain text — nothing extra is persisted.
- The chip sets a background color only, so it stays legible even when the node text has a foreground color (`a962798`).
- `ctrl+t` converts the date phrase at the caret; an unclosed bracket no longer corrupts the pill (`b794f12`, `0009e03`).

## Decisions

- Dates are specially-rendered chips, never stored brackets/markers (continuation of the no-markup-leaks rule, see 05/07).
- Date chip colors background only, so it survives a colored node.

## UX / Behavior

- A recognized date phrase renders as a background-colored chip inline, with no brackets in the stored text.
- `ctrl+t` converts the date phrase at the caret into a chip.
- Turkish and English phrases both detected (e.g. `now`, `11 şubat 2025 saat 15:20`).

## Status & History

- 2026-06-13 `b794f12` `ctrl+t` converts the date phrase at the caret.
- 2026-06-13 `0009e03` `ctrl+t` on an unclosed bracket no longer corrupts the pill.
- 2026-06-14 `8bd710f` dates are format-detected chips, no brackets stored.
- 2026-06-14 `b0b5b1d` date pill renders as a chip without brackets; drop duplicate note label while editing.
- 2026-06-14 `a962798` date chip colors background only (same commit adds `/strikethrough`, see 07).
