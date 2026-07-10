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
   `voice.go`, `worker.go`; `bash.go` holds the shared shell-run machinery). A rich alt+e editor implements the
   stateless `nodeView` interface, keeping per-node state in `m.nodeStore(it.uuid)`.

Then build/install with the fts5 tag. Runnable types execute on alt+r only (never
auto-run) and their output is ephemeral — never persisted or synced.

## NodeMods + the @mention agent

- **NodeMods = runtime-installed node types / chip kinds** ("mod" in the UI;
  "artifacts", then "GenUI nodes" historically): one mod per entry in
  `~/.config/lflow/mods` — a flat `<key>.js`, or a `<key>/` directory
  installed from git via `lflow node install <git-url>` whose `mod.json`
  (`{name, description, entry, version}`) names the entry JS. A `.disabled`
  suffix on either form turns the mod off (space in `/type` does the rename,
  ctrl+d deletes). The directory is evaluated at editor start via goja
  (`pkg/tui/editor/nodemod.go`) and reloaded when `/type` opens and after
  every agent turn, so an agent (or the user) edits the files directly. The
  JS calls `lflow.registerType({...})` / `lflow.registerChip({...})`; the
  bridge appends a regular `nodeType` descriptor, so `/type`, glyphs and
  rendering treat built-ins and mods identically. Trusted, full access
  (`lflow.exec`). A node OF a mod type is a normal node row — disable or
  delete the mod and it renders as a plain bullet, text intact. Seeded
  reference: `log.js` (external twin: github.com/yippiez/lflow-log). Legacy
  migrations run once: the old `nodes/` dir renames to `mods/`; before that,
  `artifacts`-table rows export as files.
- **Mod views = custom UI, not just one-liners** (`pkg/tui/editor/nodemod_view.go`
  + `nodemod_api.go`): a `registerType({view:{…}})` gives a mod the FULL inline
  expanded surface (alt+e) — as rich as a built-in like image. The `view` is an
  Elm loop marshaled across goja: `init` seeds per-node state, `render(node,
  state, ctx)` returns styled band strings (Go owns rail/indent/clip/window),
  `key`/`update`/`enter` return `{state?, effect?}`, `leave` persists. `render`
  is pure (no exec on the frame); side effects are EFFECT descriptors
  (`{kind:exec|fetch|tick|batch}`) Go runs off the loop and feeds back to
  `update` via `modUpdateMsg`/`modTickMsg` — ticks only while focused, so a loop
  can't animate an off-screen node. SDK extras: `lflow.canvas(w,h)` (truecolor
  cell grid → bands, the graphics escape hatch), `lflow.text.*` (width-aware
  layout), `lflow.getData/setData` (durable per-node JSON in the local-only,
  never-synced `node_mod_data` table; blobs stay out — shell to a `<uuid>` file).
  Ephemeral state lives in `m.nodeStore(uuid)` as Go-native values so it survives
  the after-agent-turn reload; the jsView is re-looked-up per message so a reload
  never delivers into a stale runtime. Reference: `examples/barchart.js`.
- **embedded skill** (`pkg/agent/skills/lflow/` — SKILL.md, cli.md, mods.md,
  examples/, embedded by `pkg/agent/skills.go`) teaching the CLI agent the
  lflow CLI, chips, and NodeMods. It is materialized to
  `~/.local/share/lflow/skills` at editor start and passed to pi via `--skill`
  each turn — skills only, never a pi extension.
- **@mention agent** (`pkg/tui/tag` + `pkg/tui/editor/agent.go`): typing `@`
  completes configured agents and lands a red **agent chip** (expands to plain
  `@Name`, so every mention detector reads it like typed text). Two trigger
  rules, nothing else: (1) alt+r on the mention node is the manual fire —
  always (starts the session or re-sends); (2) a committed change to a
  DESCENDANT of the mention (Enter, or cursor-leave via blurSendCheck) ships
  automatically. The mention node IS the thread root — the session binds to
  it, so siblings/ancestors never trigger or receive replies. Context per turn
  = the mention + everything beneath it (mirrors expanded once, cycle-guarded)
  PLUS a Screen-marked ambient section of whatever is visible in the editor
  window — nothing else; the rest of the outline the agent searches itself via
  the lflow CLI (`lflow node grep/list`, taught by the skill and system
  prompt). Replies land as red ✦ `agent` nodes — normal, editable nodes; only
  the glyph marks authorship. Replies may speak chips:
  `{{cmd:…}}` / `{{path:…}}` / `{{link:label|url}}` / `{{tag:…}}` / `{{date:…}}`
  tokens land as real chips (`{{cmd:…}}` is the runnable yellow $ chip); plain
  #tags and dates auto-convert. The pi system prompt (`pkg/tui/tag/pi.go`)
  teaches the tokens and points at the mods dir. Agents are launch-and-forget:
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
