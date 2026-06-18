# WYSIWYG rows: inline rendering, bullets/cursor look, links, time pills

| | |
|---|---|
| **Status** | Shipped |
| **Date** | 2026-06-12 |
| **Owner** | eren |
| **Related commits** | 20c2802, 1277498, 9b99acf, da5de04, 9d1fd70, 7290411, 6e37de3, 6383a85 |
| **Related ADRs** | — |

## Problem / Context

The editor should render WYSIWYG, matching the pi-prompt-chain look. The user has
precise visual demands: "the branches are also muted gray, cursor is white and
inverts color of text beneath it, only node becomes red." Links and dates should
feel native: pasting a node link mirrors it; time phrases should turn into pills.

## Goals

- Rows render WYSIWYG: muted-gray bullets and tree connectors, mirrors red `◆`, headings as yellow digit bullets `1 2 3`.
- Selection marks only the bullet (turns red); text never recolors for selection.
- Cursor is a reverse-video block that inverts the cell beneath it.
- `/copy_link` copies a node link; pasting a link mirrors that node; multiline paste fans out into nodes.
- Time phrases become date pills.
- The slash menu opens anywhere in a row; an empty finder lists everything recent-first.
- ctrl-arrow twins for every alt-arrow chord.

## Non-goals

- h1/h2 mode toggles (headings are digit-bullet styling, not modes).
- Recoloring row text on selection.

## Approach / Design

- WYSIWYG row renderer (`pkg/tui/editor/render.go`): inline bold/italic display, code blocks, quote bars, heading digit bullets (`20c2802`).
- Slash menu opens anywhere in a row (`1277498`); empty finder query lists everything recent-first (`9b99acf`).
- ctrl-arrow chords mirror the alt-arrow chords (`da5de04`).
- Node links: `/copy_link` copies, pasting a link mirrors, multiline paste fans out (`9d1fd70`).
- Time phrases convert to date pills (`7290411`); Turkish + English phrases both first-class (e.g. `now`, `11 şubat 2025 saat 15:20`).
- Look: muted-gray bullets, dark-red block cursor, text keeps its color (`6e37de3`).
- On quit, the styled outline stays in scrollback: bubbletea erases the final frame's last line, so the quit View ends with `\n` (`6383a85`).

## Decisions

- Selection is shown only by the bullet turning red; text never recolors. Cursor is a reverse-video block inverting the cell beneath it.
- Headings are yellow digit bullets, not an h1/h2 mode. "Change node circle so it's H1 or 1 instead of circle."
- Pasting a node link mirrors; pasting multiline text fans out into nodes.
- Turkish and English date phrases are both first-class.
- Alt-arrows for zoom/structure; ctrl-arrows added as twins.
- Quit View ends with `\n` so the styled outline survives in scrollback.

## UX / Behavior

- Bullets/connectors muted gray; mirror `◆` red; headings yellow digit `1`/`2`/`3`.
- Block cursor inverts the cell under it; selected row = red bullet only.
- `/copy_link` copies the node link; paste a link to mirror; multiline paste fans out.
- Time phrases auto-render as date pills.
- Slash menu opens at any caret position; empty finder lists everything recent-first.
- Every alt-arrow chord has a ctrl-arrow twin.

## Status & History

- 2026-06-12 `20c2802` WYSIWYG rows: inline bold/italic, code blocks, quote bars, heading digit bullets.
- 2026-06-12 `1277498` slash menu opens anywhere in a row.
- 2026-06-12 `9b99acf` empty finder query lists everything, recent first.
- 2026-06-12 `da5de04` ctrl-arrow twins for every alt-arrow chord.
- 2026-06-12 `9d1fd70` node links: `/copy_link`, paste mirrors, multiline paste fans out.
- 2026-06-12 `7290411` time phrases become date pills.
- 2026-06-12 `6e37de3` muted gray bullets, dark-red block cursor, text keeps its color.
- 2026-06-12 `6383a85` keep the styled outline in scrollback on every quit.

### Note

Inline `**bold**`/`*italic*` markup parsing shipped here was later removed in favor
of per-node styling, see 07-node-styling.
