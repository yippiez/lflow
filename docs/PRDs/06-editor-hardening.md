# Editor hardening: wrapping, cursor, resize, paste fixes

| | |
|---|---|
| **Status** | Shipped |
| **Date** | 2026-06-13 .. 2026-06-14 |
| **Owner** | eren |
| **Related commits** | 3ff4245, 038b1fc, 2f95574, 1de3bf1, 2933fa3, 01e0ced, 8e8972c, f0579d1, b305576, 599aa87, a47abe7, 4c3f2c4, 2e95cf8, 18f00aa, 17b0d61, 4f363d5, 7860e46, 9ae9363 |
| **Related ADRs** | — |

## Problem / Context

The editor truncated long rows instead of wrapping, and the cursor/viewport math
broke under resize storms, control-character pastes, and narrow widths. The user
asked for hostile, automated break-testing: "make sure to use tmux to use the UI as
a user would — ultracode opus agents that try to break it like I do, mess with it
and report me on the results, cap tokens though." The long fix tail below is the
result of that adversarial pass.

## Goals

- Soft-wrap long rows with a hanging indent and continuous tree rails instead of truncating.
- Line-aware cursor and viewport so wrapped rows can't push the cursor off-screen.
- Clean redraw under resize storms and shrink-then-grow cycles.
- Sanitize pasted CRLF and control characters.
- Cross-node navigation: left/right cross boundaries, backspace merges up, ctrl+left/right jump by word, alt+left walks the ancestry breadcrumb.
- `/undo`, `/go` hides empty nodes.
- A tmux-driven e2e suite and ast-grep design-invariant lint rules.

## Non-goals

- New features beyond correctness/navigation (this is a hardening pass).

## Approach / Design

- Wrapping: long rows soft-wrap with a hanging indent; tree connectors stay continuous across wrapped rows; grapheme clusters count as one width unit; tab width accounted for; very narrow widths handled (`038b1fc`, `2f95574`, `a563bb8`, `8bb51b7`, `5e3b378`, `7bdbcf1`, `73bb36b`).
- Cursor/viewport: up/down walk a wrapped node's visual lines first; home/end move within the visual line; block cursor doesn't bleed across a soft-wrap or onto a blank continuation line; selected row kept on screen at tiny heights (`23acb67`, `cb182f6`, `5159de1`, `f066571`, `da1f72d`, `ae1a4d3`, `42822ff`).
- Resize: clear stale cells / line tails on shrink and shrink-then-grow; single clean frame after a resize storm; no duplicate rows; clear stale menu lines when the terminal narrows with the slash menu open (`f592141`, `ff77128`, `1de3bf1`, `b73c818`, `2933fa3`).
- Paste sanitizing: CRLF no longer creates ghost nodes; control chars stripped; lines that sanitize to empty are dropped; note-mode paste sanitized; never emit raw control bytes from stored content on render (`01e0ced`, `8e8972c`, `f0579d1`, `b305576`, `599aa87`).
- Editing/navigation: enter splits the node at the caret and pushes children down; left/right cross node boundaries; backspace merges up; ctrl+left/right jump by word; alt+left walks up the ancestry breadcrumb; `/undo`; `/go` hides empty nodes; mirror keypress logic consolidated into `mirrorContext` (`a47abe7`, `4c3f2c4`, `2e95cf8`, `18f00aa`, `17b0d61`, `4f363d5`).
- Tooling: tmux-driven e2e suite (`7860e46`); ast-grep design-invariant lint harness (`9ae9363`).

## Decisions

- Bug-driven development from hostile multi-agent tmux runs; each fix lands as its own commit with an e2e test.
- ast-grep lint rules encode design invariants: no alt-screen, no direct `sql.Open`, no `fmt.Print` in libs, no `panic` in cmd.
- Cursor/merge behaviors specified by concrete examples (e.g. "pressing left here places it at end of Word 1"; "backspace here merges B into A").

## UX / Behavior

- Long rows wrap with a hanging indent; tree rails stay continuous.
- left/right cross node boundaries; backspace at start merges up; enter splits at caret.
- ctrl+left/right jump by word; alt+left walks up the breadcrumb.
- `/undo` reverts; `/go` hides empty nodes.
- Resize and paste never corrupt the frame or insert ghost nodes.

## Status & History

- 2026-06-13 `3ff4245` gray connectors, inverting block cursor, leaf zoom, delete confirm.
- 2026-06-13 `038b1fc` long rows wrap instead of truncating; followed by the wrap/cursor/resize/paste fix tail (`2f95574` .. `f592141`).
- 2026-06-13 `01e0ced`, `8e8972c`, `f0579d1`, `b305576`, `599aa87` CRLF/control-char paste sanitizing.
- 2026-06-14 `a47abe7`, `4c3f2c4`, `2e95cf8` enter splits at caret; mirror keypress consolidated.
- 2026-06-14 `18f00aa` cross-node left/right, backspace merges up, `/undo`, `/go`, breadcrumb + alt-left walks up.
- 2026-06-14 `17b0d61`, `4f363d5` ctrl+left/right jump by word.
- 2026-06-14 `7860e46` tmux-driven editor e2e suite.
- 2026-06-12 `9ae9363` ast-grep design-invariant lint harness.

### Note

This epoch interleaves with the mirror fixes in 03-mirrors-and-workflowy
(`ea4dac8`, `8f70a31`, `f3fbede`, `20e4b09`, `53cf6b5`, `6b83226`).
