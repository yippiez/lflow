# lflow PRD library

Retro-authored PRDs, one per feature epoch, in historical order. lflow grew through
chat with no PRDs; these reconstruct the intent, design, and pivots behind each
feature, grounded in the git history and the source under `pkg/tui/editor/`.

`00-template.md` is the canonical template for all new PRDs — copy it to
`NN-<slug>.md` and follow its section structure exactly.

## Index

- [00-template](00-template.md) — the reusable PRD template (copy this for new work).
- [01-fork-rename](01-fork-rename.md) — reshape the dnote fork into lflow; rename module, branch, headers.
- [02-outline-editor-core](02-outline-editor-core.md) — unified node model + best-match CLI + inline scrollback editor.
- [03-mirrors-and-workflowy](03-mirrors-and-workflowy.md) — red `◆` mirror nodes (the Workflowy pull was later removed).
- [04-cli-minimalism](04-cli-minimalism.md) — grouped command tree, no aliases, light help, config-only settings.
- [05-wysiwyg-rows](05-wysiwyg-rows.md) — WYSIWYG rows, gray bullets, block cursor, node links, date pills.
- [06-editor-hardening](06-editor-hardening.md) — wrapping/cursor/resize/paste fixes from hostile tmux break-testing.
- [07-node-styling](07-node-styling.md) — per-node color/bold/italic/underline/strikethrough; inline markup removed.
- [08-date-chips](08-date-chips.md) — format-detected date chips with no stored brackets.
- [09-notes-band](09-notes-band.md) — a node's note shown as a tinted band beneath it.
- [10-node-type-registry](10-node-type-registry.md) — collapse type switches into one descriptor; layout to type.
- [11-collapse-persistence-and-json-node](11-collapse-persistence-and-json-node.md) — local-only collapsed column + the reference JSON node.
- [12-keyword-animation](12-keyword-animation.md) — ultracode / ultraloop render-time shimmer.
- [13-bash-node](13-bash-node.md) — `○ $ cmd` node, alt+r runs, streamed output band, no checkmarks.
- [14-query-node](14-query-node.md) — live notes query mirroring hits as first-order children.
- [15-voice-node](15-voice-node.md) — record/play voice note with a waveform; audio in local files.
- [16-worker-node](16-worker-node.md) — Pi coding agent via RPC, one line, cost chip, provider interface.
- [17-compute-node](17-compute-node.md) — NL to code-snippet node (spec'd, deferred).
- [18-temporary-domain](18-temporary-domain.md) — always-visible ephemeral scratch outline panel.
- [19-repo-restructure](19-repo-restructure.md) — trim dnote machinery, regroup packages, rename cli to tui.
