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

`pkg/tui/zotero` reads the LOCAL Zotero library — the desktop app's
`zotero.sqlite` — and never writes: it copies the file (plus any `-wal`) to a
throwaway snapshot and reads that, so a running Zotero is never disturbed. The
data directory is found via Zotero's own `ZOTERO_DATA_DIR`, the
`dataDir` in a Zotero profile's `prefs.js`, `<home>/Zotero`, and — for lflow in
WSL with Zotero on the Windows side — `/mnt/<drive>/Users/<user>/Zotero`. The
whole library loads once per session (lazily, re-read when its mtime moves) and
is searched in process, because the picker filters on every keystroke.

There are two surfaces, and they are deliberately different things: a **chip**
is a citation inside a sentence, a **node** is the paper itself living in the
tree.

In the editor (`editor/zotero.go`) a citation is a **chip**, not a node type:
kind `zotero`, value = Zotero's own `zotero://select/…` URI (so the stored value
IS the local-open target), label = the compact author-year form. `@@` — the ONE
path to a chip — opens the library picker; `alt+g` opens the paper
where `/settings` → "Zotero opens" points (local app or zotero.org), `alt+o`
opens the other one, and the chip carries the same target as an OSC 8 hyperlink
so a terminal ctrl+click lands there too. The local hop goes through
`browser.OpenApp`, which prefers the Windows shell under WSL because that is
where the `zotero://` handler is registered. The personal web-library URL needs
the account name: taken from the library's own account settings, else
`credentials.json`, else the entry's DOI. The library location is a read-only
`/settings` row ("Zotero library", copyable with alt+c) — there is no
lflow-specific env override; what lflow finds (prefs.js, `~/Zotero`, the WSL
`/mnt` side) IS what Zotero itself uses. Same for SearxNG: `/settings` →
"searxng.url" and `credentials.json` name the instance, no `LFLOW_SEARXNG_URL`.

A whole entry mirrors into the outline as the `zotero` **node type**
(`editor/zoteroitem.go`): `/type` → Zotero or `/insert` → Zotero picks the entry
through the same library picker — the picker lands immediately, and `alt+r` on
an unbound zotero node opens it again (the safe edge case). Its tags land on
the title row as colored chips with its attachments, annotations and notes
beneath it. Every node of the mirror wears the `zotero` type and is bound in
`zotero_nodes` (node uuid → item key + kind: item / attachment / annotation /
note / meta), which is what gives each row its own mark and its own alt+g: the
entry, the PDF in Zotero's reader, or `?annotation=` at one highlight.
Annotation and tag colors are Zotero's own hex mapped to the nearest color in
the LIVE palette (`zoteroNearestColor`), so an import sits inside the active
theme; a tag the user already colored keeps their choice. `alt+r` re-reads the
entry and reconciles against those keys — in place, so a refresh updates what
changed instead of duplicating the subtree.

What each Zotero object becomes, one for one:

| Zotero | binding kind | node | mark | alt+g | alt+o |
| --- | --- | --- | --- | --- | --- |
| the entry (item) | `item` | mirror root, tags as colored chips, fields on its `/note` | `Z` and the title, both brand red | the entry, per `zotero.open` | the other one |
| its fields (type, venue, date, DOI/URL, abstract) | — | NOT rows: `:: name value` properties on the entry's note | — | — | — |
| an attachment (the PDF) | `attachment` | one ordinary row — but a LONE attachment gets none, and its marks hang straight off the entry | `○` | the PDF in Zotero's reader | the file, in the host's app |
| a second attachment onwards | `attachment` | a row each, with a divider node between documents | `○` | the PDF in Zotero's reader | the file, in the host's app |
| a highlight / underline / sticky note | `annotation` | one row, text (or comment), `p.N` | `▍` in the highlighter's color | the reader AT that mark | the entry on the web |
| an area crop / pen drawing (image, ink) | `annotation` | a real **image node** — the picture is its blob | the image node's own `▦` face | the reader AT that mark | the picture, in the host's viewer |
| an annotation comment | `comment` | an ordinary row under its highlight | `○` | the entry | the other one |
| a note | `note` | an ordinary row, HTML flattened | `○` | the entry | the other one |

Only two rows earn a mark of their own — the entry, which is the paper, and a
highlight, which is a mark in a margin. Everything else is an ordinary node that
happens to be fixed. Because a lone attachment has no row, an annotation
remembers its own document in `zotero_nodes.parent_key` (lm42) rather than
looking upward for it.

The mirror is Zotero's, not yours: the root is content-locked (movable and
deletable as a unit), every node below it is content- AND position-locked, and
`/lock` and `/note` refuse on all of them — an edit there would be silently
discarded by the next refresh. The generic guards that enforce it are
`lockedSlot` / `acceptsChildren` in `model.go`; a refused splice returns
`errStructureLocked`, which callers flash rather than treating as fatal.

Remaining doc-level rules:

Remaining doc-level rules:

- Never auto-run runnable nodes (alt+r only) — an agentic coding session chip is
  opened by that same key and by nothing else.
- Secrets live in local config — Pi in `~/.pi/agent/settings.json`, service keys
  in `~/.config/lflow/credentials.json` (e.g. `{"workflowy":{"api_key":"…"},
  "zotero":{"username":"…"}}`). Never synced, never written into the DB.
