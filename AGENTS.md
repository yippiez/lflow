# lflow

Local-first terminal outline editor (Go + bubbletea). One SQLite file, owned by
a single daemon; every CLI command is a one-shot client of it, and `lflow node
open` is an inline editor that draws in terminal scrollback (never the
alt-screen) and live-syncs with other clients. Everything is a node with a
free-string `type`.

## Layout

- `pkg/tui` — main package, CLI commands
- `pkg/tui/editor` — the editor; `registry.go` is the node-type registry (one
  descriptor per type, every hook doc-commented there); pluggable types are one
  file each in `editor/nodes/`, registered via `editor.RegisterNodePlugin`
- `pkg/tui/database` — SQLite layer; `schema.sql` is the canonical schema,
  applied only to fresh DBs — **no migrations**, a schema change is an edit to
  that file
- `pkg/tui/daemon` / `client` / `wire` — client-server plumbing, ndjson over a
  unix socket; `LFLOW_NO_DAEMON=1` opens the DB file directly (tests, surgery)
- `pkg/e2e` — bash/tmux end-to-end tests; `rules/` — ast-grep lint rules

## Build / verify / ship

- Always build with the fts5 tag: `go build --tags fts5 ./pkg/tui`
- Verify: `go test --tags fts5 ./...`, `ast-grep scan`, `scripts/test.sh`
  (e2e). Tests never touch the real outline — throwaway HOME + `LFLOW_NO_DAEMON=1`.
- After every change, install the dev binary:
  `go build --tags fts5 -ldflags "-X main.versionTag=0.1.0-dev" -o ~/.local/bin/lflow ./pkg/tui`
- Trunk-based: commit each logical change straight to `main` as
  `label: description` (labels: `editor`, `db`, `daemon`, `docs`, …) and push
  as you go. No feature branches, no PRs.
- Every visible change ships a short demo video — the recipe is the `/demo`
  command (`.opencode/command/demo.md`).

## Rules

- Structural invariants live as `// WARNING (invariant):` comments next to the
  code they govern — grep that before changing anything load-bearing.
- New node type: add a `TypeXxx` constant in `database/node.go`, one entry in
  `editor/registry.go`, behavior in its own file. No DB change needed; unknown
  types fall back to bullets.
- Runnable types run on alt+r only, never automatically; run output is
  ephemeral — never persisted or synced.
- Failures go through `m.errorFlash(msg)`, never `m.flash` directly.
- Secrets live in local config (`~/.config/lflow/credentials.json`,
  `~/.pi/agent/settings.json`) — never in the DB, never synced.
- No emojis — plain Unicode symbols only (○ ◆ ▸ ● $ →); CLI output uses `→`/`·`.
