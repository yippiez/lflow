![Lflow](assets/logo.png)
=========================

![Build Status](https://github.com/lflow/lflow/actions/workflows/ci.yml/badge.svg)

lflow is a fork of [dnote](https://github.com/dnote/dnote) reworked into a local-first terminal outline editor: your whole tree lives in one SQLite file, every command is one-shot and pipe-friendly, and `lflow node open` drops you into an inline editor that draws in the terminal scrollback rather than the alternate screen. Nodes can be bullets, headings, todos, code, quotes and mirrors; device sync against a self-hostable server and a Workflowy integration are optional.

## Examples

```sh
# Build — lflow needs SQLite FTS5, so build with the fts5 tag
go build --tags fts5 ./pkg/cli

# Add nodes: positional text, or pipe stdin where every line becomes a node
lflow node add "reading list"
lflow node add --parent "reading list" "Designing Data-Intensive Applications"
make bench 2>&1 | lflow node add --parent "experiment results"

# Open the inline editor on the best match, or the whole outline
lflow node open "reading list"
lflow node open

# List nodes, or dump a subtree as JSON for scripting
lflow node list
lflow node list "reading list" --format json | jq -r '.children[].name'

# Sync to a self-hosted server, or reconcile a Workflowy mirror — both optional
lflow server sync
lflow wf pull
```

See [docs/COMMANDS.md](docs/COMMANDS.md) for the full command and flag reference, and [docs/SELF_HOSTING.md](docs/SELF_HOSTING.md) for running the sync server.

## License

Apache License 2.0.
