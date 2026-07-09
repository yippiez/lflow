---
name: lflow
description: Work inside the lflow terminal outline app — query and edit the outline with the lflow CLI, answer with inline chips, and write, install or use NodeMods (custom node types). Use when responding to @mentions inside lflow, or whenever a task involves the user's nodes, mods, or lflow commands.
---

# lflow

You are running inside lflow, a terminal outline editor. Everything is a node
in one tree; a conversation thread is a node's subtree, and your replies land
back in the outline as nodes.

## Chips — how to speak

Embed these tokens anywhere in a reply; each renders as a structured chip:

- `{{cmd:ls -la}}` — a runnable shell command; the user runs it in place with
  alt+r. When asked for a command, answer with a cmd chip, not prose.
- `{{path:/etc/hosts}}` — a file or directory path.
- `{{link:label|https://example.com}}` — a link (label optional:
  `{{link:https://example.com}}`).
- `#tags` and `YYYY-MM-DD` dates become chips automatically — write them plainly.

Never wrap a chip token in quotes or backticks.

## The lflow CLI — how to look around

The outline is queryable from the shell. Read [cli.md](cli.md) before
composing commands. The core moves:

```
lflow node grep <text>              # find nodes → id, name, kids, type table
lflow node list <id|text>           # read a subtree (md; --format json for structure)
lflow node add <text> --parent <id> # create a node
lflow node edit <id> --name/--type/--state/--note ...
```

Node references accept an id, an id prefix, or fuzzy text — grep first when
unsure, and add `--strict` to see the candidates instead of acting on the
best match.

## NodeMods — custom node types

A mod is a JS file that registers a node type (glyph, prefix, render, a
runnable alt+r hook) or a chip kind. They live in `~/.config/lflow/mods`:

- `<key>.js` — a flat mod (yours or the user's); `<key>/` — a git-installed
  mod with a `mod.json` manifest. A `.disabled` suffix on either = off.
- List what's installed: `{{cmd:ls ~/.config/lflow/mods}}`. Read a mod's JS
  before recommending or using its type.
- To write a new mod: read [mods.md](mods.md) for the API, start from the
  closest file in [examples/](examples/), write `<key>.js` into the mods dir
  yourself. The app reloads the directory when your turn ends — no restart.
- External mods install with `lflow node install <git-url>` (see mods.md for
  the repo shape).

A node whose mod is removed or disabled stays a normal node and renders as a
plain bullet — mods are never load-bearing for the user's data.
