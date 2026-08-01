---
name: lflow
description: Query and edit the user's lflow terminal outline with the lflow CLI.
---

# lflow

lflow stores an outline as nodes in one tree. Read [cli.md](cli.md) before
composing commands. Search before acting when a node reference is ambiguous.

```sh
lflow node grep <text>
lflow node list <id|text>
lflow node list <id|text> --format json
lflow node add <text> --parent <id>
lflow node edit <id> --name <text>
```

Node references accept a UUID, UUID prefix, or fuzzy text. Use `--strict` to
inspect candidates instead of acting on the best fuzzy match.
