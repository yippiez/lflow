# Repo restructure: trim to an outliner, package regroup

| | |
|---|---|
| **Status** | Shipped |
| **Date** | 2026-06-14 |
| **Owner** | eren |
| **Related commits** | 74fc77e, 0c2d11b, 2e32ed8, 8ad5cea, f84c9d4, c873b4b, df9cb83, df1d599, 876caeb, ab1dd92, c12ab25, fd76b67, 03085af |
| **Related ADRs** | — |

## Problem / Context

Now that lflow is a focused local outliner, the inherited dnote machinery is dead
weight. Shed the fork baggage and regroup packages around the new shape. Design
artifacts should live in `/tmp/lflow-design/`, not the repo: "the design report and
ADR should not be here, they should be in tmp."

## Goals

- Strip inherited dnote machinery: watcher, makefile, docker host files, web assets, GitHub templates, non-essential scripts.
- Move markdown docs under `docs/`.
- Regroup shared packages; rename the `cli` package to `tui`.
- Drop the Apache license headers and the script that re-added them.
- Trim the README.

## Non-goals

- Keeping any dnote-specific runtime machinery not used by the outliner.
- Storing design reports/ADRs in the repo (they live in `/tmp/lflow-design/`).

## Approach / Design

- Remove: watcher (`74fc77e`), scripts except install.sh (`0c2d11b`), GitHub templates (`2e32ed8`), docker host files (`8ad5cea`), makefile (`f84c9d4`), assets (`c873b4b`).
- Move markdown files to `docs/` (`df9cb83`); group shared packages (`df1d599`); rename the `cli` package to `tui` (`876caeb`).
- Drop the Apache license header from source files (`ab1dd92`) and remove `license.sh` so the dropped headers stay dropped (`c12ab25`).
- Trim README to a single paragraph plus examples (`fd76b67`); merge PR #1 (`03085af`).

## Decisions

- Shed fork baggage now that lflow is a focused local outliner.
- The package that holds the editor/CLI is named `tui`.
- Apache headers are gone for good (the re-adding script is deleted too).
- Design reports/ADRs/images live in `/tmp/lflow-design/`, never in the repo — only the capture tooling stays in-repo.

## UX / Behavior

- No user-facing UX change; this is structure and hygiene.

## Status & History

- 2026-06-14 `74fc77e` remove watcher.
- 2026-06-14 `0c2d11b` remove scripts other than install.sh.
- 2026-06-14 `2e32ed8` remove github templates.
- 2026-06-14 `8ad5cea` remove docker host files.
- 2026-06-14 `f84c9d4` remove makefile.
- 2026-06-14 `c873b4b` remove assets.
- 2026-06-14 `df9cb83` move markdown files to docs.
- 2026-06-14 `df1d599` group shared packages.
- 2026-06-14 `876caeb` rename cli package to tui.
- 2026-06-14 `ab1dd92` drop the Apache license header.
- 2026-06-14 `c12ab25` remove license.sh.
- 2026-06-14 `fd76b67` trim README.
- 2026-06-14 `03085af` merge PR #1.

### Note

This epoch is dated 2026-06-14 (interleaved with editor-hardening) but is placed last
as a distinct structural concern.
