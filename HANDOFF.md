# HANDOFF — node/chip/file-editor plugin restructure

Handoff for the next agent. Everything below is the full context needed to continue the work. Read this first, then the files it cites.

## Goal (user-approved plan)

Make the editor a plugin-like system with **one file per thing**:

1. **One file per node type** — all node types eventually under `packages/nodes/` (one file each).
2. **One file per chip kind** — under `packages/chips/`.
3. **One file per file-editor type** (python, markdown, toml, json, rust, …) — under `packages/fileeditor/`.

Plus a feature already landed:

4. **`continueOnEnter` flag** — pressing Enter at the end of certain node types creates a sibling of the same type (not a bullet). Already true for todo, molecule; now also log, math, svg, html, line, bash. User explicitly rejected: fn, quote, class, comment, text, nlpcompute, code.

## Repo layout (layers)

Arch contract is pinned by `tests/arch/layers_test.go` (imports may only point downward; prefixes sorted by longest match — NOTE: `layerOf` iterates a map, see below).

- `packages/utils` (0), `packages/chips` (0, leaf vocabulary), `packages/mobile` (0)
- `packages/database` (1) — schema, `TypeXxx` string constants (`packages/database/node.go`), `TypeOrder` (canonical type ordering, `node.go:127`), chips table
- `packages/daemon`, `packages/nlp`, `packages/app`, `packages/outline` (2)
- `packages/editor` (3) — the outline editor (Model, view, render, chips, registry)
- `packages/filecodec` (2) — **DELETED, now `packages/fileeditor` (3)**
- `packages/fileeditor` (3) — the file editor: codecs + python/rust statement nodes
- `packages/nodes` (3) — pluggable node types
- `packages/cli` (4), `cmd` (4)

`layers_test.go` currently lists `packages/fileeditor: 3` (was `filecodec: 2`). The `layerOf` map-iteration nondeterminism (overlapping prefixes) is a latent bug if a sub-prefix entry is ever added — fix by iterating keys longest-first.

## State after 3 committed chunks (all green, `git log -3`)

### Commit 1 — `e318f39` continueOnEnter flag pass
`packages/editor/registry.go`: `continueOnEnter: true` added to log, bash, svg, html, math, line entries (todo/molecule already had it). Semantics live in `packages/editor/keys.go:323` and `:356` — the flag continues the type on Enter regardless of caret position (mid-line splits too). If end-of-line-only is wanted, add a caret-at-end check there.

### Commit 2 — `142ae3b` chips split
`packages/chips/`: `chips.go` (general: kind consts, `zoteroScheme`, `span`, `Chipify`), `tag.go` (`ReTag`, `TagSpans`, `TagsIn`), `date.go` (`ReISO`, `DateSpans`, `WordBound`, `Atoi`, `BuildDate`), `link.go` (`reLink`, `linkSpan`, `linkSpans`), `service.go` (unchanged: `Service`, `ServiceFor`, `ServiceDisplay`, `services`). Tests unchanged. Consumers: `packages/database/chip.go` (`ChipifyName`, `ChipDisplay`), `packages/editor/{chip,tags,date,service,link}.go`.

### Commit 3 — `c976fe0` fileeditor merge
`packages/filecodec/` moved into `packages/fileeditor/` (package renamed `fileeditor`):
- `codec.go` (FileCodec interface, `fileCodecs` registry, `SrcNode`, `ParseIntoDB`, `RenderFromDB`, `CodecForPath`, `CodecExts`, `insertForest`, `ensureTrailingNewline`)
- `lang.go` (`LangSpec`, `RenderCode`, `classifyMarkerBody`, `classifyComment`, `indentWidth`), `mathexpr.go`
- `python.go` — merged with old `packages/nodes/python.go`: `PythonSpec` + `pythonCodec` + `pythonKeywords` + ONE `init()` registering the codec AND the TypePython statement `NodePlugin`
- `rust.go` — same merge (`RustSpec` + `rustCodec` + `rustKeywords` + init registering codec + TypeRust plugin)
- `markdown.go`, `toml.go` (was tomlfile.go), `json.go` (was jsonfile.go)
- `nodes.go` — shared editor-side helpers: `isWordRune`, `keywordSpanColor`, `refToSrc`, `runCodeExport`
- All old filecodec tests + `span_color_test.go` (from nodes) moved here
- `editor.NodeCodeBodyTail(n NodeRef) string` added to `packages/editor/nodeplugin.go` (moved from nodes/python.go; `codeLineCount` → private `nodeCodeLineCount`), used by fn/class (nodes) and python/rust (fileeditor)
- `packages/cli/file/open.go`: `filecodec.` → `fileeditor.`
- `cmd/lflow/main.go`: added `_ "…/packages/fileeditor"` blank import (registers codecs + statement nodes); still has `_ "…/packages/nodes"`
- `packages/nodes/registry_parity_test.go`: blank-imports fileeditor (its registrations must count in the parity check)
- `packages/nodes/` now: class.go, comment.go, fn.go, text.go, nlpcompute.go (+ tests)

## The hard constraint discovered (READ BEFORE CONTINUING)

**All 69 editor test files are internal** (`package editor`), e.g. `editor_test.go:133` `newTestModel` builds `&Model{tree: t, …}` literals of unexported fields. `packages/nodes` imports `packages/editor` (for `RegisterNodePlugin`), so:

- Editor tests can NEVER import `packages/nodes` (proven: `import cycle not allowed in test`).
- If all core node types move to `packages/nodes/`, the editor's internal tests see ZERO registered types and the suite breaks with no in-package fix.

### The chosen escape (user decision): the `nodeapi` flip
Move the plugin API + registry into a new package `packages/nodeapi` (layer 3 — it imports `nlp` for `NodeCompute`; add its own `layers_test.go` entry or it inherits nothing — it needs an entry). Then:

- `packages/nodeapi` (NEW, leaf-ish): `NodeRef`, `NodeHost` (narrow, 5 methods: NodeStore/NodeDB/NodeFlash/NodeDepOK/NodeCompute), `NodeModel` (wide: extends NodeHost with ~110 one-line delegations to editor Model methods), `NodePlugin` (+14 missing fields), `NodePluginView`, `NodePluginMsg`, `NodePalette`, registry (`RegisterNodePlugin`, `RegisteredTypeKeys`), pure helpers (`NodeWindowBands`, `NodeCaretVMove/LineCol/At`, `NodeCodeBodyTail`, `NodeBlockFace`, `NodeRunOut` free func).
- `packages/editor` imports `nodeapi`; `Model` implements `nodeapi.NodeHost` + the delegations; `nodeRef` implements `nodeapi.NodeRef`; editor folds the nodeapi registry into its internal `nodeTypes` **lazily** (plugin inits run after editor's inits — a `sync.Once` triggered by `typeOf()`/`RegisteredTypeKeys()`), inserting in `database.TypeOrder` order to preserve the /type picker order.
- `packages/nodes` imports `nodeapi` + `database` + `tea` ONLY — **never editor**. Registers everything through nodeapi.
- Editor internal tests can then blank-import `packages/nodes` (no cycle!) to get registrations.
- `packages/fileeditor` likewise uses nodeapi for its python/rust statement registrations.

This was NOT yet implemented. The `nodeapi` flip is the prerequisite for moving the ~28 core types out of `packages/editor/registry.go`.

### Why files can't just move (background for the next agent)
- `nodeType` (registry.go:20-98) has 16 fields `NodePlugin` lacks: `sign`, `renderM`, `tempOnly`, `internal`, `disableChips`, `expand`, `openHost`, `flashActions`, `bands`, `continueOnEnter`, `onType`, `prefix`, `fixedColor`, `muteFrom`, `runInTail`, plus `view`/`blockCode` variants. The +14 list above = all except `sign` (already in NodePlugin) etc.
- Core behavior files have ~110 distinct `m.*` Model-method calls across voice(8), image(16), query(23), table(36), markup(21), zoteroitem(21), code(10), etc. Each becomes a one-line NodeModel delegation (unwrap `n.(nodeRef).it` — the adapter pattern already exists in `nodeplugin.go:219-248`).
- Per-type frictions to expect: dynamic glyphs take `*item` (todo ✓/○, log time, collapsed ● handled centrally in glyphFor); some helpers read item fields NodeRef doesn't expose (`CompletedAt`, `collapsed`, `note`…) — either extend NodeRef or delegate.
- `flashAction` (flash.go:28) has `do func(m *Model, it *item)` — needs an exported nodeapi equivalent + adapter.
- The 7 view structs (codeView, jsonView, imageView, markupView, moleculeView, tableView, runOutView, ncView) take `(m *Model, it *item)` — convert to `(h NodeHost, n NodeRef)`.
- `NodeTheme()` (nodeplugin.go:335) reads editor theme vars (theme.go:97-100, cReset/cFG/… are package vars set by the theme) — nodeapi needs its own theme copy set by editor (e.g. `nodeapi.SetTheme`), since plugin files must not import editor.

### Test-move strategy (the workable version)
- Model-bound behavior STAYS in editor as Model methods (they already are Model methods — e.g. `voiceRender`, `tableBandLines`, `runQuery`) exposed via NodeModel delegations; their tests STAY in editor unchanged.
- Pure behavior (span colors, compose trees, prefixes, glyphs, markup parse/serialize) moves to nodes/ rewritten against `NodeRef`; their tests move to nodes/ and use `fakeHost`/`fakeNode` (pattern: `packages/nodes/fakes_test.go`) or the real Model behind nodeapi interfaces.
- Move types incrementally, one commit per type or small group, suite green after each.

## Current node types inventory (for the move)

**Core in editor (`registry.go:136-352`) — 28 types:** bullets, todo, divider, empty, h1, h2, h3, code, quote, log, json, bash, query, web, wf, voice, image, svg, html, pir, math, table, molecule, line, agent (retired/internal), thinking, webresult (internal), zotero.
- Per-type behavior files already exist in editor: bashnode.go, webnode.go, code.go, json.go, log.go, markup.go (svg+html), math.go, molecule.go, table.go, voice.go, image.go, line.go, query.go, wf.go, zoteroitem.go; trivial types are inline in registry.go.
- **5 plugin types in `packages/nodes/`:** class, comment, fn, text, nlpcompute.
- **2 statement nodes in `packages/fileeditor/`:** python, rust.

**Chip kinds** (9, in editor/chip.go:176 `chipKinds`): tag, date, link, cmd, icon, agent, mol, element, zotero. Per-kind editor files: tag.go, date.go, link.go, service.go, cmdchip.go, molchip.go, icon.go, zoteroitem.go, agent.go. Shared vocabulary lives in packages/chips (split per kind already).

## Decisions locked by the user

- continueOnEnter = true ONLY for: todo, molecule, log, math, svg, html, line, bash. NOT fn, quote, class, comment, text, nlpcompute, code, table, headings, divider, empty, json, query, web, wf, voice, image, pir, thinking, zotero.
- nlpcompute stays a NODE (in packages/nodes), not a file-editor type.
- python/rust are FILE-EDITOR types (packages/fileeditor), not outline nodes — but they ARE registered as NodePlugins (statement nodes) from fileeditor.
- fn/class/comment/text stay in nodes (they're /type-pickable in the outline).
- The user wants the files UNDER `packages/nodes/`; they accepted the nodeapi approach ("move it to nodeapi … whatever easiest").

## Verification baseline

- `go build ./...` — passes.
- `go vet ./...` — passes (except pre-existing: none known).
- Test suite has PRE-EXISTING environmental failures unrelated to this work (sqlite built without fts5 → "no such module: fts5" failures in packages/database, packages/editor (most of the 186), cmd/lflow, daemon, cli/infra; also editor tests needing real binaries/SVG rasterizers like TestSVGRunRendersAPicture, TestWebNodeHangsHits, TestAgent* — all fail identically on a clean tree at 07c64ed). Use `git stash` + `go test` to compare before/after failure sets; a move is green if the failure SET is unchanged (only timing diffs).
- Pass reliably: packages/chips, packages/fileeditor (except fts5-using ones), packages/nodes parity, tests/arch (layering).
- gofmt: repo is clean.

## File map of the new packages

```
packages/chips/      chips.go (general) tag.go date.go link.go service.go + 2 tests
packages/nodes/      class.go comment.go fn.go text.go nlpcompute.go
                     fakes_test.go nlpcompute_test.go registry_parity_test.go
packages/fileeditor/ codec.go lang.go mathexpr.go nodes.go
                     python.go rust.go markdown.go toml.go json.go + 7 test files
packages/editor/     registry.go (nodeType + 28 core entries), nodeplugin.go (plugin API),
                     nodeplugin.go contains NodeCodeBodyTail, NodeTheme, NodeRunOut, NodeBlockFace,
                     NodeWindowBands, NodeCaret*, and the RegisterNodePlugin adapters
```

## Suggested next steps (in order)

1. Create `packages/nodeapi/` (imports: tea, database, nlp, utils only). Move/define: NodeRef, NodeHost, NodePlugin (+14 fields), NodePluginView, NodePluginMsg, NodePalette/NodeTheme, registry with `RegisterNodePlugin`, `RegisteredTypeKeys`, insert-by-`database.TypeOrder`; pure helpers NodeWindowBands/NodeCaret*/NodeCodeBodyTail/NodeBlockFace/NodeRunOut.
2. Editor: implement nodeapi interfaces on Model/nodeRef; lazy fold nodeapi registry → internal nodeTypes (sync.Once in typeOf/RegisteredTypeKeys); `SetTheme` bridge; keep RegisterNodePlugin working (both paths register into the fold).
3. Convert the 5 nodes/ plugin types + fileeditor python/rust to register via nodeapi (remove editor import from those files). Verify `go test ./packages/nodes ./packages/fileeditor ./tests/arch`.
4. Make one editor internal test file blank-import `packages/nodes` (proves the cycle is gone).
5. Move trivial core types (bullet, todo, divider, empty, h1-3, quote, pir, thinking, agent, webresult) to nodes/ — watch todoGlyph (needs CompletedAt → extend NodeRef or delegate).
6. Move pure-hook types: log (prefix/muteFrom/glyph), math (spanColor/bodyTail), molecule, line, json (view), code.
7. Move Model-heavy types via NodeModel delegations: bash, web, query, wf, voice, image, svg/html, table, zoteroitem — one commit each.
8. Update layers_test.go (add `packages/nodeapi`), stale comments, main.go imports if needed.
9. Final: full build/vet/test comparison vs 07c64ed failure set.

## Gotchas

- `nodeplugin.go` comment says plugins "live in editor/nodes" — stale.
- `database/node.go` doc comments reference "nodes/python.go", "nodes/rust.go" — stale (now fileeditor).
- `editor/registry.go:372` comment references packages/nodes — still accurate.
- `registry.go:349-351` comment references "editor/nodes" — stale.
- After the flip, `RegisterNodePlugin`'s in-editor adapter (nodeplugin.go:199-256) becomes the fold code — keep it working for any remaining in-package registrations.
- Ordering: /type picker shows nodeTypes in slice order; the fold must insert in database.TypeOrder order (parity test also enforces TypeOrder == registered keys).
- `packages/fileeditor` must ALSO stop importing editor eventually (it imports editor for RegisterNodePlugin/NodeTheme/NodeRunOut/NodeCodeBodyTail today) — swap to nodeapi; its python/rust NodePlugin registrations move to the nodeapi path.
- Don't run `go test ./...` and treat fts5 failures as regressions — compare failure sets instead (see Verification baseline).
