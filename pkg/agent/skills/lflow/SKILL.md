---
name: lflow
description: Work inside the lflow terminal outline app — query and edit the outline with the lflow CLI and answer with inline chips. Use when responding to @mentions inside lflow, or whenever a task involves the user's nodes or lflow commands.
---

# lflow

You are running inside lflow, a terminal outline editor. Everything is a node
in one tree. A conversation thread hands you the mentioned node's parent (one
level above, for context), the node, and everything beneath them; your replies
land back in the outline as nodes. That slice is all you are given — search
the rest of the outline yourself with the CLI below whenever it would help.

## Chips — how to speak

Embed these tokens anywhere in a reply; each renders as a structured chip:

- `{{cmd:ls -la}}` — a runnable shell command; the user runs it in place with
  alt+r. When asked for a command, answer with a cmd chip, not prose.
- `{{path:/etc/hosts}}` — a file or directory path.
- `{{link:label|https://example.com}}` — a link (label optional:
  `{{link:https://example.com}}`).
- `#tags` and `YYYY-MM-DD` dates become chips automatically — write them plainly.

Never wrap a chip token in quotes or backticks.

## Attachments — special nodes under a reply

A reply is one short comment plus optional **attachments**: typed child nodes
(not conversation bullets) for code, images, json, quotes, logs, runnable
shell, etc. Prefer an attachment over stuffing a payload into the comment.

Inline (body must not contain `}` — use the block form when it does):

- `{{attach:bash|go test ./...}}` — child with a runnable `$` chip
- `{{attach:image|caption}}` or `{{attach:image|/abs/path.png|caption}}`
- `{{attach:quote|ship when green}}`

Block form (multi-line or braced bodies):

```
{{attach:code}}
package main
func main() {}
{{/attach}}

{{attach:json}}
{"env": "prod"}
{{/attach}}
```

Types: `code`, `image`, `bash`, `json`, `quote`, `log`, `todo`, `h1`–`h3`,
`query`, or any other node type key.

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

## @mention on a todo

When the turn's `<instructions>` name a host todo id, that @chip sits on an
incomplete todo. After the work succeeds this turn, shell-run the given
`lflow node edit <id> --state complete` yourself (tool call, before final
reply text). Skip only on PASS. Do not complete early, leave it for later,
paste the command into the reply, or narrate the completion.
