# Keyword animation: ultracode / ultraloop shimmer

| | |
|---|---|
| **Status** | Shipped |
| **Date** | 2026-06-17 |
| **Owner** | eren |
| **Related commits** | 35e4fcc |
| **Related ADRs** | — |

## Problem / Context

The user wants two magic words to animate as they are typed. "Two special words:
ultrathink and ultracode. For ultrathink make each letter a different color,
constantly shifting, with a shining white shim moving inside; for ultracode color it
purple with the white shim. ultrathink maximizes the LLM's thinking level; ultracode
orchestrates other agents." Later refinements: pastel/light colors for ultrathink, a
pulsing animation, and "ultraloop also — this word can be at any place of the
sentence."

The orchestration behavior behind the keywords is deferred; this epoch ships only
the visual animation.

## Goals

- Animate the keywords `ultracode` / `ultraloop` (and earlier `ultrathink`) anywhere in a node as they are typed.
- ultracode: purple with a moving shine; ultraloop/ultrathink: their own palettes with a sliding highlight; a pulsing feel.
- Animation never writes a marker into the stored node name.

## Non-goals

- The orchestration behavior the keywords stand for (staged pipeline / self-refinement loop) — deferred to a later layer (see worker, 16).

## Approach / Design

- Render-time only (`pkg/tui/editor/anim.go`): a global `animFrame` clock advanced by an animation tick; rendering reads it. Both run on bubbletea's single event-loop goroutine, so no synchronization is needed.
- Detection is purely render-time on the lowercased row text, so nothing is stored — it never writes a marker into the name and never violates the per-node-style rule.
- Each keyword has a base color and a lighter peak tint (never white) so the word keeps its color while a soft highlight band slides across it (`shineColorAt`, `markKeywords`).
- The animation tick runs only while a magic keyword is visible on screen (`hasMagicKeyword`, `startAnim`).

## Decisions

- Animation is render-time and storage-free — no marker leaks into the node name (consistent with the no-markup-leaks rule, see 07/08).
- The highlight never goes full white; the word keeps its identity color.
- The tick runs only when a keyword is on screen, to avoid idle redraws.
- The orchestration semantics behind the keywords are deferred.

## UX / Behavior

- Typing `ultracode` makes the word purple with a sliding shine band.
- Typing `ultraloop` animates with a red palette; the word can appear anywhere in the sentence.
- (Earlier) `ultrathink` rendered with pastel/light shifting colors.

## Status & History

- 2026-06-17 `35e4fcc` animate ultracode / ultraloop keywords as you type.

### Note

The build history was uncertain about this hash; confirmed via
`git log --oneline | grep ultracode` as `35e4fcc` (sits between `f6e75e2` and
`5a90f18`).
