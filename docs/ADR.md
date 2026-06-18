# Agent Decision Record (ADR)

This is an append-only LOG of decisions made while building lflow — both autonomous
agent calls and user-directed ones. It exists so the next agent can see *why* the
code looks the way it does and not re-litigate settled choices. **Append new entries
at the BOTTOM (newest last).** Keep each entry to the minimal shape below — title,
why, when. Do not add sections, tables, or prose beyond it. If a later decision
reverses an earlier one, write a NEW entry that says so; never edit history.

Entry format:

```
---
title: <short decision title>

Why
<1-3 sentences: the reasoning / problem it solved / alternatives rejected>

When
<date — and commit hash(es) if applicable>
---
```

---
title: Keep dnote's bones, adapt rather than rewrite

Why
lflow forked dnote for its SQLite backend and USN sync engine; rewriting from scratch was wasteful. Rule: adapt what fits, and ask before removing anything too hard to adapt.

When
2026-06-07 — d1f8af3, c369ce9, 2188ac9, b2cba56, c66d176, 8663a38, 4b7f2c9
---

---
title: Everything is a node with a free-string type

Why
A single unified node model (no books/notes split) means new node types are just a new value in the free-string `nodes.type` column and need no DB migration. dnote books became h1 roots, notes became children.

When
2026-06-12 — cd4b8d6, 1706337
---

---
title: Inline editor draws in scrollback, never the alternate screen

Why
The editor renders into the terminal scrollback so output stays scriptable and the styled outline survives quitting; the alt-screen would erase it. Enforced by an ast-grep lint rule.

When
2026-06-12 — 899daa7, 9ae9363
---

---
title: Debug output to stderr so stdout stays pipeable

Why
Commands are one-shot and pipe-friendly (`make bench 2>&1 | lflow node add`), so anything non-data must not pollute stdout.

When
2026-06-12 — 07bd92d
---

---
title: ast-grep lint rules encode design invariants

Why
Hostile multi-agent break-testing kept reintroducing banned patterns. Lint rules pin them down: no alt-screen, no direct sql.Open, no fmt.Print in libs, no panic in cmd.

When
2026-06-12 — 9ae9363
---

---
title: Workflowy is one optional backend, pull-only

Why
Local SQLite is the source of truth; Workflowy hits are pulled in as read-only mirrors, not a full two-way sync. The user repeatedly corrected the agent's over-reach here.

When
2026-06-12 — 57a1a2d, b90b256
---

---
title: Mirror cadence is a constant, no timer glyph or --every flag

Why
Visible mirrors refresh every 5s, hidden ones every 60s, plus once on appearance. An earlier per-node countdown timer glyph (5)→(4) was dropped — "just the diamond." Cadence is an implementation detail, not user-facing.

When
2026-06-12 — b90b256 (timer-glyph idea dropped, never shipped)
---

---
title: CLI minimalism — no aliases, no examples, no help subcommand

Why
Greenfield project, so no legacy aliases. Full command names, grouped command tree (node/server), a light custom help renderer reachable only via --help. Overlapping commands/flags get merged or deleted.

When
2026-06-12 — 671e70b, 3fdfd59, b5ba7ec, 89c235b, f5437f1, 3cdc1be, 5a04053, f3945e8
---

---
title: Environment settings live in the config file, not flags

Why
Settings that describe the environment (dbPath, workflowy.apiKey) belong in the settings file at ~/.lflow/settings.json, not the command line. The --dbPath flag was removed.

When
2026-06-12, 2026-06-14 — 4de4101, c4f4eb7
---

---
title: Output uses arrows and middots, never emoji ticks

Why
"DO NOT USE EMOJIS LIKE TICK, only arrows." Output uses `→ … · …`; no ✓/✗, no parenthesized explanations.

When
2026-06-12 — 3fdfd59
---

---
title: Selection marks only the bullet; text never recolors

Why
The selected row turns its bullet red; the cursor is a reverse-video block that inverts the cell beneath it. Text keeps its own color so styling is never lost to selection. Quit View ends with `\n` so the styled outline stays in scrollback.

When
2026-06-12 — 6e37de3, 6383a85
---

---
title: Long rows wrap, never truncate; cursor/viewport are line-aware

Why
Truncation hid content and pushed the cursor off-screen. Rows soft-wrap with a hanging indent and continuous tree rails; the long fix tail came from hostile tmux break-testing, each fix its own commit + e2e test.

When
2026-06-13 to 2026-06-14 — 038b1fc, 2f95574…f592141, 1de3bf1, 2933fa3, 7860e46
---

---
title: Sanitize pasted CRLF and control characters

Why
Raw control bytes and CRLF from paste created ghost nodes and corrupted render. Paste is sanitized; lines that sanitize to empty are dropped.

When
2026-06-13 — 01e0ced, 8e8972c, f0579d1, b305576, 599aa87
---

---
title: Drop inline **bold**/*italic* markup — styling is per-node only

Why
Inline markers leak into the stored name, FTS search, and stdout/export, and per-character styling would shift colors and cost too much to maintain. Styling is a per-node attribute in `item.style` (e.g. `bold,color:blue`). Asterisks now render literally. Durable, load-bearing rule.

When
2026-06-14 — d2a84f0, 4b3b3a5, a962798
---

---
title: Dates are format-detected chips with no brackets stored

Why
Recognized date formats render as background-colored chips automatically; nothing bracketed is stored in the node name, so no markup leaks. The chip survives even when the node text is colored. ctrl+t converts the date phrase at the caret. Turkish + English both first-class.

When
2026-06-13, 2026-06-14 — 8bd710f, a962798, b0b5b1d, b794f12, 0009e03
---

---
title: A node's note renders as a tinted band beneath it

Why
The only prior way to see a note was to zoom in. The band shows the note under the node and becomes the edit surface when zoomed; the tree line runs left of the note and curves into its corner to meet children. Chosen via design-by-images iteration on tmux PNG mockups.

When
2026-06-15 — c29b433, 3c53e12
---

---
title: Rename node "layout" → "type"; unify /style and /type pickers

Why
"type" is the accurate name for what the property is, and the rename was the right window to introduce a descriptor table while there were only ~7 types. Styling and type selection collapsed into two pickers.

When
2026-06-17 — 822f6b7
---

---
title: NodeType registry — node types are one descriptor

Why
Behavior was scattered across glyphFor/renderBody, typeOrder/typeLabels, the markdown export switch and ValidTypes. One `nodeType` descriptor table + `typeOf(key)` (falling back to bullets for unknown keys) makes types extensible — the user's top priority. Existing types ported with zero visual change.

When
2026-06-17 — 786330a, 4977af4
---

---
title: Glyphs are plain Unicode text symbols, no emoji

Why
"no emoji — every sign/glyph is a simple Unicode text symbol, avoid the media-control glyphs." Signs are ○ ◆ ▸ ● ▁▂▇ ⌕ $ {} → ◌; never ✓/✗ or ▶⏸⏹⏺ or VS16 selectors.

When
2026-06-17 — 786330a (cross-cutting)
---

---
title: Animate ultracode / ultraloop keywords as typed

Why
The keywords shimmer (ultracode purple with a moving white shim, pulsing) as a visible affordance for forthcoming orchestration behavior, which is deferred. ultraloop can appear anywhere in a sentence.

When
2026-06-17 — 35e4fcc
---

---
title: Local-only collapsed column (schema 19); never synced

Why
Fold state is local view-state, not content — it must persist across restarts but never sync. It is the model for all later derived/view state. A save-time write was added as a backstop to the immediate write.

When
2026-06-17 — 5a90f18, 0b2af6f, 382f14e
---

---
title: json node is the reference pattern for rich types

Why
json proved the registry pattern all rich types follow: `{}` sign, truncated inline preview, red "JSON parsing failed" when invalid, inline-non-editable, custom alt+e full-panel pretty/syntax-colored editor.

When
2026-06-17 — 939f6f8, 72c7a04
---

---
title: Run output is ephemeral in-memory, not persisted

Why
The user demanded "no changes to the database" per feature. Bash run output is kept ephemerally in in-memory maps keyed by uuid (runOut/runCh/runCancel in pkg/tui/editor/bash.go) and is never persisted or synced. A generic per-node NodeInternalData JSON blob was discussed as an intended store but never implemented; no such column or struct exists in the code.

When
2026-06-18 — 38b84e6 (output is ephemeral in-memory, never persisted/synced)
---

---
title: Runnable nodes never auto-run

Why
bash/query/worker/voice execute only on alt+r (a second alt+r cancels) — never on load, sync, paste, or import. The server stores runnable types as opaque strings and never executes them.

When
2026-06-18 — 38b84e6, b2aea1a, c5f9957, e7554ef, 1bcb449
---

---
title: bash node shows a spinner, no checkmark or cross

Why
Rendered `○ $ command` (bullet kept, `$` gray prefix). A pulsing ◐ shows while running, reverting to plain ○ when done. "no checkmark, no error mark." Output streams into a self-contained gray band (stdout white, stderr red), capped ~3 lines with an `exit N · ⋯ N more · alt+e expand` footer. Command text stays gray — only the output box is colored.

When
2026-06-18 — 38b84e6
---

---
title: Drop the NodeGhostLand results pane — output is just a node

Why
A second results pane below the footer for bash/agent output was designed and mocked, then cut: "I think it should just be a node instead of this custom results thing." The plan moved from child-nodes → one-node-per-run → an intended per-node InternalData blob, but the shipped code keeps output ephemeral in-memory and persists nothing.

When
2026-06-18 — 38b84e6 (ghost-pane idea dropped, never shipped)
---

---
title: query node searches the notes DB and mirrors hits, not ripgrep over the codebase

Why
First implemented as a codebase ripgrep query, then changed to query the lflow node DB and live-mirror matching nodes — the semantics the user actually specified. Hits are first-order read-only mirror children reconciled by source UUID (update in place, append new, remove stale, regenerate a shift+tab'd-out child). Refreshes lazily when visible. Query language: :AND :OR nesting, :IS, :BEFORE/:AFTER, `>` drill-down; syntax errors render red.

When
2026-06-18 — b2aea1a (codebase, superseded), c5f9957 (notes, current)
---

---
title: Derived children are a flat first-order list, reconciled by stable ID

Why
Query hits and any generated children are a bounded list of direct children, never recursively expanded. Reconcile by stable source ID, not wipe-and-rebuild, so user edits and removals are respected.

When
2026-06-18 — c5f9957
---

---
title: Binary/large content lives in local files, never the synced row

Why
Voice audio is a local .wav under ~/.local/share/lflow/voice/<uuid>.wav; the amplitude envelope is recomputed from the .wav on demand (parseWavEnvelope) and cached in an in-memory map (m.voiceEnv), never stored in the synced node. Same rule for snippets and agent transcripts — only a local file, nothing binary in the synced row.

When
2026-06-18 — 1bcb449, 42b2a7f, e8831ed
---

---
title: worker is a task-oriented Pi agent invoked directly via exec

Why
The worker runs a coding agent (Pi) with the worker node's own text (it.name) as the task. pi is launched directly via exec.CommandContext `--mode rpc --no-session --approve --no-extensions` with lflow's own `finish_worker` tool loaded. A provider abstraction (an AgentProvider Go interface) and a children-Context() concatenation were considered for later but are not implemented; the code calls pi directly with the single node's text.

When
2026-06-18 — e7554ef, b3a7ccb, 9697ab1
---

---
title: worker stays one line, produces no outline nodes, no → prefix

Why
Big reversal: originally an inline tool-call transcript with the answer appended as a typed child. Cut to a single line `┊ model · time · ↑↓tokens · $ · +/-` with a live one-line summary; the deliverable is a single finish_worker markdown and details live on the alt+e page. The `→` prefix was reserved for compute nodes, so worker has no prefix.

When
2026-06-18 — 9697ab1
---

---
title: Secrets never enter argv, history, the synced DB, or logs

Why
Secrets are read from local config stores, never synced or logged: Workflowy API credentials in ~/.lflow/settings.json (WorkflowyConfig) and Pi settings in ~/.pi/agent/settings.json. A consolidated ~/.config/lflow/credentials.json mode 0600 is the intended target for a planned `lflow auth` feature but is not yet created or read anywhere in the code. The never-sync, never-log rule holds regardless of store.

When
2026-06-18 — e7554ef (cross-cutting; credentials.json planned, not implemented)
---

---
title: Temporary Domain is ephemeral — db=nil so it never persists or syncs

Why
A second outline region for scratch notes and throwaway agents that behaves like the main outline but lives in memory only; its tree has a nil db so save() is a no-op. It is marked by a plain muted-gray "Temporary Domain" text divider.

When
2026-06-18 — 49e7a7d
---

---
title: Temporary Domain is an always-visible panel reached by go-down, not an alt+t window

Why
Pivoted from an alt+t window-swap to an always-visible panel below the divider, entered by navigating down past the outline. The fancy dashed-box divider was reduced to plain text at the user's request; the divider sits right after the notes with no gap.

When
2026-06-18 — b3a7ccb, acaed3f, 15305e9
---

---
title: Enter on an expanded parent creates an empty first child, not a sibling

Why
With children visible, pressing enter on the parent should descend into the tree (new first child) rather than add a sibling next to it.

When
2026-06-18 — f8172d0
---

---
title: compute node (NL→snippet) is spec'd but deferred; → reserved for it

Why
A `→`-prefixed natural-language snippet description that generates code on alt+r. Explicitly deferred "until bash/query/worker feel right"; only the `→` prefix is reserved, taken back from the worker. No implementation commit yet.

When
2026-06-18 — spec f6e75e2, plan 205880c (no impl)
---

---
title: Trim fork baggage; design artifacts live outside the repo

Why
Stripped dnote's watcher, makefile, docker host files, web assets, GitHub templates and Apache headers; moved docs under docs/ and renamed the cli package to tui. Design reports/ADRs/images for review live in /tmp/lflow-design/, never the repo — only capture tooling stays in-repo.

When
2026-06-14 — 74fc77e, 0c2d11b, 2e32ed8, 8ad5cea, f84c9d4, c873b4b, df9cb83, df1d599, 876caeb, ab1dd92, c12ab25
---

---
title: Temporary Domain divider is the status bar itself

Why
The always-visible temp panel needed a separator from the main notes. Rather than draw a second `╌╌ temp ╌╌` rule, the status bar (root · N/M · model · thinking) is rendered mid-frame as the divider: notes above it, temp below. One line does both jobs; the frame is padded to a constant height so the inline renderer never strands stale lines with the bar mid-frame.

When
2026-06-18 — b706044 (supersedes the brief tempDivider rule in 15305e9)
---

---
title: /type picker is searchable and bounded

Why
With 11 node types the picker ran off the bottom of short windows and the status bar with it, and there was no way to filter. It now filters as you type (filteredTypes + a "type: <query>" header), reserves body rows for itself, and scrolls its option list (max 8 visible) so it and the status bar always fit. Also dropped parenthetical type labels ("Worker (Pi agent)" → "Worker").

When
2026-06-18
---

---
title: Query results are persisted real mirrors, and search includes unsaved nodes

Why
Live-query results were ephemeral (the `derived` flag skipped them on save), so they reset on every relaunch and only ever found saved nodes. Now the result mirrors are REAL persisted nodes, reconciled in place on each run (keep matches, add new, tombstone stale — idempotent), so they survive a relaunch; and the search merges the in-memory tree with the DB FTS so unsaved/just-typed nodes are found too. Query nodes also show "updated <relative>" from an in-memory last-run timestamp (resets per session). This reverses the earlier ephemeral-derived-mirror decision.

When
2026-06-18 — supersedes c5f9957 (ripgrep→notes) and the derived-mirror approach
---

---
title: lflow and pchain will merge — keep the agent model aligned

Why
pchain (a pi coding-agent extension, see work2/pchain) and lflow (this TUI, plus a planned mobile app and app server) are converging products and will be merged. Design the worker/agent surface so the two stay compatible: a lflow worker node is effectively the face of a pchain-style agent job (task, status, usage ↑in ↓out $cost, lastActions/activity stream, deliverable). lflow already mirrors pchain's compact job line and colored tool-call stream. Decisions touching agents should preserve this alignment.

When
2026-06-18 — strategic (no commit; future reference)
---

---
title: Temporary Domain gains 7-day retention — persisted with a TTL, no longer session-ephemeral

Why
The temp space is the agent/worker surface and must keep work (and agent "receipts") across restarts, but must not clutter the permanent notebook. Decision: temp content persists locally with a 7-day retention window, then is cleaned up — a TTL store, not the session-only db=nil model. This supersedes "Temporary Domain is ephemeral — db=nil". Open: exact store (separate agent/temp store vs a flagged region of the notes db), whether temp ever syncs (lean: local-only, not synced).

When
2026-06-18 — design decision (supersedes the db=nil temp model; not yet implemented)
---

---
title: Agent/worker workflow — temp is the lab, main is the notebook

Why
Brainstorm direction for integrating agents, kept aligned with pchain (lab=temp, notebook=main; only distilled results cross the border). Decisions so far:
- Temp is the agent surface: a new node created in temp DEFAULTS to a worker; some node types are temp-only (lean: worker is temp-only).
- Context = the worker node's MESSAGE (its text) + its NOTE + its CHILDREN subtree. There is no separate "attach context" gesture — you add context by adding children, including mirroring any node (or another worker) in as a child, so "any node can be context" holds. (This corrects an earlier over-complication that made context separate references.)
- Output is NOT a child (input-as-children + output-as-children was the clash; resolved by keeping children = INPUT only). The deliverable is held by the worker, viewable via expand (alt+e), and crosses into main only by pressing Enter on a finished worker, which MATERIALIZES a copy under Root. Spent worker stays as a re-runnable receipt.
- Workers are ordinary movable nodes but position is behavior-free (cosmetic); temp reads as a flat job list, matching pchain's job model.
Open: whether the harvested result materializes as a parsed subtree vs a flat note; whether query nodes are also temp-only.

When
2026-06-18 — brainstorm/design (not yet implemented)
---

---
title: Temp persistence = a second "Temp" root in the notes db with a 7-day TTL sweep

Why
Resolves the open store question from the 7-day-retention decision. Instead of a separate store or the db=nil in-memory tree, the notes db gets TWO top-level roots: "Root" (the notebook) and "Temp" (the lab), both ordinary roots — the Temp root is just a second Root. Temp content is the subtree under the Temp root. Reuse existing columns: added_on = created_at, edited_on = changed_at — no schema change. On startup, sweep the Temp subtree and delete top-level entries unchanged for 7 days. Granularity: expire each top-level Temp entry as a UNIT, only when ALL of its UNIQUE descendant nodes are >7d old (newest unique descendant edited_on > 7d). "Unique" matters because mirror-in-mirror can make the naive descendant walk infinite — dedupe real nodes and guard cycles. Touching anything inside keeps the whole entry alive. Deleting a node that is a mirror's source leaves the mirror in place (existing mirror behavior). Sync: the Temp subtree IS synced, same as Root (so agent work reaches the mobile app/server); pulling Workflowy nodes into Temp is a future feature. This collapses the db=nil / separate tempTree / mainStash-swap machinery into "focus Root vs focus Temp" on one persisted, synced tree.

When
2026-06-18 — design decision (refines the temp 7-day-retention entry; not yet implemented)
---
