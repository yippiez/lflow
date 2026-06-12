![Lflow](assets/logo.png)
=========================

![Build Status](https://github.com/lflow/lflow/actions/workflows/ci.yml/badge.svg)

Lflow is a local-first terminal outline tool.

Your outline lives in **one SQLite file**. Every command works offline. Nodes are
bullets, headings, todos, code, quotes and mirrors; `lflow find "query"` drops you
into an inline terminal editor on the best match. Commands are one-shot and
pipe-friendly, so `make bench 2>&1 | lflow append "experiment results"` just works.

Device sync is optional and runs against a self-hostable `lflow-server`. Workflowy
is an optional integration (a mirror source, not a backend).

## Install / build

Lflow needs SQLite FTS5, so build with the `fts5` tag:

```sh
go build --tags fts5 ./pkg/cli
```

The Makefile wraps the release builds:

- `make version=0.1.0 build-cli` — build the CLI (use `make debug=true build-cli` for a dev build)
- `make version=0.1.0 build-server` — build the sync server
- `make version=0.1.0 build-server-docker` — build the server Docker image
- `make test` — run the CLI, API and e2e test suites

## Quick start

```sh
# Create a root node
lflow add --root "reading list"

# Add children (positional text, or pipe stdin: one node per line)
lflow add "reading list" "Designing Data-Intensive Applications"
make bench 2>&1 | lflow append "experiment results"

# Find a node and open the inline editor on the best match
lflow find "reading list"

# Dump a subtree as JSON for scripting
lflow list "reading list" --format json | jq -r '.children[].name'
```

## The editor

`lflow find <query>` and `lflow edit <node>` open an inline editor. It draws in the
terminal scrollback (it never switches to the alternate screen), so output stays in
your history when you quit. Changes are saved on quit, and `ctrl+s` saves explicitly.

Glyphs: `○` bullet, `●` collapsed, `□` todo / `■` done, `◆` mirror (local or workflowy).

### Keys

| Key | Action |
| --- | --- |
| `enter` | new sibling below |
| `tab` / `shift+tab` | indent / outdent |
| `ctrl+space` | fold / unfold |
| `ctrl+d` | delete the node and its subtree |
| `alt+shift+↑` / `alt+shift+↓` | move the node up / down among siblings |
| `alt+↓` | zoom in (make the node the view root) |
| `alt+↑` | zoom out |
| `ctrl+s` | save |
| `ctrl+q` or `esc esc` | quit (saves first) |
| `↑ ↓ ← →` `home` `end` | move the cursor / caret |

### Slash commands

Type `/` at the **start** of a row to open the slash menu, then type to filter and
`enter` to run:

| Command | Effect |
| --- | --- |
| `/mirror` | mirror another node here (fuzzy finder) |
| `/mirror_to` | mirror this node under another node |
| `/move_to` | move this node under another node |
| `/go` | jump the editor to another node |
| `/complete` | toggle done |
| `/h1` `/h2` `/h3` | make a heading |
| `/todo` | make a todo |
| `/code` | make a code node |
| `/quote` | make a quote |
| `/bullet` | back to a plain bullet |
| `/note` | edit this node's note (the body text under the bullet) |

## Device sync

Sync mirrors the local node tree to a self-hosted `lflow-server` using the USN-based
protocol adapted from dnote. Sync is optional; nothing leaves your machine until you
log in and run `lflow sync`.

```sh
lflow login                 # authenticate against the server
lflow sync                  # incremental push/pull
lflow sync --full           # full reconcile
lflow sync --dry-run        # show what would be pushed/pulled
lflow logout
```

Point the CLI at your server by setting `apiEndpoint` in `~/.config/lflow/lflowrc`,
or pass `--apiEndpoint` to `login`/`sync`. Conflict rule: a node edited locally
(dirty) wins and is pushed back; otherwise the server state wins. See
[SELF_HOSTING.md](SELF_HOSTING.md) for running the server.

## Workflowy

Workflowy is an optional integration, not a backend. You anchor a workflowy node
into your local tree; mirrored nodes render as `◆` in the editor. While the editor
is open, anchored mirrors stay fresh automatically in the background (visible
mirrors every 5 seconds, off-screen ones every minute, and immediately when one
first comes on screen) — local edits push out and remote edits appear in place.
Outside the editor, `lflow wf pull` / `push` reconcile on demand (each runs both
directions).

```sh
# Log in. For 2FA accounts, pass a sessionid directly instead of email/password:
lflow wf login
lflow wf login --session <sessionid>

# Anchor a workflowy node under a local node (default: a new root)
lflow wf mirror https://workflowy.com/#/abc123def456 --into "reading list"

# List mirrors
lflow wf list

# Reconcile (both directions either way)
lflow wf pull
lflow wf push

# Detach a mirror, keeping or dropping the local copy
lflow wf unmirror "reading list" --keep
lflow wf unmirror "reading list" --drop
```

Conflicts: **workflowy wins**. The overwritten local value is appended to
`wf-journal.log` (in lflow's data directory) so nothing is silently lost.

## Migrating from dnote

Lflow is a fork of dnote. The first time it runs against an existing dnote database
it converts the data automatically: each **book becomes a root heading node** and
each **note becomes a child node** (the note's first line becomes the node name, the
rest becomes the node's note). Converted rows resync from scratch because the
node-based server protocol is not compatible with the old book/note sync state.

## Commands

See [pkg/cli/COMMANDS.md](pkg/cli/COMMANDS.md) for the full command and flag
reference.

## License

Apache License 2.0.
</content>
</invoke>
