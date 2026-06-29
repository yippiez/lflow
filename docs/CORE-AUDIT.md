# Core audit + refactor plan — node-type extensibility & the agent node

Status: audit only (no code changed). Written 2026-06-19 after the agent-workflow
series, to answer two worries: (1) the agent node's UI/UX feels off, and (2) the
core must stay strong as **many** more node types arrive.

Audit against the standard you set: a new node type should be **self-contained**
(one place), **uniform** (reuses shared interaction, not bespoke modes/keys),
**clean** (one consistent data shape), and **thin** (small declarative render).

---

## 1. What the core gets right today

- **Everything is a node.** One `nodes` table, one tree, one renderer. New types
  need no migration. This is the load-bearing idea and it still holds.
- **The registry exists.** `pkg/tui/editor/registry.go` declares each type once:
  `key, label, sign, glyph, render, renderM, inlineEditable, tempOnly, expand, run`.
  Bullets/todo/headings/quote/code/json fit almost entirely inside it.
- **Two clean hooks already prove the pattern:** `run(m,it) tea.Cmd` (alt+r) and
  `expand(m,it)` (alt+e). A type contributes behavior by filling a field.

This is a good spine. The problem is everything the richer types need that the
spine *doesn't* offer yet — so they reach around it.

## 2. The five seams a node type touches — and where they leak

| Seam | Clean hook? | Reality today |
|------|-------------|---------------|
| Glyph / inline body | ✅ `glyph/render/renderM` | per-type render funcs are genuinely self-contained |
| Run an action | ✅ `run` | bash/query/worker all dispatch through it |
| Expanded view | ⚠️ `expand` exists… | …but it sets a **hardcoded central mode** (`modeJSON`/`modeAgent`/`modeSteer`) with a hand-written handler + `viewX` in the big switch |
| Per-node state | ❌ none | each type adds **its own `map[string]…` on `Model`** |
| Persistence | ❌ none | rich state is **ephemeral in-memory**; no per-node typed data |

Concrete leak counts (grepped):

- **Type-switches outside the registry:** `render.go` (Query, Worker, Code,
  headings, quote), `editor.go` (Worker harvest hook, Worker band-skip, temp
  default), `model.go`, `temp.go`. ~15 sites that say "if this type, do X".
- **Bespoke modes:** `modeJSON`, `modeAgent`, `modeSteer` — each a top-level enum
  value with its own key handler and `view*` function wired into the central
  `update`/`View` switches. The registry has **no way to contribute a mode**, so
  every rich view is hand-wired in the core.
- **Per-type state on `Model`:** `runOut/runCancel/runCh` (bash+worker),
  `voiceRec/voiceEnv/voiceDur` (voice), `queryRunAt` (query), and for the worker
  **six** maps: `workerUsage/workerDeliverable/workerAction/workerActions/
  workerStatus/workerSteer`. Every new stateful type grows the central struct.
- **No `NodeInternalData`.** CLAUDE.md already flags this blob as "planned, not
  implemented." Its absence is *why* state lives in `Model` maps and nothing
  rich survives a reload.

## 3. Case study: the agent node (why it "feels off")

For **one** type the worker added: 6 `Model` maps, 2 modes (`modeAgent`,
`modeSteer`), ~6 central key touch-points (`alt+r` last-agent branch, `alt+s`,
`alt+shift+s`, `Enter` harvest hook, `s` title-lock interception, `alt+e`→view),
a ~100-line `viewAgent`, an outline-composer `viewSteer`, and a subprocess
runtime (stdin/stdout/stderr goroutines, steering channel, status lifecycle)
living in `worker.go` + `agent.go`.

Two distinct problems:

- **Architecture:** if every future type costs this much central surface,
  `editor.go` becomes the bottleneck and the "one registry entry" promise dies.
  This is the thing you felt break.
- **UX ("I like bullets but working with it isn't good"):** the agent is operated
  through a *different grammar* than a bullet — special keys, a full-panel mode to
  see/steer, a query-in-name / context-in-children / output-by-harvest split you
  have to hold in your head. It reads as a styled bullet but doesn't *work* like
  one. The visuals match pchain; the **interaction model** is what's missing.

## 4. Stress test against the roadmap

Your near-term list and what each demands the core *not yet have*:

- **Node links** (`→ <target>`, alt+g jump): a lightweight reference distinct from
  mirror (which embeds a live subtree). Needs a **link attribute + jump** as a
  core primitive, plus `→` render. Today only `mirror_of` exists.
- **Molecule (SMILES → 2D/3D), CAD (OpenSCAD, LLM-editable), drawings, images:**
  per-node **typed payload** (SMILES string, SCAD source, drawing/image data) and
  an **expanded viewer that can launch out-of-process**, not just inline Go. Today
  `expand` is inline-only and there's nowhere to store the payload.
- **Python / compute sessions, live/streaming nodes:** a shared **job/stream
  runtime** (spawn, stream, cancel, status, output) — exactly what
  bash and worker each re-implement separately right now.
- **ultraloop/ultracode modifiers, skills on agents:** per-node **config/modifier
  data** layered on a base type — again needs typed per-node data + a way to
  compose behavior.
- **Cross-device sync (deferred, to be re-added later):** rich per-node data must
  have a **defined synced vs ephemeral boundary** (SMILES/SCAD/drawing = synced;
  run output/viewer cache = ephemeral). Today the boundary is implicit (whatever
  is/ isn't a column).

Every one of these wants the same four missing capabilities: **typed per-node
data**, **a registry-contributed view**, **a shared runtime**, and a **reference
primitive**. Build those once and the list gets cheap; keep hand-wiring and each
one is another agent-sized incision.

## 5. Refactor plan (toward a strong core)

Ordered so the foundation lands before more node types. None of this changes
behavior the user sees first; it relocates where types plug in.

**P1 — `NodeInternalData`: typed per-node payload.** Add the planned generic blob
(persisted JSON column) + an ephemeral in-memory sibling. Types read/write their
data here instead of `Model` maps. SMILES, SCAD source, link target, agent
deliverable, query results → persisted; run output, viewer caches, live activity
→ ephemeral. Defines the sync boundary explicitly. *Removes the per-type-map
growth on `Model`.*

**P2 — Registry-contributed views (kill bespoke modes).** Replace
`modeJSON/modeAgent/modeSteer/…` with one generic `modeNodeView` driven by a
`view` capability on the registry: a small interface `{ Init, Update(key), View }`
the type implements. The central switch becomes a single dispatch to "the active
node view." Adding a molecule/CAD/drawing viewer = implement the interface in the
type's file; **zero central edits.** Inline-only invariant preserved (these render
into the editor region, never the alt-screen).

**P3 — Shared job/stream runtime.** Extract bash+worker's spawn/stream/cancel/
status into one reusable runner keyed by node uuid, writing to `NodeInternalData`
(ephemeral). Worker steering = the runner's "send" + "alive" surface. New compute/
python/live nodes reuse it. *Collapses `runOut/runCh/runCancel` + the six worker
maps into one runtime.*

**P4 — Reference primitive (links).** Add a link attribute (separate from mirror)
with `→ <target name>` render and `alt+g` jump, as a core feature usable by any
node. Mirror stays "embed live subtree"; link stays "point at, jump to."

**P5 — External viewer kind.** A view capability variant that opens a node's
payload in an out-of-process viewer (for molecule/CAD/drawing). Keeps the TUI
thin; rich rendering happens where it can.

**P6 — Agent node, refit + UX.** Once P1–P3 exist, the worker becomes: a type with
`run` (alt+r, never auto), a contributed `view` (observe+steer), and its state in
`NodeInternalData`. Then fix the *interaction* so it feels like a bullet:
re-evaluate the alt+s/alt+shift+s/s gesture spread, and the
query(name)/context(children)/output(harvest) grammar, against "operate it like a
bullet." (Separate UX pass — worth its own questions/iteration.)

## 6. Sequencing & what to do about the agent *now*

1. **P1 + P2 first** — they're the spine every future node sits on; do them before
   adding molecule/CAD/etc.
2. **P3** when the second runtime-y node (python) is imminent.
3. **P4/P5** as those features come up.
4. **Agent (P6):** don't keep extending it on the current scaffolding. Hold new
   agent UX work until P1/P2 land, *or* do a small contained simplification now
   (fewer modes/keys) if it's getting in your way day-to-day.

## 7. Invariants to preserve (do not regress)

From CLAUDE.md / ADR: everything-is-a-node; no markup leaks into stored text
(outline in/out, never markdown); inline scrollback only — **never** the
alt-screen (lint-enforced); **never auto-run** runnable nodes anywhere (alt+r
only); secrets, view-state, the Temporary Domain contents-vs-structure, and binary
files follow their defined sync rules. The refactor must make these *easier* to
hold (one runtime, one data path), not introduce new exceptions.

---

### One-line summary

The "everything is a node + registry" core is right, but rich types currently
escape it through bespoke modes, per-type `Model` maps, and missing per-node data.
Land **typed node data (P1)** and **registry-contributed views (P2)** first; then
the agent — and molecule/CAD/drawing/python/links — all plug in as self-contained,
thin, uniform types instead of central incisions.
