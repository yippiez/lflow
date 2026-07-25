# Coding Agent Instructions

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

## Rules

- Structural invariants live as `// WARNING (invariant):` comments next to the
  code they govern — grep that before changing anything load-bearing.
- New node type: add a `TypeXxx` constant in `database/node.go`, one entry in
  `editor/registry.go`, behavior in its own file. No DB change needed; unknown
  types fall back to bullets.
- Runnable types run on alt+r only, never automatically; run output is
  ephemeral — never persisted or synced.
- Failures go through `m.errorFlash(msg)`, never `m.flash` directly.
- No emojis — plain Unicode symbols only (○ ◆ ▸ ● $ →); CLI output uses `→`/`·`.

## Zotero citations

`pkg/tui/zotero` reads the LOCAL Zotero library — the desktop apps
`zotero.sqlite` — and never writes: it copies the file (plus any `-wal`) to a
throwaway snapshot and reads that, so a running Zotero is never disturbed. The
data directory is found via `LFLOW_ZOTERO_DIR`, `ZOTERO_DATA_DIR`, the
`dataDir` in a Zotero profiles `prefs.js`, `<home>/Zotero`, and — for lflow in
WSL with Zotero on the Windows side — `/mnt/<drive>/Users/<user>/Zotero`. The
whole library loads once per session (lazily, re-read when its mtime moves) and
is searched in process, because the picker filters on every keystroke.

In the editor (`editor/zotero.go`) a citation is a **chip**, not a node type:
kind `zotero`, value = Zoteros own `zotero://select/…` URI (so the stored value
IS the local-open target), label = the compact author-year form. `@@` — or
`/cite`, or `/insert` → cite — opens the library picker; `alt+g` opens the paper
where `/settings` → "Zotero opens" points (local app or zotero.org), `alt+o`
opens the other one, and the chip carries the same target as an OSC 8 hyperlink
so a terminal ctrl+click lands there too. The local hop goes through
`browser.OpenApp`, which prefers the Windows shell under WSL because that is
where the `zotero://` handler is registered. The personal web-library URL needs
the account name: taken from the librarys own account settings, else
`credentials.json`, else the entrys DOI.

Remaining doc-level rules:

- Never auto-run runnable nodes (alt+r only) — an agentic coding session chip is
  opened by that same key and by nothing else.
- Secrets live in local config — Pi in `~/.pi/agent/settings.json`, service keys
  in `~/.config/lflow/credentials.json` (e.g. `{"workflowy":{"api_key":"…"},
  "zotero":{"username":"…"}}`). Never synced, never written into the DB.
