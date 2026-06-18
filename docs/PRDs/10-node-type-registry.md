# Node-type registry: one descriptor per type; layout to type rename

| | |
|---|---|
| **Status** | Shipped |
| **Date** | 2026-06-17 |
| **Owner** | eren |
| **Related commits** | 822f6b7, 786330a, 4977af4, f6e75e2, 205880c |
| **Related ADRs** | — |

## Problem / Context

Node behavior was scattered across many switches: `glyphFor`/`renderBody`
(render.go), `typeOrder`/`typeLabels` (editor.go), the markdown export switch
(outline.go) and `ValidTypes` (node.go). The user wants many more node types and
made extensibility a hard requirement: "I want to add more node types in the future,
so node type being extensible is really important to me." The "layout" to "type"
rename was the window: "do it while there are only 7 types. Wait, and every future
type pays the tax."

## Goals

- Replace the scattered `switch it.typ` logic with a single `nodeType` descriptor table.
- `typeOf(key)` lookup that falls back to bullets for unknown keys.
- Port the existing types with zero visual change.
- Rename the property "layout" to "type" everywhere.

## Non-goals

- Any visual change to existing types in this epoch.
- A DB migration for new types (free-string `type` means none is needed).

## Approach / Design

- A single `nodeType` descriptor (`pkg/tui/editor/registry.go`) with fields: `key, label, sign, glyph, render, renderM, inlineEditable, expand, run`. The `/type` picker order and labels derive from this one table.
- Cross-cutting concerns stay where they belong: the mirror `◆` and collapsed `●` glyphs stay in `glyphFor` (they apply to every type); legacy display attributes (heading bold, quote bar, code background) stay in `renderBody`.
- A rich type sets `render`/`renderM` for full inline control (json, voice); a read-only inline type sets `inlineEditable=false`; an alt+e type sets `expand`; a runnable type sets `run`.
- `typeOf(key)` returns the descriptor; unknown keys fall back to bullets.
- "layout" renamed to "type" across the codebase; styling unified into `/style` and `/type` pickers (`822f6b7`); a leftover `Layout` field reference fixed in tests (`4977af4`).
- Design spec in `docs/SPECs` (`f6e75e2`) and build plan (`205880c`).

## Decisions

- One descriptor per type is the single extension seam; ideally one self-registering file per type so core files never change again.
- Everything-is-a-node with a free-string `nodes.type` means new types need no DB migration.
- No emoji: every sign/glyph is a simple Unicode text symbol; avoid the media-control glyphs.
- The 8 existing types ported with zero visual change; the user asked to "show me both cases as code" before committing.

## UX / Behavior

- `/type` picker lists types in registry order with their labels.
- Existing types (Bullet, Todo, Heading 1/2/3, Code, Quote, JSON) look identical to before.
- The property is called "type" everywhere (formerly "layout").

## Status & History

- 2026-06-17 `822f6b7` rename node "layout" to "type"; unify styling into `/style` & `/type` pickers.
- 2026-06-17 `786330a` NodeType registry — types are one descriptor.
- 2026-06-17 `4977af4` fix leftover `Layout` field reference (renamed to Type).
- 2026-06-16/17 `f6e75e2` runnable-nodes design spec; `205880c` build plan.
