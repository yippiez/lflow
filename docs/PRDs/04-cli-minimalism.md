# CLI minimalism: grouped commands, light help, config-only settings

| | |
|---|---|
| **Status** | Shipped |
| **Date** | 2026-06-12 |
| **Owner** | eren |
| **Related commits** | 3fdfd59, b5ba7ec, 89c235b, 671e70b, f5437f1, 3cdc1be, 5a04053, f3945e8, 4de4101, 732dce3, e142a0d |
| **Related ADRs** | — |

## Problem / Context

This is a greenfield tool, so the CLI surface should be minimal and opinionated from
day one: "no command aliases at all." dnote's surface had overlapping commands and a
flag zoo; lflow should merge or delete anything redundant and keep settings that
describe the environment in the config file, not on the command line.

## Goals

- A grouped command tree: `lflow node open|list|add|move|remove|edit`, `lflow server …`, `lflow wf …`, `lflow export`, `lflow version`.
- No aliases, no help examples, no `help` subcommand (only `--help`).
- A custom light cobra help renderer.
- Settings that describe the environment (e.g. `dbPath`) live only in `~/.lflow/settings.json`.

## Non-goals

- Command aliases of any kind.
- Help examples or a `help` subcommand.
- Environment flags like `--dbPath`.
- Overlapping commands (merge or delete instead).

## Approach / Design

- Always-present root so `add` auto-targets root; `--parent` defaults to root; `open` command added with arrow-style output (`3fdfd59`).
- `find` and `edit` folded into `open` (`b5ba7ec`); `append` folded into `add` (as `add --parent`); complete/uncomplete folded into `edit --state`.
- `--all`/`--completed` flags removed: completed nodes always resolve and list (`89c235b`).
- Grouped command tree with full names, no aliases, no examples (`671e70b`); `open`/`list` join the node group (`f3945e8`).
- Custom help renderer: no title line, starts at usage/commands, only descriptions muted gray, flags aligned at the command column, reachable only via `--help` (`f5437f1`, `3cdc1be`, `5a04053`).
- `dbPath` moved into the config file; the flag is gone (`4de4101`).

## Decisions

- No aliases, no help examples, no `help` subcommand; full command names only.
- Output uses `·` separators and `→` arrows, never parenthesized explanations and never tick emoji. "DO NOT USE EMOJIS LIKE TICK, only arrows."
- Settings describing the environment belong in the config file (`~/.lflow/settings.json`), not the command line.
- When two commands/flags overlap, merge or delete them.
- Root is always-present so `add` auto-targets it; `open` with no arg opens root.

## UX / Behavior

- `lflow node open|list|add|move|remove|edit`, `lflow server …`, `lflow export`, `lflow version`.
- `--help` only; help starts at usage/commands, descriptions muted gray, flags aligned at the command column.
- One-shot output: `→ … · …`, no emoji ticks.

## Status & History

- 2026-06-12 `3fdfd59` always-present root, `--parent` default, `open` command, arrow output style.
- 2026-06-12 `b5ba7ec` fold find and edit into open.
- 2026-06-12 `89c235b` completed nodes always resolve and list; drop `--all`/`--completed`.
- 2026-06-12 `671e70b` node and server command groups; full names, no aliases, no examples.
- 2026-06-12 `f5437f1` colored help, reachable only through `--help`.
- 2026-06-12 `3cdc1be` lighter help, starts at usage, only descriptions colored.
- 2026-06-12 `5a04053` help groups start at commands; flags align with the command column.
- 2026-06-12 `f3945e8` open and list join the node group; append folds into add.
- 2026-06-12 `4de4101` dbPath moves into the config file; the flag is gone.
- 2026-06-12 `732dce3`, `e142a0d` docs: command reference for the simplified surface.
