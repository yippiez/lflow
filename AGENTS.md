# AGENTS.md — operating guide for AI agents working in lflow

lflow is a local-first **terminal outline editor** (Go + bubbletea), forked from
dnote into a keyboard-driven outliner. The whole tree lives in one SQLite file
owned by a single **daemon** process; every CLI command is a one-shot,
pipe-friendly client of it, and `lflow node open` drops into an inline editor
that draws in the terminal **scrollback**, never the alt-screen — and live-updates
when any other client (CLI, agents) writes. Everything is a node with a
free-string `type`, and node types are an extensible registry — one descriptor
per type in `pkg/tui/editor/registry.go`.

## Build / test / run

- Always build with the fts5 tag (required by SQLite FTS5 node triggers):
  `go build --tags fts5 ./pkg/tui`
- After every change, install the dev binary so the user can test it:
  `go build --tags fts5 -ldflags "-X main.versionTag=0.1.0-dev" -o ~/.local/bin/lflow ./pkg/tui`
- Test before installing: `go test --tags fts5 ./...`
- Test in isolation against a throwaway HOME/XDG + seeded DB — **never** the real
  outline at `~/.local/share/lflow/lflow.db`. SQLite surgery goes through a
  `-tags fts5` Go program (the sqlite3 CLI lacks fts5).
- Development is **trunk-based**: work directly on `main`, no feature branches
  or PRs. Commit each logical change as its own `label: description` commit
  (labels: `editor`, `db`, `agent`, `daemon`, `docs`, …) and push to `main` as
  you go — do not batch work at the end.
- No emojis — plain Unicode symbols only (○ ◆ ▸ ● $ {} →). CLI output uses `→`/`·`.

## Daemon + live sync

- One daemon per database owns the SQLite file (WAL, one connection,
  update-hook change events). Everything else is a client: `infra.Init` →
  `client.Ensure` dials `daemon.sock` next to the DB and auto-spawns
  `lflow serve --quiet --idle` (10 min idle exit) when absent. A daemon built
  from a different binary is shut down and respawned on first contact, so dev
  rebuilds never talk to stale code. `LFLOW_NO_DAEMON=1` opens the file
  directly — tests and DB surgery use this; `lflow serve` itself never routes
  through a daemon.
- Foreground `lflow serve` is the live monitor: one dim `→` line per event
  (serving/connected/applied/gone), node names in yellow, chip anchors
  resolved. `--db`/`--sock` point it at another database (isolated testing).
- Wire protocol (`pkg/tui/wire`): ndjson over the unix socket. SQL is
  forwarded through a `database/sql` driver (`pkg/tui/client`), so every
  `database.*` helper works unchanged on a remote handle; values travel as
  tagged strings ("i:"/"f:"/"s:"/"b:"/"d:") because UnixNano int64s do not
  survive JSON floats. The daemon (`pkg/tui/daemon`) serializes all writes —
  a client transaction holds the write lock begin→commit, watchdogged at 30s.
- Editor live sync (`pkg/tui/editor/livesync.go`): there is no unsaved state
  anymore — edits auto-flush ~1s after typing pauses (bar shows `syncing`;
  ctrl+s = flush now). The subscribe feed folds other clients' commits into
  the in-memory tree in place. Conflicts are errorless last-writer-wins per
  node, with a dirty shield: a node edited locally since the last flush never
  adopts a remote version (the flush lands within a second and wins). External
  changes drop the undo stack; events defer while a picker/note/expanded view
  is open and drain when it closes; a dropped feed reconnects and resyncs
  wholesale.

## Adding new nodes

`nodes.type` is a free string, so a new type needs **no DB migration** and no
per-feature column — and no scattered `switch typ`:

1. Add a `TypeXxx` constant in `pkg/tui/database/node.go` and to `ValidTypes`.
2. Add one `nodeType` entry to the `nodeTypes` slice in
   `pkg/tui/editor/registry.go` — that slice drives the `/type` picker, and the
   field doc-comments there list every hook (`sign`, `glyph`, `render`,
   `inlineEditable`, `tempOnly`, `run` on alt+r, `expand`/`view` on alt+e,
   `toContext` for the node's XML element in agent context).
3. Put the behavior in its own `pkg/tui/editor/<type>.go` (see `json.go`,
   `voice.go`, `worker.go`; `bash.go` holds the shared shell-run machinery). A rich alt+e editor implements the
   stateless `nodeView` interface, keeping per-node state in `m.nodeStore(it.uuid)`.

Then build/install with the fts5 tag. Runnable types execute on alt+r only (never
auto-run) and their output is ephemeral — never persisted or synced.

## Node priority

`nodes.priority` (lm39) says where INCOMING nodes land among a node's children:
`up` = top, `down` = bottom. New children (CLI `add`), moved-in nodes
(`/move:to`, `mv`, indent, multi-select, `/mirror:from`) and agent replies all
route through it (`database.PlaceRank`, `tree.reparent`). New nodes default
up; everything that existed before lm39 is down. `/priority:up` /
`/priority:down` set it (immediate write, like /star). Agent-chipped mention
nodes and ✦ replies are FORCED down — a conversation always reads top-down
chronological; `/priority:up` refuses a mention, and chip completion / thread
send convert a pre-set up (`forceThreadPriorityDown`). `buildThread` walks a
priority-up node's children reversed, so agent context is always oldest-first
regardless of display order — the pi prompt tells the agent so.

## The @mention agent

- **No runtime extension system.** NodeMods (runtime JS node types / chip
  kinds via goja; "artifacts", then "GenUI nodes" historically) existed and
  were removed in 2026-07 — every node type is compiled in now. Nodes OF a
  former mod type still render (unknown types fall back to bullets, text
  intact); the `log` type was promoted to a built-in. lm38 dropped the
  `artifacts` and `node_mod_data` tables.
- **embedded skill** (`pkg/agent/skills/lflow/` — SKILL.md, cli.md, embedded
  by `pkg/agent/skills.go`) teaching the CLI agent the lflow CLI and chips.
  It is materialized to `~/.local/share/lflow/skills` at editor start and
  passed to pi via `--skill` each turn — skills only, never a pi extension.
- **@mention agent** (`pkg/tui/tag` + `pkg/tui/editor/agent.go`): typing `@`
  completes configured agents and lands a red **agent chip** (expands to plain
  `@Name`, so every mention detector reads it like typed text). Two trigger
  rules, nothing else: (1) alt+r on the mention node is the manual fire —
  always (starts the session or re-sends); (2) any local edit to a DESCENDANT
  of the mention arms a ~1s debounce (markAgentTouch → noteAgentChange); when
  it settles the agent re-reads the thread and decides whether to reply (PASS
  is fine) — cursor-leave and Enter do not ship on their own. The mention node IS the thread root — the session binds to
  it, so siblings/ancestors never trigger or receive replies. Context per turn
  = the mention's parent (one ambient `<parent>` element) + the mention +
  everything beneath it, rendered as nested XML (`<asked>`/`<answer>`/`<node>`;
  a typed node wears its type's element via the registry's `toContext` hook —
  `<todo done="true">`, `<log time="…">`, `<json>` with its document as the
  body — role tags win over type tags) inside a `<NodeContext>` block under an
  `<instructions>` tag; every node's
  children land at most once, so mirrors can neither loop the walk nor
  duplicate a subtree — nothing else; the rest of the outline the agent
  searches itself via the lflow CLI (`lflow node grep/list`, taught by the
  skill and system prompt). Replies land as red ✦ `agent` nodes — an internal type (never
  offered in /type, only the agent creates one), born locked; /lock unlocks
  a reply for reshaping like any other node. Only text after the turn's LAST tool call
  lands as the reply — narration between tool calls feeds the live band and
  is discarded, so replies read like chat messages, not work reports. Replies may speak chips:
  `{{cmd:…}}` / `{{path:…}}` / `{{link:label|url}}` / `{{tag:…}}` / `{{date:…}}`
  tokens land as real chips (`{{cmd:…}}` is the runnable yellow $ chip); plain
  #tags and dates auto-convert. Attachments hang as typed children under the
  reply via `{{attach:type|body}}` or a `{{attach:type}}…{{/attach}}` block
  (code, image, bash-as-cmd, json, quote, … — not conversation bullets). The
  pi system prompt (`pkg/tui/tag/pi.go`) teaches the tokens. Agents are launch-and-forget:
  every turn is a fresh CLI run (pi --no-session, or the grok CLI when the
  model pref reads `grok:…`) fed the whole thread as it reads now — no remote
  session to drift from edited nodes. `agent_sessions` holds only the LOCAL
  thread binding (node ↔ agent), so follow-ups keep reaching the agent across
  editor restarts.
  Config `~/.config/lflow/agents.json`; without it a built-in mock **Pi** is
  registered. Wire protocol: JSON over websocket, see `pkg/tui/tag/ws.go`.

## Key invariants

The structural invariants now live as `// WARNING (invariant):` comments next to the
code they govern — grep `WARNING (invariant)`. They cover: inline scrollback / no
alt-screen, no markup in stored text, the node-type registry (no DB migration),
ephemeral run output, sync exclusions (secrets / view-state / Temporary Domain /
binary), and the status bar being the last rendered line.

Remaining doc-level rules:

- Never auto-run runnable nodes (alt+r only).
- Secrets live in local config — Pi in `~/.pi/agent/settings.json`, service keys
  in `~/.config/lflow/credentials.json` (e.g. `{"workflowy":{"api_key":"…"}}`).
  Never synced, never written into the DB.
