# Temporary Domain: ephemeral scratch outline region

| | |
|---|---|
| **Status** | Shipped |
| **Date** | 2026-06-18 |
| **Owner** | eren |
| **Related commits** | 49e7a7d, b3a7ccb, acaed3f, 15305e9, f8172d0 |
| **Related ADRs** | — |

## Problem / Context

The user wants "a special place for temporary nodes — acts exactly like the normal
outline but is below the footer and doesn't survive closing the app. If you want some
temporary notes or agents while working, you place them there."

## Goals

- A second outline region below the footer that behaves exactly like the main outline.
- Ephemeral: in-memory only, never persisted or synced.
- Marked by a plain muted-gray "Temporary Domain" divider.

## Non-goals

- Persisting or syncing the scratch content.
- A fancy dashed-box divider affordance (tried, then dropped to plain text).
- An alt+t window-swap as the access path (changed to go-down navigation).

## Approach / Design

- `pkg/tui/editor/temp.go`: a second `tree` with a nil db, so `save()` is a no-op and it never persists or syncs. `ensureTempTree` keeps at least one node so the panel is never blank.
- Always-visible panel anchored at the bottom of every page, above the status bar (`acaed3f`). `tempPanelBudget` sizes it: a small glance strip when idle (up to 6 lines), up to two-thirds of the body when focused, always leaving at least one line for the main outline.
- Focus: pressing Down past the last node of the main outline moves focus into it (`enterTemp` stashes main state); Up at its top returns (`exitTemp`). No shortcut, no special divider behavior.
- `readonlyRegionLines` renders the idle panel as a static region; `dashed` swaps the `◌` glyph for non-mirror nodes (the Temporary Domain look). Divider sits after notes with no gap (`15305e9`). Enter on an expanded parent creates its first child (`f8172d0`).

## Decisions

- The Temporary Domain is in-memory only — never persisted or synced (one of the "what never syncs" invariants).
- The divider is plain `Temporary Domain` text, muted gray — not a dashed box. "I don't like it — make it plain `Temporary Domain` just text."
- Access is via go-down navigation, not an alt+t window swap.
- It is an always-visible panel, not a swapped window.

## UX / Behavior

- An always-visible panel below the main outline, above the status bar, marked by a plain muted-gray "Temporary Domain" divider.
- Press Down at the bottom of the main outline to focus it; Up at its top returns.
- Edits behave exactly like the main outline; non-mirror nodes use the `◌` glyph.
- Focused, it grows up to two-thirds of the body; idle, it stays a small glance strip; content is gone on quit.

## Status & History

- 2026-06-18 `49e7a7d` Temporary Domain — ephemeral scratch outline (initially alt+t).
- 2026-06-18 `b3a7ccb` Temporary Domain via go-down (same commit adds worker status bar, see 16).
- 2026-06-18 `acaed3f` always-visible panel, not a window.
- 2026-06-18 `15305e9` temp panel — divider after notes, no gap.
- 2026-06-18 `f8172d0` enter on expanded parent creates first child.

### Pivots

- The divider went from a fancy dashed-box affordance to plain text.
- Access changed from an `alt+t` window-swap to go-down navigation.
- The region went from a swapped "window" to an always-visible panel below the divider.
