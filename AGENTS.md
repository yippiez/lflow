# AGENTS.md — operating guide for AI agents working in lflow

lflow is a local-first **terminal outline editor** (Go + bubbletea), forked from
dnote into a keyboard-driven outliner. The whole tree lives in one SQLite file;
every CLI command is one-shot and pipe-friendly, and `lflow node open` drops into
an inline editor that draws in the terminal **scrollback**, never the alt-screen.
Everything is a node with a free-string `type`, and node types are an extensible
registry — one descriptor per type in `pkg/tui/editor/registry.go`.

## Build / test / run

- Always build with the fts5 tag (required by SQLite FTS5 node triggers):
  `go build --tags fts5 ./pkg/tui`
- After every change, install the dev binary so the user can test it:
  `go build --tags fts5 -ldflags "-X main.versionTag=0.1.0-dev" -o ~/.local/bin/lflow ./pkg/tui`
- Test before installing: `go test --tags fts5 ./...`
- Test in isolation against a throwaway HOME/XDG + seeded DB — **never** the real
  outline at `~/.local/share/lflow/lflow.db`. SQLite surgery goes through a
  `-tags fts5` Go program (the sqlite3 CLI lacks fts5).
- Commit each logical change as its own `label: description` commit; push as you go.
  Branches are named `label/explanation` — see [CONTRIBUTING.md](CONTRIBUTING.md).
- No emojis — plain Unicode symbols only (○ ◆ ▸ ● $ {} →). CLI output uses `→`/`·`.

## Adding new nodes

`nodes.type` is a free string, so a new type needs **no DB migration** and no
per-feature column — and no scattered `switch typ`:

1. Add a `TypeXxx` constant in `pkg/tui/database/node.go` and to `ValidTypes`.
2. Add one `nodeType` entry to the `nodeTypes` slice in
   `pkg/tui/editor/registry.go` — that slice drives the `/type` picker, and the
   field doc-comments there list every hook (`sign`, `glyph`, `render`,
   `inlineEditable`, `tempOnly`, `run` on alt+r, `expand`/`view` on alt+e).
3. Put the behavior in its own `pkg/tui/editor/<type>.go` (see `json.go`,
   `bash.go`, `voice.go`, `worker.go`). A rich alt+e editor implements the
   stateless `nodeView` interface, keeping per-node state in `m.nodeStore(it.uuid)`.

Then build/install with the fts5 tag. Runnable types execute on alt+r only (never
auto-run) and their output is ephemeral — never persisted or synced.

## Key invariants

The structural invariants now live as `// WARNING (invariant):` comments next to the
code they govern — grep `WARNING (invariant)`. They cover: inline scrollback / no
alt-screen, no markup in stored text, the node-type registry (no DB migration),
ephemeral run output, sync exclusions (secrets / view-state / Temporary Domain /
binary), and the status bar being the last rendered line.

Remaining doc-level rules:

- Never auto-run runnable nodes (alt+r only).
- Secrets live in local config — Pi in `~/.pi/agent/settings.json` (a consolidated
  `~/.config/lflow/credentials.json` is planned, not yet built).
