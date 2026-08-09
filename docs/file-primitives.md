# Files as primitives

A design for `lflow file open`: how every node type comes to work in every file
format, without a node type ever being invented for a file format's sake.

## The problem with what exists

A codec today answers one question per node type: "how do I write a `fn`? a
`todo`? a `math`?" `RenderCode` (tui/editor/lang.go) is a switch with eight
cases and a fallback; the markdown, toml and json codecs each carry their own.
That is an N×M grid — 38 node types × 5 formats — and every cell is hand-written
Go. Nobody fills a grid that size, so the codecs do the only thing left:

```go
func (jsonCodec) Allowed() map[string]bool { return allowed(database.TypeText) }
```

`Allowed()` is the grid's unfilled cells, shipped as a feature. In a `.json`
session 37 of the 38 node types are removed from the `/type` picker. In `.py`
and `.rs`, 29 are. The outline's whole vocabulary — the reason to write in
lflow rather than vim — is switched off exactly where a file is open.

The grid also leaks upward into the type system. `TypePython` and `TypeRust`
are node types whose entire reason to exist is that a file format needed them;
they are the same idea ("one logical line of source") registered twice, and a
third language means a third. Meanwhile `fn` and `class` are shared across the
two code codecs but mean nothing in `.toml` or `.md`, so a TOML `[package]`
section — a named container holding members, which is precisely what `class`
already models — parses as `TypeText` and its structure is thrown away.

The fix is not to fill the grid. It is to stop having one.

## The shape of the fix

Insert a layer. A node type does not tell a codec what it is; it declares which
**primitive shape** it reduces to. A codec never hears about node types at all —
it implements the primitives, and there are six of them.

```
38 node types ─┐
               ├─→ 6 primitive shapes ─→ 5 codecs
 new node type ─┘        (+ attributes)      └─ new format
```

Adding a node type costs one line and touches no codec. Adding a format costs
one codec and touches no node type. N+M, not N×M.

## The six shapes

| shape | what it is | who reduces to it |
|---|---|---|
| `leaf` | one logical line of content | bullets, text, todo, log, quote, line, pir, thinking, python, rust, toml keys |
| `block` | a header line whose children are its body | fn, class, h1–h3, table, barplot, bash, svg, html, molecule, toml sections, json objects/arrays |
| `note` | an annotation, not content | comment |
| `blank` | deliberate vertical space | empty, divider |
| `verbatim` | opaque lines the codec must never reformat | code fences, embedded blobs, unparseable regions |
| `expr` | a subtree that linearizes to a single line | math, nlpcompute |

A codec implements six functions instead of a switch over every type that
exists:

```go
type FileCodec interface {
    Name() string
    Exts() []string
    Parse(src string) ([]*SrcNode, error)

    RenderLeaf(n *SrcNode, depth int) []string
    RenderBlock(n *SrcNode, depth int, body []string) []string
    RenderNote(n *SrcNode, depth int) []string
    RenderBlank(n *SrcNode, depth int) []string
    RenderVerbatim(n *SrcNode, depth int) []string
    Dialect() map[string]string // for expr — see below
}
```

`Allowed()` is deleted. There is nothing left for it to gate: every node type
reduces to a shape, and every codec implements every shape.

## Attributes carry the differences

Shapes are coarse on purpose. What separates a `todo` from a `bullets` is not a
shape, it is a checked bit; what separates a Python statement from a Rust one is
not a shape, it is a language. Those ride as **attributes** — an open string map
on `SrcNode`, read by codecs that care and ignored by codecs that do not.

| attribute | meaning | example |
|---|---|---|
| `checked` | a leaf is done | markdown writes `- [x] `, python writes `# DONE ` |
| `lang` | which language this source is in | keyword coloring, fence tags |
| `kind` | what a block is | `function`, `type`, `section`, `sequence`, `mapping`, `group` |
| `key` | a leaf's left-hand side | `name = "x"`, `"a": 1` |
| `blob` | a content-addressed payload reference | voice, image, svg raster |

This is what kills `TypePython` and `TypeRust`. A source line becomes a `leaf`
with `lang=python`, and the language comes from the session's codec, not from
the node's identity. Keyword coloring — the only real reason those two types
exist — becomes a table lookup keyed on `lang`, which the `pythonKeywords` and
`rustKeywords` maps already are. A fourth language adds a keyword table, not a
node type.

`kind` is what generalizes `fn` and `class` past programming languages. A
`block` with `kind=type` is a Python class, a Rust struct, a TOML `[section]`,
and a JSON object — the same primitive, four surfaces:

```
block kind=type  "package"        →  class package:      (.py)
                                 →  struct package { }   (.rs)
                                 →  [package]            (.toml)
                                 →  "package": { }       (.json)
                                 →  ## package           (.md)
```

## Expressions and dialects

`expr` is the one shape that needs per-format knowledge, and it is also where
the current code most clearly shows the N×M problem: `mathExpr(n, spec.Name)`
takes the *language name* as an argument, so every new format edits the math
node, and every new operator edits every format.

Invert it. The node linearizes into a **canonical algebra** — `^(π, 2.0)` — and
the codec owns a `Dialect() map[string]string` translating canonical symbols
into its own spelling:

```go
// python
{"π": "math.pi", "^": "**", "×": "*", "÷": "/", "√": "math.sqrt"}
// rust
{"π": "std::f64::consts::PI", "^": ".powf", "√": ".sqrt"}
```

A new operator is one canonical symbol plus one row per dialect — added where
the format lives, by whoever owns that format. The math node stops knowing that
Rust exists.

## Reversibility: what a file cannot say

Reduction is lossy in exactly one way. A `table` and a `barplot` are both
"readings" of an ordinary subtree — that is already their documented design, and
it is the primitive model working perfectly: both reduce to `block` + `leaf`
children and round-trip byte-identically. But on reopen the file cannot say
*which* reading it was, so both come back as plain blocks.

Three ways to keep identity, and the choice is user-visible:

- **A — infer, store nothing.** The file stays pristine. Types that read off
  syntax (`# TODO` → todo, `def f():` → block kind=function, `$$…$$` → expr)
  come back exactly; the rest come back as their shape. This is already how
  the codecs mostly work.
- **B — inline markers.** `# ⟨lflow barplot⟩` on the line. Identity survives;
  every file grows lflow-specific litter. This is today's
  `# lflow <type>: <text>`, which additionally drops `note`, `checked`,
  `output` and every side table.
- **C — a sidecar.** `notes.md` beside `.lflow/notes.md.map`, keyed by content
  anchor. Files stay pristine *and* identity survives; the cost is a second
  file that drifts when the source is edited outside lflow.

**Recommended: A, falling back to C.** Infer everything inferable — which is
most of it — and write a sidecar only when a node in the file genuinely cannot
be inferred. A `.py` full of statements, comments and todos never grows a
sidecar. Put one bar plot in it and a sidecar appears, carrying only that node.

## Where files genuinely cannot follow

These are not gaps in the design; they are properties of the targets, and each
needs a decision rather than an implementation.

1. **JSON has no comments.** Strict JSON cannot carry a `note`, a `blank`, a
   `checked` bit, or any inline marker. Either `.json` emits JSONC (and stops
   being loadable by strict parsers), or `.json` is the one format that is
   sidecar-only. A "replace your text editor" tool must not write JSON that
   `json.load` rejects, so sidecar-only is the honest answer.

2. **Blobs are not text.** Voice recordings, images and rasterized SVG live in
   `node_blobs`. Base64 in a comment is not a serious option at megabyte scale.
   These need an asset directory (`.lflow/assets/<hash>`) with the node carrying
   a `blob` attribute — which is the `C` sidecar, generalized.

3. **Mirrors and chips point outside the file.** `mirror_of` and chip anchors
   reference nodes in the real outline, but a file session runs on a throwaway
   scratch DB with `Live = nil` — no daemon, no outline. A mirror of a node that
   is not in this file has nothing to resolve against. Either file sessions gain
   a read path to the live outline, or cross-document references expand to their
   text on save and stop being live.

4. **Live nodes carry no state a file can hold.** `agent` sessions, `query`
   results, `web` searches and `wf` mirrors are handles on external, changing
   state. They can be carried and restored, but their *content* is a cache; a
   file can hold the query, never the results.

## Node types worth adding

The primitive model exposes three gaps where a format's real structure
currently degrades to `TypeText`:

- **`pair`** — a key/value leaf (`leaf` + `key` attribute). TOML keys, JSON
  members, YAML mappings, `.env` lines, front-matter. Today every one of these
  is a `text` node and the editor cannot see where the key ends — so it cannot
  align values, validate a type, or offer an enum. This is the single highest-
  value addition: config files are most of what a text editor gets used for.

- **`section`** — a named container that is not a programming type
  (`block` + `kind=section`). Rather than stretch `class` to cover
  `[profile.release]`, give the primitive its own name and let `class` remain
  the programming-language reading of it.

- **`raw`** — an opaque region carried byte-for-byte (`verbatim`). The pressure
  valve that makes opening *any* file safe: a license header, a minified blob,
  a construct the parser does not know yet. Without it, every unparseable
  region is a corruption risk; with it, "I don't understand this" is a
  representable state.

`fn` and `class` are **not** replaced — they keep their names and their glyphs,
and gain `kind`. They were already language-neutral; the change is that they now
mean something in `.toml` and `.md` too.

## What this deletes

- `Allowed()` from the `FileCodec` interface, and every call site.
- `TypePython` and `TypeRust` from `TypeOrder` (kept valid, like `TypeAgent`,
  so existing nodes keep their name).
- The type switch in `RenderCode`, and its equivalent in each of the four other
  codecs.
- `examples/README.md`'s node-type × file-format matrix — which `codec.go`
  points at twice and which does not exist. Under this design the matrix is six
  rows long and belongs in this document.
