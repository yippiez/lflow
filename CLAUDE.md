# CLAUDE.md

lflow is a local-first terminal outline editor (Go + bubbletea). This file is the
quick-reference; **[AGENTS.md](AGENTS.md) is the full agent governance guide — read
it.**

## Record decisions

Append every non-trivial decision (autonomous or user-directed) to
**[docs/ADR.md](docs/ADR.md)** using the format documented there (title / Why /
When, newest at the bottom). Read it before you start so you don't re-litigate
settled choices.

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
- No emojis — plain Unicode symbols only (○ ◆ ▸ ● $ {} →). CLI output uses `→`/`·`.

## Key invariants (full list + rationale in AGENTS.md and docs/ADR.md)

- Inline scrollback only, never the alt-screen (lint-enforced).
- No markup leaks into stored text — styling/dates/links are per-node attrs or chips.
- Everything is a node; new types go in the registry (`pkg/tui/editor/registry.go`),
  no DB migration. Run output is ephemeral in-memory only (never persisted; a generic
  `NodeInternalData` JSON blob is planned, not implemented); binary → local file.
- Never auto-run runnable nodes (alt+r only). Never sync secrets, view-state, the
  Temporary Domain, or binary files. Secrets live in local config — Workflowy in
  `~/.lflow/settings.json`, Pi in `~/.pi/agent/settings.json` (a consolidated
  `~/.config/lflow/credentials.json` is planned, not yet built). Status bar is the
  last rendered line.

## Pointers

- **[AGENTS.md](AGENTS.md)** — full operating guide and invariants.
- **[docs/ADR.md](docs/ADR.md)** — decision log; append to it as you work.
- **[docs/PRDs/](docs/PRDs/)** — feature PRDs; copy `00-template.md` for new ones.
- **docs/SPECs/**, **docs/COMMANDS.md**, **docs/SELF_HOSTING.md** — specs and refs.
