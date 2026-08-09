# File conversions and node subsets

How `lflow file open` decides which node types a file format accepts, and how a
node type comes to have a syntax in that format.

This supersedes an earlier draft in this file that proposed collapsing node
types onto a set of universal "primitive shapes". That design was wrong, for a
reason worth recording: it generalized the layer that was already general. See
[Why not primitives](#why-not-primitives).

## The model

**Filtering is the design, not a workaround.** Each file format accepts a
subset of the node types. A `.py` session offers the types that mean something
in Python; a `.toml` session offers fewer. No format accepts everything, and no
node type is invented for a format's sake — the file editor only ever *filters*
the note editor's vocabulary, never extends it.

Three decisions fix the shape of it:

1. **The subset is derived, never written by hand.** A type is in a format's
   subset if and only if a conversion is registered for it. `Allowed()` stops
   being a list a codec author maintains and becomes a computed value, so it
   cannot disagree with what the codec actually does.
2. **Conversions live in a standalone (type × family) table** — owned by
   neither the node type nor the codec, readable as a matrix in one place.
3. **Formats are grouped into families.** A conversion targets a family, not a
   file extension, so one conversion covers every format in it.

Families exist because `.py` and `.rs` are already one implementation:
`RenderCode(doc, spec)` parameterized by a `LangSpec` carrying `commentLead`,
`indentUnit` and `braces`. A conversion written for the code family serves both,
and serves the next code-like language for free.

## What this changes today

Measured against the current codecs:

| format | `Allowed()` says | native cases | verdict |
|---|---|---|---|
| python | 9 | 9 | exact |
| rust | 9 | 9 | exact |
| toml | 3 | 3 | exact |
| json | 1 | 0 (own structure walk) | exact enough |
| markdown | **nil — all 38** | **15** | **drifted** |

Markdown is the only liar, and it is the reason the derived subset matters: it
offers every node type in the `/type` picker and then dumps 23 of them into
`<!-- lflow molecule: … -->` marker comments. Under the derived rule its subset
collapses to the 15 it actually implements.

**The marker-comment fallback is deleted.** `fallbackLead`, the `default:` arms
in `RenderCode`/markdown/toml, and `classifyMarkerBody`'s restore path all go.
They exist to carry types a session should never have been able to create.

## Why deleting the fallback is safe

Removing carriage is only safe if a foreign type cannot reach a file session.
It cannot, and this is worth stating because it is not obvious:

- **`Allowed()` gates exactly one thing.** It is referenced in one place,
  `filteredTypes` (tui/editor/editor.go). Nothing enforces it on parse, paste
  or save — it is purely the `/type` picker's filter. So the picker is the only
  place a type is chosen.
- **The node clipboard carries plain text.** Copy/cut serialize nodes as "one
  line per node, two spaces per depth" (tui/editor/clipboard.go). A paste into
  a file session creates nodes of that session's default type. Typed nodes do
  not travel.
- **Mirrors resolve against the scratch DB only.** A file session runs with
  `Live = nil` on a throwaway DB the parser just built, so the finder can only
  reach nodes that came out of this file.
- **The Temp subtree is never rendered.** `RenderFromDB` walks from
  `RootUUID`; Temp is a sibling root.

So filtering the picker is sufficient. That is the strongest argument for this
model over the universal-node one: the invariant is enforced at a single point.

## The one hole: generated types

Types marked `internal` never appear in the picker but are minted
programmatically by another node. There are two — `agent` (retired, the key
kept valid) and `webresult`, which a `web` node creates when run.

If `web` is ever in a format's subset while `webresult` has no conversion for
that family, a run puts unconvertible rows in the tree and a save has nowhere
to write them.

**Unresolved.** The options considered:

- Transitive derivation: `nodeType` declares a `generates` edge, and a type
  enters a subset only if everything it can generate converts there too,
  enforced by an architecture test. Nothing lost, cannot drift.
- Strip generated rows on save: they are a cache and `alt+r` rebuilds them.
  Simple and honest, but a save silently removes visible rows.
- Generated children inherit the parent's projection.
- Internal types get an implicit universal conversion.

Also still open: the exact family list (code / markup / config / data, and
whether `.json` belongs with `.toml`), and whether a conversion is declarative
data or a render/parse function pair — table grids, bar plots and math
linearization are code, not templates, so the table likely holds functions.

## Why not primitives

The rejected design collapsed all 38 node types onto six shapes (`leaf`,
`block`, `note`, `blank`, `verbatim`, `expr`) with attributes, so codecs would
implement shapes instead of types.

It conflated two different problems:

- **Carriage** — does a node survive a save and reopen? Already generic. One
  marker mechanism per format handled every type, including ones no codec knew.
- **Projection** — does a todo read as `- [ ]` in markdown and `# TODO` in
  Python? Genuinely per-format, and irreducible. `- [ ]` versus `# TODO` is
  knowledge no abstraction deletes; a shape layer only moves it from a switch
  into an attribute bag.

The primitive layer would have re-described carriage, which needed no help,
while leaving projection exactly as expensive. Worse, it traded away node
identity — a `table` and a `barplot` both reduce to `block` + leaves, so the
file could no longer say which reading it was.

One piece of it survives on its own merits: **`TypePython` and `TypeRust` are
one idea registered twice.** Both mean "one logical line of source"; the only
thing separating them is a keyword table for coloring, which is a lookup keyed
on a language, not a node type. Collapsing them removes the only two node types
that exist because a file format needed them — which is exactly the rule this
document is built on.
