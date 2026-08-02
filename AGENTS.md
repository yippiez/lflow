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
   shell-run machinery and `bashnode.go` the **Bash node** — a shell command
   composed AS an outline, the same idea as math: a red `$` row — the cmd chip's
   prompt, always, fold or no fold — whose children are its parts, where a join operator (`|` `&&` `||` `;`) joins them, a wrapper (`$()`
   `()`) wraps them, empty text is a plain container and anything else heads them;
   a completed node is commented out of the command, chips resolve on the way
   through, the row hangs the composed line dim (`bodyTail`) — replaced by the
   run's streaming headline once there is one — and alt+r runs THIS node's
   subtree — `code.go` the shared multi-line **code block** —
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

- **search** — `/insert → agent` lists every session found across all three
  stores, newest first, filtered as you type. A session is searched by its NAME
  first, then by which agent it is and the directory it ran in. That is the only
  agent surface: there is no `/agent` and no `/agents`, because a chip belongs to
  the row it sits in and the outline is already the index of them.
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

`⌥e` opens a panel showing the session's name IN FULL (wrapped — a session named
by its first prompt is a sentence, and clipping is what makes two of them look
alike) and the directory it is pinned to. Nothing else, on purpose: everything
further lflow could say it would be guessing at. It used to print a state word
and a "last opened" time; "idle" was only ever the absence of a live registry,
which Pi and opencode do not have at all, and the timestamp was a store file's
mtime relabelled as an open. Neither was real, so both are gone.

`⌥r` / `⌥e` / `⌥n` / `⌥c` act on the chip under the caret, falling back to the
row's only session when the row wraps and the caret lands past it; a row with
several says so rather than guessing.

WARNING (invariant): lflow never copies a conversation into the outline. A chip
stores only `{variant, cwd, session id}` plus your own name and color in LOCAL
`node_output` — never in the chip row, never synced — and everything else is read
back out of the CLI's own store (`agentstore.go`) on demand.

## Edit suggestions

`lflow suggest` is the review queue: a proposed change lives in the
`suggestions` table (lm42) and touches NOTHING until somebody approves it —
kind `add` carries a node proposed under a parent, kind `edit` carries the
fields (`name`/`note`/`type`) proposed for one node. `suggest add` / `suggest
edit` file one, `list` / `show` read them (both take `--format json`, the
machine face agents use), `approve` applies through
`database.ApplySuggestion` — node write and settled status in ONE transaction,
so an approval can never half-land or apply twice — and `reject` settles
without touching the outline. Chips are created at approval time, so a
rejected suggestion leaves none behind. An edit snapshots the target text
(`base_name`/`base_note`): if the node moved on since, approve refuses and
says so, `--force` applies anyway. On the wire a suggestions write flags
`Event.Suggest` — deliberately NOT an aux table, since a proposal changes
nothing a client renders — and `lflow serve` logs it as `→ suggested`.

In the EDITOR (`pkg/tui/editor/suggest.go`) a pending proposal reads INLINE on
the node itself: its glyph becomes a yellow outline circle (`glyphSuggest`, in
place of whatever the type would draw) and the row gains `→ <proposed text>`
(`+ text` for a proposed child, `· +N` when more wait behind it). The arrow is a
suffix on the rendered line — like the mirror/locked/child-count one — so every
row-shaped type carries it for free (bullets, todo, headings, table, query,
math, json, wf, the nlpcompute prose face); types whose row is REPLACED by a
block (Code, the nlpcompute code face) and the divider rule take the same text
as one line under the block instead (`suggestBlockLines`). Proposals arrive live
when the feed reports one, and the bar counts them (`N suggestions`, no glyph —
the circle belongs on the node).

`alt+v` opens review on the cursor node (`modeSuggest`): the proposal expands
under it AT THE NODE'S OWN COLUMN — not indented, and carrying no circle of its
own, since the node already wears one — with author, message, the before/after,
then `y` approves, `n` rejects, `esc` leaves it pending, ↑↓ walks a node's
queue, and `/suggestions` jumps to the next node carrying one. The inline arrow
and the expansion are a READING of the suggestions table — approving is the only
path that writes, and it goes through `database.ApplySuggestion` and then
resyncs. A proposal against the current view's own root has no row to hang from;
`/suggestions` says so instead of pretending there is nothing.

## Query nodes

A Query node's name IS its search; `alt+r` runs it and reconciles the hits as
mirror children. Every filter is a `key(value)` call — never a `:key:value`
sandwich — alongside bare words, `#tag`, `&&`/`||`/`>`/parens, and `-x` to
negate:

```
buy type(todo) is(open)                     todos still open that mention "buy"
project > type(todo) after(2026-06-01)      dated todos under a "project" node
#urgent -is(done) in(<picked node>)         open urgent work inside one subtree
"why the build keeps failing" as(tree)      semantic search, nested under ancestors
```

Hits come back ranked: `/star` pins first, then relevance when a quoted atom
scored them, then name. A purely lexical query has no score — only "matched" —
so it stays name-ordered. A run streams its candidates, then hands the finished
expression to a worker (`queryReadyMsg`) so a big evaluation never blocks the UI
goroutine; the toolbar shows `N queries running` as an ultraloop shimmer
(`ShineText`) while it works. The worker reads only frozen data — the candidate
set stops growing and `qCtx.chips` is a snapshot — and everything touching the
tree (mirror reconciliation, the `as(tree)` breadcrumb sort) stays on the UI
goroutine.

Qualifiers: `type()` `in()` `after()`/`since()` `before()`/`until()` `is()`
(starred, unstarred, done, open) `has()` (note, children) `as()` (tree/list). A
`(` opens a value only when glued to a known key, so `(project || release)`
still groups; brackets also hold a value with spaces together, which is how
`before(2026-06-20 14:30)` keeps its clock time. The `:` completer inserts
`type()` with the caret between the brackets, and typing a key out by hand
lands the same text. Every colon spelling (`type:todo`, `:type:todo`) still
parses — query text is persisted node text, so old queries must keep matching
(invariant noted in `querytime.go`).

**A `"quoted phrase"` is a SEMANTIC atom** (`semantic.go`): it matches by meaning,
so it can return a node sharing no word with the phrase. The quote marks render
yellow like a math operator; the phrase inside stays ordinary text. The model is
built from the outline itself on every run — random indexing over the candidate
set, fused by Reciprocal Rank Fusion with BM25 and character-trigram Dice — so
there is no model file, no download, and no network call.

The model is **outline-native**: the tree is evidence, not just layout. A node's
fingerprint carries a decayed share of its ancestors', and its vector blends its
ancestors' and children's, so a query about a container reaches what is filed
inside it (including rows that share no word with it) and a query about a child
reaches its container. Each ancestor is damped by `sqrt(children)` — a parent of
three is making a claim about all three, a bucket of fifty is barely making one
about any. That damping is load-bearing: without it a subtree collapses into one
blob where every node is "related" to every other, and a query for one child
returns all its siblings. Nodes reach up and down, never sideways. Quality is bounded by
what the outline itself makes available: with no co-occurrence evidence linking
two vocabularies, the honest answer is no hits, and the matcher returns none
rather than the top of the noise. Swapping in static embeddings later changes
only the vector channel, not the fusion or the syntax.

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

EVERY visible change ships with a short video — one per change, handed over as
soon as the change is done, so it can be re-watched later. That includes the
small ones: a color, a glyph, a fold state. A still frame is not a substitute
(the thing being judged is usually motion or a transition); attach a still only
as an extra. A change with nothing on screen — docs, refactors, test-only work —
needs none. **`DEMO.md` in the repo root is the
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
- A cmd chip forms ONLY by deliberate trigger: `$$` lands an empty $ chip to fill
  in (alt+i edits its command), and `/insert → cmd` is the other way in. A single
  `$` is always literal — `$i`, `$(seq …)`, `$HOME` are shell syntax that type
  normally in a bash node, never a chip; there is no `$cmd` + double-space flow.
- A run is LIVE, never a wait: output streams in as it arrives (~50ms batches).
  **Both shell surfaces stream in their ROW, not under it** — a cmd chip in its
  label, a Bash node in its `→` tail (`runInTail` in the registry, fed by
  `runTails`): the newest line while the command is in flight, the first line
  once it settles, clipped to `runTailWidth` so a torrent cannot rewrap the row.
  A running chip's cell shimmers and a running node's tail does — a silent run
  (`sleep 30`) still shimmers "running…" instead of sitting static, because the
  animation tick starts the moment a run begins, not on its first output byte.
  Neither claims a single line beneath itself. The whole output is alt+e away,
  and the two surfaces read 1:1 there — same header, same pane, same keys.
  The tail belongs to the command that was RUN: edit a Bash node's text (or a
  chip's command via `alt+i`) and the stale result drops, reverting to the new
  composed command.
- The expanded run view is a terminal WINDOW, never a growing list: it opens at
  the height it will keep (`winH-1` filled rows) and the output scrolls inside
  it, so nothing on screen shifts as lines arrive. One line of chrome — state,
  elapsed clock, and where it ran, right-aligned — then the pane, which is plain
  terminal output on the editor's own background (no tinted block behind it).
- A shell run IS a terminal, not a captured log: `bash -c` runs on a **PTY** and
  its raw bytes drive a VT screen (`pkg/tui/editor/term.go`) — so programs detect
  a tty and colour themselves, `\r` progress bars overwrite in place, `clear`
  clears, cursor work lands, and the expanded band shows exactly what a terminal
  would, following the tail until you scroll up. Every band reads
  `runState.lines()`: the screen for a shell run, the appended list for a line
  producer (query, math's LaTeX export). On a PTY there is no separate stderr.
- Secrets live in local config — Pi in `~/.pi/agent/settings.json`, service keys
  in `~/.config/lflow/credentials.json` (e.g. `{"workflowy":{"api_key":"…"}}`).
  Never synced, never written into the DB.
