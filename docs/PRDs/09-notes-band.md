# Notes band: a node's note shown as a tinted band beneath it

| | |
|---|---|
| **Status** | Shipped |
| **Date** | 2026-06-15 |
| **Owner** | eren |
| **Related commits** | c29b433, 3c53e12 |
| **Related ADRs** | — |

## Problem / Context

A node's note was only visible by entering it. "Only way to see a note is to go into
it — show me other ways to relay that info as images." This was a pure
design-by-images iteration: the user reviewed dark HTML picker reports with real
tmux-captured PNG mockups and rejected directions until one landed.

## Goals

- Render a node's note as a tinted band directly under the node.
- When zoomed into the node, the band becomes the editable note surface.
- The tree line runs to the left of the note and curves into its corner to meet child nodes.

## Non-goals

- Showing the full note text inline at all times (the chosen option drops the note text in the glance view).

## Approach / Design

- A node's note renders as a tinted band directly under the node (`c29b433`).
- When zoomed in, the band becomes the editable surface (`3c53e12`); the duplicate note label shown while editing was dropped (see `b0b5b1d` in 08).
- The tree connector runs left of the note and curves into its corner so child nodes still join the tree visually. See `noteBandLines` in `pkg/tui/editor` (referenced by render and the temp panel).

## Decisions

- The note is surfaced as a tinted band, not hidden behind a zoom-only view.
- The chosen design (option 4 in the picker report) drops the note text in the glance band and fixes the tree lining: "the line should run left of the note and curve in its corner to meet child nodes."
- Design validated entirely via tmux PNG mockups reviewed in a browser, then a synthesized paste-back decision.

## UX / Behavior

- A node with a note shows a tinted band directly beneath it.
- Zooming into the node makes the band the editable note surface.
- The tree rail runs to the left of the band and curves into its corner to reach child nodes.

## Status & History

- 2026-06-15 `c29b433` show a node's note as a tinted band under it.
- 2026-06-15 `3c53e12` note band shows when zoomed in and is the edit surface.

### Pivot

Pure design-by-images iteration: multiple mockup directions were rejected in HTML
picker reports until option 4 (tinted band, dropped note text, corrected tree
lining) was chosen.
