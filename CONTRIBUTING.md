# Contributing to lflow

See [AGENTS.md](AGENTS.md) for the build / test / run conventions and the
structural invariants. This file covers how branches and commits are named.

## Branch naming

Branches are named `label/explanation`:

- **`label`** — the change category, drawn from the same vocabulary as commit
  labels: `editor`, `db`, `agent`, `docs`, `server`, etc.
- **`explanation`** — a short kebab-case summary of the work.

Examples:

```
docs/agents-and-contributing
editor/persist-bash-run-output
db/revive-tombstoned-rows
```

Pick the `label` that matches the dominant change; if a branch spans several,
choose the most representative one.

## Commit messages

Commit each logical change as its own `label: description` commit, using the same
`label` vocabulary as branches, and push as you go — do not batch work at the end.

```
docs: add CONTRIBUTING.md explaining branch and commit naming
editor: persist bash run output across restarts via a local cache
```

## Before you push

- Build and test with the fts5 tag (see [AGENTS.md](AGENTS.md)).
- No emojis — plain Unicode symbols only.
</content>
</invoke>
