# Spec · Runnable nodes

**Status:** draft — consolidates the former bash / agent / code-cell / live-query /
node-pipeline / node-internal-data / cli-auth specs into one. The design was validated
with a throwaway prototype (now removed); implementation proceeds **one node type at a
time** against this spec.

This is the single spec for lflow's runnable node types. Four types live on top of one
small set of foundations (a type registry + a per-node data blob + local auth), and a
**Temp Space** sits below the outline as their scratch/run area.

- **bash** — node text is a shell command; `alt+r` runs it.
- **compute** — node text is code; runs in a Python session (local or Colab).
- **live query** — node text is a query; results live-mirror as read-only children.
- **worker** — node text is a message to a coding agent; `alt+r` runs a turn.

## Core philosophy — every node outputs context

Everything is a node, and **every node exposes one thing to the rest of the system: a
context string** (`Context() string`). A bash node's context is its run output; a query
node's is its results; a compute node's is its generated snippet; a worker's is its own
summary; a plain note's is its text. This single string is the universal seam by which
any node feeds any other.

The **worker** is the consumer. Running a worker **produces no outline nodes** — it just
does work, taking its **children's context strings** as input (any node type qualifies:
bash, query, compute, even another worker). You inspect what it did — the **diff**, the
**actions it took**, and a **summary** — in its **details page**, never as nodes appended
to the tree.

> **Pending your input** — **compute** backend details (§4) and the **Temp Space**
> behaviour (§8) are still **TBD**; compute is deferred until bash/query/worker feel
> right. Everything else is settled.

---

## 1. Foundations

### 1.1 NodeType registry (prerequisite refactor)

Every type is added through one descriptor table that replaces today's scattered
switches in `glyphFor`/`renderBody` (`pkg/tui/editor/render.go`), `typeOrder`/
`typeLabels` (`pkg/tui/editor/editor.go`), the markdown export switch
(`pkg/tui/outline/outline.go`), and `ValidTypes` (`pkg/tui/database/node.go`).

```go
type NodeType struct {
	Key   string // node.Type / item.typ value, e.g. "bash"
	Label string // /type picker label

	Glyph  func(it *item) (glyph, color string) // nil → default ○ / ● collapse glyph
	Prefix string                               // injected before text, like quote's │
	Attrs  string                               // text SGR attrs under per-node style

	Markdown func(name string) string           // export rewrite; nil = unchanged
	Run      func(m *Model, it *item) tea.Cmd    // nil = not runnable (alt+r no-op)
	Context  func(it *item) string               // node's context string; nil = node text
}

func typeOf(key string) *NodeType // never nil; unknown keys fall back to bullets
```

`glyphFor`/`renderBody`/`typeOrder`/`typeLabels` become lookups; `ValidTypes` becomes
"is this key in the registry." The `○ $` / `→` renders reuse the same `Prefix` seam
that quote already uses for `│`.

### 1.2 NodeInternalData — the per-node blob

A generic per-node `InternalData string` JSON field — the storage primitive every rich
type uses. **Read-only to the user**: written only by a type's machinery (run output,
agent turns, query results), never editable and never leaked into search/export.

```go
func loadData[T any](raw string) T  // tolerant: empty/garbage → zero value
func saveData[T any](v T) string     // marshals, enforces the size cap
```

- Size-capped (proposal 256 KB) with per-list bounds, so a long history of big outputs
  can't blow the blob.
- **OPEN (the one blocker before real implementation):** one `internal_data` DB column
  (recommended — the last migration ever) **vs** a local sidecar file. Output and agent
  transcripts may be large and are reproducible; a sidecar keeps the synced row small.

### 1.3 Local auth — `lflow auth <provider>`

Compute (Colab) and worker (provider keys) need credentials. `lflow auth <provider>`
prompts with a hidden, no-echo field (`golang.org/x/term`); Colab uses a browser OAuth
loopback (ported from `beyin-monorepo/packages/compute/src/auth`).

**HARD INVARIANT:** secrets never appear in argv, shell history, the synced DB, or logs.
They live only in a local `~/.config/lflow/credentials.json`, mode `0600`. This unblocks
`/mirror:wf` (workflowy), Colab, and worker provider keys.

### 1.4 `Context() string` — the universal node seam

Every node type implements `Context() string` (the registry `Context` field; `nil` falls
back to the node's text). This is the one value a node contributes when another node uses
it as input — the mechanism behind the §0 philosophy.

```go
func (t *NodeType) Context(it *item) string // bash → run output · query → results
                                            // compute → generated snippet · worker → summary
```

- It is derived from the node's `InternalData` (run output, query hits, worker summary),
  so it is always the live, read-only product of the node — never hand-edited.
- A worker's input is the concatenation of its **children's** `Context()` strings
  (cycle-safe, §6.5). Because the seam is uniform, mixing node types as context is free.
- Size-bounded per node so a deep context tree can't blow past the model's window.

---

## 2. The single-line style (decided)

Every runnable node stays **one line**. Run state lives in the bullet (a pulsing
`· ∘ ○ ◯` spinner while running, back to `○` when idle — **no** `✓`/`✗`). Metadata
trails the node text after a dim `┊` separator.

**Worker** is the canonical case — a plain `○` bullet (no prefix), the message, then
`┊ model · time · tokens · $`:

```
○ how do I run the tests?     ┊ claude-sonnet · 44s ↑5.0k ↓2.5k $0.0015
```

While a turn runs, the same line shows the live one-line activity instead of the final
cost:

```
○ how do I run the tests?     ┊ claude-sonnet · 12s · running the suite…
```

- `↑` input tokens, `↓` output tokens, `$` cost, time in seconds, then a **`+N -M`
  line-change count** from the worker's diff (added green, removed red).
- The live activity ("running the suite…") comes from a separate cheap summary agent.
- **No inline transcript, no second line.** A worker produces no outline nodes; its diff,
  actions, and summary open in the **details page** (`alt+e`, §7).

**Prefixes** distinguish the runnable types: bash `$ `, compute `→ ` (the dash-arrow,
reserved for compute), live query `⌕ `; worker has **none**. All prefixes render in
**muted gray** (no accent color) — like the bullet and bash's `$`. bash keeps its
code-block body; metadata (`exit 0`, run `i/n`) trails the same way. live query trails
`· N hits`.

---

## 3. bash node

Node text is a shell command. `alt+r` runs it; output **streams** into a self-contained
band under the node (never child nodes), stored in `InternalData`.

```
○ $ go test ./...                          ← $ kept; command on a dark bg, 1 padding
  › go test ./...                          ┐ preview, max ~3 lines, on the gray bgCode box
  ok    lflow/pkg/tui/editor   0.142s      │ stdout white
  --- FAIL: TestSync (0.01s)               ┘ stderr red
  exit 1 · ⋯ 3 more · alt+e expand          ← bottom line: TRANSPARENT, muted gray
```

- **Render:** `○` bullet + `$ ` prefix + the command as **plain gray text on a transparent
  background** (no chip — only the output box below is colored). Multiline: Enter adds a
  line; **Enter on a blank line deletes it and exits to a new node below.**
- **Output band — bounded preview + a contrasting bottom line.** A capped preview (max ~3
  lines) of the streaming output on the contained gray box — **stdout white, stderr red**,
  no labels/chrome. It's closed by a **bottom line with no background** (transparent, muted
  gray) carrying `exit <code> · ⋯ N more · alt+e expand` (code green for 0, red otherwise;
  `running…` while live). The transparent bottom line **contrasts** the gray box and forms
  its natural divider/edge; the full output lives behind `alt+e`.
- **Run history:** each `alt+r` appends a `BashRun` and selects it; `alt+,`/`alt+.` page
  runs; `alt+e` opens the selected run full-screen (full-width rules, pinned command,
  white scrollable text). Bounded by run count (≈20) and the blob cap.
- **Never auto-run** — not on load/sync/paste/import, only `alt+r`. Server stores
  `type="bash"` as an opaque string and never executes. A second `alt+r` cancels.
- **Export:** fenced ```bash block of the command; output not exported.

```go
type BashRun struct {
	Output    string `json:"output"`
	Exit      int    `json:"exit"`
	RanAt     int64  `json:"ran_at"`
	Truncated bool   `json:"truncated,omitempty"`
}
type BashData struct {
	Runs     []BashRun `json:"runs"`     // chronological, newest last
	Selected int       `json:"selected"` // shown in the band; default = latest
}
```

---

## 4. compute node

A **`compute`** node is a **natural-language description of a code snippet**, prefixed
with the dash-arrow `→`. You write what you want in plain words — "take the scores, drop
the NaNs, then average them" — and `alt+r` **generates the code** for it, written to
`.pi/snippets/<id>.<ext>`. Because the node stays natural language, it reads as part of
the notes and doesn't clutter the outline with code.

```
○ → take the scores, drop the NaNs, then average them        ← NL description (default)
```

After generation you **toggle** (proposal: `alt+t`) between the NL description and the
generated snippet — the code is shown as the full code block (numbered lines, cyan `│`,
full-width `bgCode`) in place of the description:

```
○  1 │ xs = load_scores()                                     ← toggled to the snippet
   2 │ xs = xs[~np.isnan(xs)]
   3 │ print(xs.mean())
```

- The NL text is the editable `name`; the generated code lives in `.pi/snippets/` (and a
  pointer/hash in `InternalData`), regenerated by `alt+r`, never hand-edited inline.
- `Context()` returns the generated snippet (and its output once executed).
- Toggling is per-node view state; the default view is the NL description so notes stay
  readable.

**Execution (later):** running the snippet in a Python session (local or Colab, ported
from `beyin-monorepo/packages/compute`: `src/kernel`, `src/session`, `src/colab`, browser
OAuth via `lflow auth colab`) is a follow-on — generation + toggle come first. Risks:
Colab has no clean programmatic kernel API; rich/image output needs sixel/kitty or a
"saved plot.png" fallback.

Workers can emit compute nodes (NL descriptions) in their work.

---

## 5. live query node

Node text is a **query**; matching nodes appear as **mirror nodes that are first-layer
(direct) children** of the query node — read-only references to the source nodes,
refreshed lazily when the node is visible and the DB is dirty. They render in lflow's
existing **mirror style: a red `◆` glyph + a dim `mirror` suffix** (same as
`it.mirrorOf != ""` in `render.go`), so a hit is recognizably a mirror, not an owned node.

**Refresh is a reconcile, keyed by the mirrored source's UUID** (not a wipe-and-rebuild):

- a hit **already a direct child** → **updated in place** (kept where it is);
- a hit **not present** → **appended at the bottom**;
- a direct-child mirror that **no longer matches** → removed.

Because the query **owns** its hits, you can't permanently evict a still-matching hit by
moving it out: e.g. `shift+tab` a mirror to outdent it past the query's subtree, and the
next refresh **regenerates it and adds it back at the bottom** (the stray copy is dropped).
This makes the child list a live projection of the result set while preserving the order
of the hits you've kept.

**Language:**
- plain words are searched as text;
- `:AND` `:OR` combine, parens nest;
- `:IS <type>` filters by node type;
- `:BEFORE <date>` / `:AFTER <date>` filter by date — matching **both** a node's date
  chip and its `added_on`;
- `>` is **scoped drill-down**: find the left term, then search the right term **only
  inside the left match's subtree**, returning the right hits — e.g.
  `(meeting :AFTER 2026-01-01) > action item`.

**Errors:** a syntax or runtime error renders the node **red** (the bullet circle's
color, matching the editor — not a background highlight).

Cycle-safe: mirrored hits are read-only; a `visited` set prevents a query result that
loops back from recursing (shared with the worker context guard, §6).

---

## 6. worker node

Node text is a **message to a coding agent**. `alt+r` runs a turn over the node's
**children context** (§1.4, cycle-safe §6.5). The worker stays **one line** (§2) and
**produces no outline nodes** — it just does work. What it did (the **diff**, the
**actions taken**, a **summary**) lives in its **details page** (§7); its own
`Context()` returns the summary so a parent worker can build on it.

### 6.1 Provider abstraction

Go has interfaces — that's the mechanism. The common surface is a **normalized event
stream**; each adapter owns its own CLI flags and wire format.

```go
type AgentProvider interface {
	Run(ctx context.Context, req AgentRequest) (<-chan AgentEvent, CancelFunc, error)
}
type AgentRequest struct {
	Model, System, Message, Context string
	Orchestrate                     bool // ultracode (§6.4)
}
type AgentEvent struct {
	Kind   EventKind    // Thinking | Message | ToolStart | ToolResult | Final | Error
	Text   string
	Tool   string
	Args   string
	Result *AgentResult // the work product (Kind == Final) — see §6.3
	Err    string
}

// AgentResult is what the worker did — shown in the details page (§7), never as nodes.
type AgentResult struct {
	Summary string        `json:"summary"` // one-liner; becomes the node's Context()
	Actions []AgentAction `json:"actions"` // tool calls, condensed
	Diff    string        `json:"diff"`    // unified diff of file changes, if any
}
type AgentAction struct {
	Tool, Detail string
}
```

### 6.2 Pi RPC adapter (first provider)

Mirrors `pchain/pi/src/agents/manager.ts`: spawn the CLI in RPC mode, talk
newline-delimited JSON over stdin/stdout.

```
pi --mode rpc --no-session --approve --tools read,bash,grep,find,ls \
   --append-system-prompt <system> --name <node> [--model <m>]
```

- stdin: `{id,type:"prompt",message}`, `{type:"steer",message}`, `{type:"abort"}`.
- stdout: `agent_start` → `message_end {message,usage}` →
  `tool_execution_start {toolName,args}` → `tool_result` → `agent_end`, plus
  `{type:"response",id,success,data}` acks. Mapping: `message_end`→`Message`,
  thinking deltas→`Thinking`, `tool_*`→`ToolStart`/`ToolResult`, `agent_end`→`Final`.

### 6.3 Result · a details record (no nodes)

The worker emits **no nodes** — its product is an `AgentResult` (§6.1) written to the
node's `InternalData`: a **summary**, the **condensed actions**, and a **diff** of any
file changes. The summary is the node's `Context()` (§1.4) so a parent worker can build
on it; the full record opens in the details page (§7). A cheap summary model phrases the
live one-liner and the final summary; actions and diff come straight from the provider's
tool stream.

### 6.4 Magic keywords · ultracode & ultraloop

Magic words are detected **anywhere in the message at render time** (nothing stored — no
per-node style violation) and animate in place. They **keep their color (and keep
animating) even while the node is focused/being edited** — only the single character under
the caret takes the cursor highlight:

- **`ultracode`** — animated **purple** with a soft sliding glow (the highlight blends to a
  light purple tint, never white, so the word stays purple). Orchestrates a staged pipeline
  (below).
- **`ultraloop`** — animated **red** with a continuous sliding shine (a touch faster than
  ultracode's purple). It runs the worker as a **self-refinement loop**: re-run, feed the prior
  result back in, repeat until a stop condition holds (e.g. tests pass / no further
  changes) or a cap is hit. *(OPEN: exact stop condition + cap; this is the looping form of
  the §6.4 pipeline. Detection is anywhere in the sentence.)*

`ultracode` sets `Orchestrate: true`: the lead reads a natural-language plan and runs a
**staged, conditional pipeline** of worker + bash nodes:

> first three deepseek-v4-flash propose improvements in **parallel**, then one
> kimi-2.6 implements, then a **bash** node runs the tests, and **if they fail** a
> kimi-2.6 fixes and re-tests (capped).

Stages run in sequence; a stage may fan out in parallel; a stage may be conditional /
loop on a bash exit code with a retry cap. Sub-agents render as a **purple group**
(circle + branch lines + text); the test bash node stays normal-colored.

```go
type Stage struct {
	Label          string
	Nodes          []int // node ids in this stage (parallel within a stage)
	OnPass, OnFail int   // next stage index, or -1
	MaxRetry       int
}
type Pipeline struct { Start int; Stages []Stage }
```

### 6.5 Context · children, cycle-safe

The worker's input is the concatenation of its children's `Context()` strings (§1.4),
walked depth-first. A `visited` set of UUIDs expands each node **once**; later
occurrences (a mirror, or a live-query result that loops back) render as a reference
marker `↪ <name>`. Hard caps on depth and total size. Shared with the live-query cycle
guard (§5).

### 6.6 Model selection + comparison

`alt+m` picks the model (stored per turn, shown in the `┊` line). Comparison = run the
same question on two worker nodes (or re-run) with different models and compare their
details pages (summary + diff) side by side.

---

## 7. Details page

The single-line worker shows only `┊ model · time · tokens · $` and, while running, the
live one-liner. Everything else opens in the **details page** (`alt+e`): the worker's
**summary**, the **condensed actions** it took (`Read api util functions`, `Bash go test
./...`), and the **diff** of file changes — the `AgentResult` (§6.3) rendered full-screen
(same reader chrome as bash's `alt+e`). None of it lands in the outline.

---

## 8. Temp Space

A second outline region rendered **below the footer**. It behaves **exactly like the
normal outline** — full editing, every node type, indent, run, fold — but it is
**ephemeral**: its contents are **not persisted and do not survive closing and relaunching
the app**.

It's a scratchpad: jot temporary notes while you work, or stand up throwaway agents
(worker nodes) you don't want cluttering — or persisting in — the real outline. Run them,
read their details, then quit and they're gone.

```
  … main outline …
─────────────────────────────────────────  ← footer (help / session)
Temporary Domain                             ← plain muted-gray label, just text
  ◌ how do I run the tests?   ┊ claude-sonnet · 44s ↑5.0k ↓2.5k $0.0015
  ◌ scratch: ideas to revisit
```

- The divider is plain text — **`Temporary Domain`** in the **same muted gray as the
  footer**, no rules or emblems.
- Its nodes carry a **dotted circle glyph `◌`** (U+25CC) instead of the solid `○`, so an
  ephemeral node is recognizable at a glance (still pulses the spinner while running, `●`
  when collapsed).
- Same key bindings and node machinery as the main outline; the cursor moves between the
  two regions seamlessly.
- Backed by in-memory state only — never written to the DB, never synced.
- Throwaway agents live here so they don't persist in the real outline.

---

## 9. Keybindings

| key | action |
|-----|--------|
| `alt+r` | run the cursor node — bash runs, compute generates the snippet, worker runs a turn · again while running cancels |
| `alt+t` | compute: toggle between the NL description and the generated snippet |
| `alt+s` | steer — extra message into the running/finished worker turn |
| `alt+e` | worker: open the details page · bash: open the run reader |
| `alt+,` / `alt+.` | step between runs/turns |
| `alt+m` | pick the model for the worker node |
| `alt+↑` / `alt+↓` | unfold / fold a node's children |

Arrow combos are all taken by the editor (`alt+left` walks up/zoom, `ctrl+left/right`
jump nodes), hence `alt+,`/`alt+.` for history.

---

## 10. Security

- Provider/Colab keys come from §1.3 — never in argv, never synced, never logged.
- Arbitrary execution (bash, compute, worker tools) is **client-only** and **never
  auto-run**; the server stores types as opaque strings.

---

## 11. Implementation order

1. **Foundations:** `NodeInternalData` (resolve column-vs-sidecar) + the NodeType
   registry incl. `Context()`; port the 7 existing display types, verify no visual change.
2. **bash:** registry entry + spinner glyph + async `runBash` + `alt+r` run/cancel +
   output band + `alt+e` reader + run history + markdown export + `Context()` = output.
3. **The single-line metadata seam** (`┊ …`) shared by bash/compute/worker.
4. **worker:** `AgentProvider` interface + Pi RPC adapter + `alt+r` running over children
   `Context()` + `AgentResult` (summary/actions/diff) to `InternalData` (no nodes) +
   details page (§7) + `Context()` = summary + `alt+m`.
5. **live query:** parser + lazy live-mirror + red-on-error + cycle guard + `Context()`.
6. **ultracode / ultraloop** render-time keyword animations + the ultracode pipeline +
   the ultraloop self-refinement loop + headless `lflow agent run`.
7. **Temp Space:** ephemeral second outline region below the footer (in-memory only).
8. **compute:** `→` NL node + `alt+r` snippet generation to `.pi/snippets/` + `alt+t`
   toggle + the code-block renderer; **then later** local Python kernel + footer session +
   Colab OAuth/remote kernel.
9. **auth:** `lflow auth <provider>` + the credential store.

Out of scope here: `lflow node list --depth` is a separate CLI bug fix — see
[node-list](../node-list/node-list.md).
