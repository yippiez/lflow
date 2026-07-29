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
- Verification (tests, e2e, ast-grep, isolation rules) lives in
  `.claude/agents/verifier.md` — Claude Code calls it as the `verifier`
  subagent; other agents follow the same steps manually. Verify before
  installing.
- Development is **trunk-based**: work directly on `main`, no feature branches
  or PRs. Commit each logical change as its own `label: description` commit
  (labels: `editor`, `db`, `agent`, `daemon`, `docs`, …) and push to `main` as
  you go — do not batch work at the end.
- No emojis — plain Unicode symbols only (○ ◆ ▸ ● $ {} →). CLI output uses `→`/`·`.

## Daemon + live sync

- One daemon per database owns the SQLite file (WAL, one connection,
  update-hook change events) and runs NLPCompute generation — the client is
  only a client. `wire.OpCompute` streams one generation per dedicated conn
  (editor ships the prompt + cwd + skill dir; closing the conn cancels Pi);
  `wire.OpDeps` answers which CLI binaries the daemon can exec — node types
  declare `cliDeps` in the registry, and a missing dep greys its /type entry
  and runs error "Missing dependency: <bin>". Everything else is a client: `infra.Init` →
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
   `onType` to land on a face right after /type, and `toContext`/`toContextM`
   for structured XML context).
3. Put the behavior in its own file. PLUGGABLE types live in
   `pkg/tui/editor/nodes/<type>.go` — ONE file per node — registered at init
   via `editor.RegisterNodePlugin` (see `editor/nodeplugin.go`): the editor
   hosts the generic plugin API (`NodeHost` = editor surface, `NodeRef` = the
   node, both interfaces so a node file tests against fakes), async work flows
   back through `NodePluginMsg`, `OnRemove` cancels in-flight work. Core woven
   types (bullets…image, `json.go`, `voice.go`, `math.go` — a math expression
   composed AS an outline: a node's text is an operator (colored yellow via the
   `spanColor` hook) with operands as children or a plain atom leaf; simple
   expressions stay inline, complex ones fan into a child tree and the operator
   row shows a dim linear preview of its subtree via the `bodyTail` hook; alt+r
   exports any subtree as LaTeX (mathToLatex) into the run band, and one symbol
   table (mathSym) drives both operator coloring and LaTeX — arithmetic, Greek,
   relations, calculus, set/logic, plus programming/bitwise/tensor operators;
   `bash.go` holds the shared
   shell-run machinery, `code.go` the shared multi-line **code block** —
   `codeBlockLines`: a borderless gray block (no rule box, no header) whose every
   line is the dim line number, a white vertical rule to its RIGHT, then the
   highlighted code; the block REPLACES the node's row via the `blockCode` hook
   (`viewRenderRows` → `blockGroupLines`), shared by the Code node and the
   nlpcompute node's code face) stay in the editor as `nodeType` entries; a rich
   alt+e editor implements the stateless view interface either way, per-node
   state in the node store. The Code node is edited only in that focused block
   (not inline): its multi-line body IS `it.name`, Enter is a newline inside
   the block, two spaces after a content char at the end exit to a fresh
   sibling, Tab indents two spaces. (The canvas plugin — a two-plane crosshair
   painter — and the codereview / codesig plugins were removed in 2026-07; old
   rows of any removed type fall back to bullets like any unknown type.)

Then build/install with the fts5 tag. Runnable types execute on alt+r only (never
auto-run) and their output is ephemeral — never persisted or synced.

## Tables

The Table node (`pkg/tui/editor/table.go`) is a grid READING of an ordinary
subtree, not a storage shape: the table's children are its **columns** (their
text is the header), each column's children are that column's **cells** top to
bottom (row n = the nth child of every column), and a cell's own children are the
outline **inside** that cell. Nothing moves on conversion, so any node becomes a
table with `/type` and every node under it stays an ordinary node. Cells are
ragged in the outline and square on screen; typing into an empty slot
materializes it.

The two faces are the FOLD STATE — folded draws the grid as a band under the row,
open is the plain outline — so ctrl+space / alt+↑ / alt+↓ toggle table ⇄ nodes
with no extra state and the choice persists like any fold. alt+e opens the grid
editor (arrows/⇥ walk the cells, typing edits the real cell node, ⏎ adds a row
across every column, ⌥n appends a column, ⌥d twice drops a row, ⌥→ zooms into a
cell's own outline). Structural work the grid does not cover — deleting a column,
reordering, retyping a cell — happens in the nodes face, which is the point of
the toggle. A cell holding a chip is edited there too: a chip renders collapsed,
so a caret index in the grid is not an index into the stored text.

## Node priority

`nodes.priority` (lm39) says where INCOMING nodes land among a node's children:
`up` = top, `down` = bottom. New children (CLI `add`) and moved-in nodes (`/move:to`, `mv`, indent,
multi-select, `/mirror:from`) route through it (`database.PlaceRank`,
`tree.reparent`). New nodes default
up; everything that existed before lm39 is down. `/priority:up` /
`/priority:down` set it immediately, like /star.

## Agentic coding sessions

A coding session with an agent is an inline CHIP — and only a chip, so it can be
dropped into whatever note it belongs to instead of owning a row. Three CLIs, one
entry each in one table in `pkg/tui/editor/agent.go`: Claude Code ✽, Pi ᴘɪ,
opencode ▣. (Web services are deliberately not here yet.)

lflow does not START conversations. A chip is a handle on one its CLI already
made, so the whole vocabulary is four verbs:

- **search** — `/agent` lists every session found across all three stores, newest
  first, filtered as you type. A session is searched by its NAME first, then by
  which agent it is and the directory it ran in.
- **add** — enter files the highlighted session as a chip at the caret. There is
  no create path in the picker.
- **open** — `⌥r` suspends lflow (`tea.ExecProcess`) and the CLI resumes that
  session in its pinned directory. A session whose directory is gone refuses to
  open. `⌥r` is the only way a CLI is ever launched.
- **rename** — `⌥n` opens the name field on the chip. `⌥c` recolors it.

Names and colors come from the CLIs themselves, and what each one actually
exposes was checked against real stores, not assumed:

| | name | color | renamable by lflow |
|---|---|---|---|
| Claude Code | `~/.claude/sessions/<pid>.json` `.name`, live registry only | none | no |
| Pi | none — the first prompt names it | none | no |
| opencode | `.title` in its session record | none | **yes** |

So a rename is ALWAYS stored locally on the chip and always wins; `opencodeRename`
additionally writes the new title into opencode's own record, atomically and
round-tripping every other key. The other two have no name field lflow may write:
Claude Code's lives in a registry keyed by the pid of a running process, and Pi's
records carry none. No CLI exposes a session color today, so `⌥c` is what colors a
chip — the variant's default until you pick otherwise.

Colors: Claude's orange and Pi's purple are themed swatches; opencode's mark is
black on white and takes the `agentMono` literal `#000000`, with white ink from
`contrastInk`. A completed row's strikethrough runs THROUGH the pill.

`/agents` lists every session chip in the outline, live first, done ones muted
below; `alt+enter` on the row marks done. `⌥e` opens a three-line panel — the
session, where it runs and its state, and the keys — and nothing else.

WARNING (invariant): lflow never copies a conversation into the outline. A chip
stores only `{variant, cwd, session id}` plus your own name and color in LOCAL
`node_output` — never in the chip row, never synced — and everything else is read
back out of the CLI's own store (`agentstore.go`) on demand.

## NLPCompute code generation

NLPCompute is the only in-editor Pi surface. `alt+r` sends its natural-language
instruction and local outline neighborhood as one fresh, no-session generation
turn. The daemon runs Pi in the cell's pinned working directory and streams
progress back; generated code is stored in local `node_output`, shown through
the shared code-block face, and never executed automatically. The embedded
`pkg/agent/skills/lflow/` skill teaches the generator how to query the outline.
There is no conversational assistant, mention completer, assistant reply type,
or resumable external coding-session reference.

## Demo videos

A visible change ships with a short video. **`DEMO.md` in the repo root is the
recipe** — tmux drives the editor against a throwaway DB, `scripts/ansishot.py`
paints the captured frames, ffmpeg muxes them, and captions are burned along the
top. There is no committed demo tooling on purpose: assemble it in a scratch
directory, record, throw the scratch away. Follow DEMO.md exactly so every clip
matches — its numbers (caption bar, colors, timings) are the house look, and its
sandbox rule is what keeps a demo out of the real outline.

## Key invariants

The structural invariants now live as `// WARNING (invariant):` comments next to the
code they govern — grep `WARNING (invariant)`. They cover: inline scrollback / no
alt-screen, no markup in stored text, the node-type registry (no DB migration),
ephemeral run output, sync exclusions (secrets / view-state / Temporary Domain /
binary), and the status bar being the last rendered line.

Remaining doc-level rules:

- Never auto-run runnable nodes (alt+r only) — an agentic coding session chip is
  opened by that same key and by nothing else.
- Secrets live in local config — Pi in `~/.pi/agent/settings.json`, service keys
  in `~/.config/lflow/credentials.json` (e.g. `{"workflowy":{"api_key":"…"}}`).
  Never synced, never written into the DB.
