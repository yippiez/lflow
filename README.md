![Lflow](assets/logo.png)
=========================

![Build Status](https://github.com/lflow/lflow/actions/workflows/ci.yml/badge.svg)

Lflow is a local-first terminal outline tool.

Your outline lives in **one SQLite file**. Every command works offline. Nodes are
bullets, headings, todos, code, quotes and mirrors; `lflow open "query"` drops you
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
lflow add "reading list"   # top-level (under the always-present root)

# Add children (positional text, or pipe stdin: one node per line)
lflow add --parent "reading list" "Designing Data-Intensive Applications"
make bench 2>&1 | lflow append "experiment results"

# Open the inline editor on the best match
lflow open "reading list"
lflow open                 # the whole outline

# Dump a subtree as JSON for scripting
lflow list                 # top-level nodes
lflow list "reading list" --format json | jq -r '.children[].name'
```

## The editor

`lflow open [node]` opens an inline editor. It draws in the terminal scrollback —
it never switches to the alternate screen — and quitting leaves the fully styled
outline behind in your history. Changes are saved on quit, and `ctrl+s` saves
explicitly.

Rows are muted gray; the selected row and its inline caret are red. Glyphs: `○`
bullet, `●` collapsed, `□` todo / `■` done, `◆` mirror, and headings show their
level digit `1` `2` `3` instead of a circle.

Rows render what they mean: `**bold**` and `*italic*` style their text and the
markers hide until the row is selected, code rows draw as a block, quote rows get
a `▎` bar, completed nodes strike through.

Time phrases convert into date pills: when the cursor row contains `now`, `bugün`,
`tomorrow`, `11 şubat 2025 saat 15:20`, `11 february 2025 at 15:20`, `2025-02-11`
or `11/02/2025`, the bottom bar offers it and `ctrl+t` replaces the phrase with a
`[[2025-02-11 15:20]]` pill rendered as a chip.

### Keys

Every alt+arrow chord also works as the ctrl+arrow twin, for terminals like
Windows Terminal that keep alt+arrows for pane navigation.

| Key | Action |
| --- | --- |
| `enter` | new sibling below |
| `tab` / `shift+tab` | indent / outdent |
| `ctrl+space` | fold / unfold |
| `ctrl+d` | delete the node and its subtree |
| `ctrl+t` | convert the detected time phrase into a date pill |
| `alt+shift+↑/↓` or `ctrl+shift+↑/↓` | move the node among siblings |
| `alt+→` or `ctrl+→` | zoom in, making the node the view root |
| `alt+←`, `alt+backspace` or `ctrl+←` | zoom out |
| `alt+↑/↓` or `ctrl+↑/↓` | collapse / expand the node |
| `ctrl+s` | save |
| `ctrl+q`, `ctrl+c` or `esc esc` | quit — saves and leaves the outline in scrollback |
| `↑ ↓ ← →` `home` `end` | move the cursor / caret |

Pasting works the way an outliner should: a multiline paste continues the current
row and fans the remaining lines out as siblings, and pasting a copied node link
(`/copy_link`) onto a row turns it into a mirror.

### Slash commands

Type `/` anywhere in a row to open the slash menu, then type to filter and `enter`
to run. The typed `/query` is stripped when a command runs; `esc`, or a query that
matches nothing, keeps it as plain text — which is also how you type a literal
slash.

| Command | Effect |
| --- | --- |
| `/mirror` | mirror another node here via the fuzzy finder |
| `/mirror_to` | mirror this node under another node |
| `/copy_link` | copy this node's link — paste it on another node to mirror |
| `/move_to` | move this node under another node |
| `/go` | jump the editor to another node |
| `/complete` | toggle done |
| `/h1` `/h2` `/h3` | make a heading |
| `/todo` | make a todo |
| `/code` | make a code node |
| `/quote` | make a quote |
| `/bullet` | back to a plain bullet |
| `/note` | edit this node's note, the body text under the bullet |

The fuzzy finder behind `/mirror`, `/move_to` and `/go` starts listing everything,
recent first, and narrows as you type.

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
