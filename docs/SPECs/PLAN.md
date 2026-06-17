# lflow node system — master build plan

The goal: support **hundreds of node types** where each type is a **drop-in file**, and
every type shares the same operations (optional sign, glyph, inline render, custom `alt+e`
expanded view, run, context, export). This plan sequences the work; we implement it
**case by case**, each a small, tmux-verified, installed change.

---

## 0. Foundation — the NodeType registry (do first)

**Why:** today a type's behavior is smeared across `switch it.typ` in ≥4 files
(`render.go` glyphFor + renderBody, `editor.go` typeOrder/typeLabels/edit-guards/alt+e,
`outline.go` export, `database/node.go` ValidTypes). Adding a type = shotgun surgery and 3
parallel lists to hand-sync. That does not scale.

**The descriptor** — one struct; one `map[string]NodeType`; ideally **one file per type**
that self-registers in `init()`, so the core files never change again:

```go
type NodeType struct {
    Key, Label string                                   // "voice" + /type picker label

    Sign   string                                       // optional prefix: "$ ", "{} ", "⌕ ", 🎙 ; "" = none
    Glyph  func(it *item) (glyph, color string)         // nil → ○ ; todo box, mirror ◆, heading digit, ● REC
    Render func(it *item, name string, sel bool) string // nil → default text; json/voice/query override

    InlineEditable bool                                 // false → typing is a no-op (json/voice/query) — ONE flag
    Expand func(m *Model, it *item) tea.Cmd             // optional custom alt+e view; nil → none
    Run    func(m *Model, it *item) tea.Cmd             // optional alt+r action (bash/voice/query/worker)
    Context func(it *item) string                       // node's context string (worker input)

    Markdown func(name string) []string                 // export; nil → "- name"
}
func typeOf(key string) *NodeType   // never nil; unknown → bullets
var registry = map[string]NodeType{}
```

Every want maps to a field: **optional sign → `Sign`**, **glyph/circle → `Glyph`**,
**custom alt+e → `Expand`**, **shared ops → `Run`/`Context`/`Markdown`/`InlineEditable`**.

**Migration:** port the 8 existing types (bullets, todo, h1-3, code, quote, json) into
descriptors; rewrite glyphFor/renderBody/typeOrder/typeLabels/ValidTypes/export/edit-guards
as registry lookups; **verify zero visual change** (editor tests + tmux before/after diff).
After this, every new type below is a drop-in file. **Effort:** medium, contained,
test-backed. **This is step 1 of the roadmap (§5).**

---

## 1. Cross-cutting invariants

- **Everything is a node.** `nodes.type` is a free string → adding a type needs **no DB
  migration**.
- **Derived/generated children are FIRST-ORDER ONLY.** Nodes nest infinitely, so anything
  generated (query hits, agent output) is a **bounded, flat list of direct children** —
  never recursively expanded. This is the nesting limit you asked for; it's enforced per
  feature and shared via a helper.
- **Big/binary content lives in local files, never the synced DB.** Audio, snippets, agent
  transcripts → `~/.local/share/lflow/<kind>/<uuid>.<ext>`; the node stores only a **ref +
  small metadata**. (Needs the InternalData primitive — see §5 step 0b.)
- **Local view-state vs synced content.** Like `collapsed` (already local-only), derived
  state and view state never sync.
- **Secrets never** in argv / shell history / synced DB / logs (Pi keys, Colab) — local
  `~/.config/lflow/credentials.json` 0600.
- **Universal operations** are dispatched generically from the registry, so `alt+e`,
  `alt+r`, export, edit-blocking work for any type without touching core files.
- **No emoji.** Every sign/glyph is a **simple Unicode text symbol** (geometric block:
  `○ ◆ ▸ ● ▁▂▇ ⌕ $ {} → ◌`), never an emoji and never a VS16 selector. Avoid the
  media-control glyphs `▶ ⏸ ⏹ ⏺` — most terminals render those as colored emoji; use the
  plain triangles/circles `▸ ▹ ● ○ ■` instead.

---

## 2. Node types (case by case)

Format per type: **Purpose · Display · Storage · Tech stack · Edge cases · Registry fields**.

### 2.1 json — DONE (reference implementation)
The proven pattern: `{}` sign, truncated preview, red ` · JSON parsing failed`,
inline-non-editable, `alt+e` full-panel pretty + syntax-colored editor. Everything below
follows this shape.

### 2.2 voice note — NEW
- **Purpose:** record a voice message; play it back.
- **Display (simple icons, no emoji):** a `▸` play sign + a **waveform of vertical bars of
  varying heights** (`▁▂▃▄▅▆▇█`) + duration, e.g. `○ ▸ ▁▂▅▇▆▃▂▁  0:12`. While recording: a
  red `●` + **live animated bars** (reuse the anim tick). While playing: `▹` and a position
  marker sweeping the bars. (`▸ ▹ ● ■` are plain text triangles/shapes — not the emoji
  media glyphs `▶⏸⏹⏺`.)
- **Storage:** audio file at `~/.local/share/lflow/voice/<uuid>.wav`; the node keeps a
  **ref + duration + a small amplitude envelope** (~40 ints for the bars) in InternalData.
  Binary never touches the DB or sync.
- **Tech stack:**
  - *Record:* detect an available CLI — `sox` (`rec`), `arecord` (ALSA), or `ffmpeg`;
    spawn on `alt+r`, stop on `alt+r` again (like bash run/cancel).
  - *Playback:* `play`(sox) / `aplay` / `paplay` / `ffplay`; spawn non-blocking, stop key.
  - *Waveform:* parse PCM WAV samples in Go (`encoding/binary`), downsample to N buckets →
    block-bar heights. No heavy deps.
- **Edge cases:** no recorder/player installed → dim "install sox/ffmpeg" message; mic
  permissions; max duration cap; delete the wav when the node is deleted; only one playback
  at a time; recording indicator must not block the UI (async, like bash).
- **Registry:** `Glyph` (`▸` / red `●` while recording), `Render` (bars+duration), `Run`
  (record/stop), `Expand` (alt+e → big waveform + play/scrub), `InlineEditable=false`,
  `Markdown` (link to the audio file).
- **Decisions:** WAV vs ogg; transcription later (Context() = transcript via a STT step?).

### 2.3 codebase live-query — NEW (mirrors + reconcile)
- **Purpose:** a query node whose source is the **codebase**; its hits appear as
  **read-only mirror children** that stay in sync as code changes.
- **Display:** `⌕ <query> · N hits`; each first-order child = a mirror, name like
  `path/file.go:42  matched line text` (red `◆` lflow mirror style).
- **Mirrors + reconcile (the edge cases you emphasized):**
  - Hits are **first-order children only** (a flat list — the nesting limit).
  - Each hit has a **stable ID**. Refresh = **reconcile by ID** (same engine as the node
    live-query): direct children that still match are **updated in place** (line number
    refreshed if the finding **moved**), missing hits **appended at bottom**, stale ones
    **removed**, and a child you moved out (shift+tab) is **regenerated at the bottom**.
  - "Checks first-order children have the correct IDs and mirrors them" → exactly the
    reconcile pass; any child whose ID is wrong/missing is re-mirrored.
- **Tech stack:** `ripgrep` (`rg --json`) if present, else `grep -rn`; parse to
  file:line:match. Lazy refresh on visible + dirty (debounced).
- **Edge cases:** cap result count (e.g. 200, log the truncation); respect `.gitignore`;
  skip binaries; **hit-ID stability** is the crux (content-hash of the matched line so a
  moved-but-unchanged line keeps its identity vs. file:line which breaks on every insert);
  the query node's own subtree must not match itself (loop guard); huge repos → time cap.
- **Registry:** `Render` (⌕ + hits), `Run` (re-run + reconcile), `Expand` (alt+e → full
  result list / jump-to-file), children derived + read-only.
- **Decisions:** hit ID = **content-hash** (survives moves) vs file:line (simpler);
  one query type with a **source selector** (lflow nodes ↔ codebase) vs two types.

### 2.4 worker — Pi coding agent — NEW (from spec)
- **Purpose:** a node whose text is a message to a coding agent (Pi); runs a turn over its
  **children's Context()**; **outputs nothing to the outline** — diff/actions/summary live
  in the `alt+e` **details page**; the node line shows `┊ model · time · ↑↓tokens · $ · +/-`.
- **Tech stack — grounded in the real impl** (`work2/pchain/pi/src/agents/manager.ts`,
  `extensions/pi-async-agents.ts`):
  - Spawn `pi --mode rpc --no-session --approve --tools <list,finish_worker>
    --append-system-prompt <ctx> --name <node> [--model <m>]` (plus `--no-extensions
    --no-skills --no-context-files` and a small finish-tool extension). Newline-delimited
    JSON over stdin/stdout.
  - **stdin commands:** `{id,type:"prompt",message}` (also steer/abort) — one JSON line.
  - **stdout events:** `agent_start` → `message_end {message}` → `tool_execution_start
    {toolName,args}` / `tool_execution_update` / `tool_result` / `tool_execution_end
    {result.details}` → `agent_end`.
  - **The worker's only deliverable is one `finish_worker` tool call** whose `markdown` is
    the answer; trailing assistant text is the literal `ASYNC_AGENT_DONE`. The system
    prompt forces this. → the node's single result + Context().
  - **The diff and +/- count come from `tool_execution_end` `result.details`** (unified
    diff of file-edit tools) — captured into the details page. Status comes from the
    structured `finish_worker` status (JSON, not text).
  - `AgentProvider` Go interface + Pi adapter normalizing those events. Keys via cli-auth.
- **Edge cases:** context bounded (first-order children, size cap, cycle-safe `↪`); never
  auto-run (alt+r only); cancel on re-run; invalid/over-budget handling; `alt+m` model.
  `ultracode`/`ultraloop` keyword animation is **already done**; their orchestration
  (staged pipeline / self-refinement loop) is a later layer.
- **Registry:** `Render` (cost line), `Run` (alt+r turn), `Expand` (alt+e details page),
  `Context`, `InlineEditable=true` (the message is editable).

### 2.5 compute — NL → snippet (from spec, deferred)
`→` natural-language description; `alt+r` generates a snippet into `.pi/snippets/`; `alt+t`
toggles NL ⇄ code; Python execution (local/Colab) later. Registry: `Sign="→ "`, `Run`
(generate), `Expand`/toggle.

### 2.6 bash — shell command (from spec)
`$` sign, real `bash -c` execution streamed (stdout white / stderr red), contained output
box + transparent footer line, run history in InternalData. Registry: `Sign="$ "`, `Run`,
`Expand` (full output reader).

---

## 3. Temporary Domain (a region, not a type)
A second outline region **below the footer**, behaving exactly like the main outline but
**ephemeral** (in-memory only, never persisted/synced) — scratch notes and throwaway
agents. Dotted-circle `◌` nodes.

- **No title.** Instead, the region is marked by a **muted-gray dashed outline below the
  footer** — a dashed box (e.g. `┄┄┄ / ┊ / ╌` in `cDim`) that is the **access affordance**:
  it's always visible under the footer, and navigating into it (or a key) drops the cursor
  inside. Empty → just the dashed frame; populated → the `◌` nodes sit inside the frame.

---

## 4. Tech-stack summary

| Feature | External tools | Go libs | Storage |
|---|---|---|---|
| registry | — | — | — (in-memory table) |
| json | — | encoding/json | node name |
| voice | sox / arecord / ffmpeg · play / aplay / ffplay | os/exec, encoding/binary | local .wav + envelope in InternalData |
| codebase query | ripgrep (fallback grep) | os/exec, encoding/json | derived first-order mirror children |
| worker (Pi) | `pi` CLI (RPC mode) | os/exec, encoding/json | AgentData in InternalData; keys in local creds |
| compute | python kernel / Colab (later) | os/exec, oauth2 loopback | .pi/snippets + InternalData |
| bash | /bin/bash | os/exec | run history in InternalData |

---

## 5. Sequenced roadmap (dependencies in order)

0. **NodeType registry** (port 8 types, verify no visual change). ← unlocks everything.
0b. **InternalData primitive** — resolve the open blocker (one `internal_data` DB column
    **vs** a local sidecar file) — needed by voice/bash/worker. Recommend a local sidecar
    for big/binary so the synced row stays small.
1. **Voice note** — type + waveform render + record/play + envelope. *(your first new case)*
2. **Codebase live-query** — query + first-order mirror reconcile + move-regeneration.
3. **Worker / Pi** — provider interface + Pi RPC adapter + details page + cost/±line.
4. **bash** + **compute** — port onto the registry.
5. **Temporary Domain** — ephemeral region.
6. **cli-auth** — `lflow auth <provider>` (gates Pi keys + Colab).
Later: ultracode/ultraloop orchestration; voice transcription; compute execution.

**Open decisions to settle as we hit each case:**
- 0b: InternalData column vs sidecar.
- 2.2: WAV vs ogg; recorder/player priority; transcription?
- 2.3: hit ID = content-hash vs file:line; one query type (source selector) vs two.
- 2.4: default model; Pi tool allowlist.

---

## How we proceed
Each numbered case = one small change: build → `go test` → tmux verify (`:memory:` or
isolated HOME) → install to `~/.local/bin/lflow` → you verify → commit. We start at **step
0 (registry)** unless you want to reorder.
