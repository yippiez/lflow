# Node styling: per-node color/bold/italic/underline/strikethrough

| | |
|---|---|
| **Status** | Shipped |
| **Date** | 2026-06-14 |
| **Owner** | eren |
| **Related commits** | d2a84f0, 4b3b3a5, a962798 |
| **Related ADRs** | — |

## Problem / Context

Styling must be a per-node dataset attribute, not inline markup. The user explicitly
rejected per-character/range styling: "I fear it will cost too many bucks because
then I need to add characters, edit characters, and this can cause the color to
shift... markers have to be ignored in search, stdout has to clear them, it gets
messy — let's not do that." Inline `**bold**`/`*italic*` parsing shipped in
05-wysiwyg-rows but is removed here.

## Goals

- Per-node styling via `/color /bold /italic /underline /strikethrough`.
- Styling stored as a per-node attribute (`item.style`), not inline markers.
- Remove inline `**bold**`/`*italic*` markup parsing; asterisks render literally.

## Non-goals

- Per-character or per-range styling.
- Any inline markup that would leak into stored text, search, or export.

## Approach / Design

- Styling is a per-node attribute in `item.style` — comma-separated tokens like `bold,italic,color:blue` — backed by a `nodes.style` column (migration lm16). See `pkg/tui/editor/style.go`.
- The renderer folds the tokens into the row's SGR attributes (`pkg/tui/editor/render.go`).
- `/color` is an 8-swatch picker. `/bold /italic /underline /strikethrough` toggle their token.
- Inline `**bold**`/`*italic*` parsing removed (`4b3b3a5`); asterisks now render literally.

## Decisions

- Styling is a per-node dataset attribute, never inline markers. This is a durable, load-bearing rule (memory: lflow-styling-per-node).
- No per-character/range styling — it would shift colors, pollute search, and require stdout/export stripping.
- Asterisks are literal text.

## UX / Behavior

- `/color` opens an 8-swatch picker; `/bold`, `/italic`, `/underline`, `/strikethrough` toggle.
- Style applies to the whole node's text; the bullet still follows the selection (red) and date chips override background.
- `**`/`*` render literally.

## Status & History

- 2026-06-14 `d2a84f0` add `/color /bold /italic /underline` node styling.
- 2026-06-14 `4b3b3a5` drop inline `**bold**`/`*italic*` markup; style is per-node only.
- 2026-06-14 `a962798` add `/strikethrough` (same commit also makes date chips color background only, see 08-date-chips).

### Pivot

A reversal: inline `**bold**`/`*italic*` markup (shipped in 05) was removed. The
per-node model is the durable design; markers leaking into stored text was the
explicit anti-pattern.
