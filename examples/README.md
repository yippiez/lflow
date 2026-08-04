# examples — `lflow file open` and the node ⇄ file translation matrix

Every file here opens in the node editor with `lflow file open <file>` and
round-trips: the file parses into a node tree, you edit the tree, and each
save (ctrl+s, quit) serializes it back through the file type's codec. The
files are deliberately over-annotated — each one narrates its own mapping,
so opening it in the editor IS the documentation.

- `notes.md` — every markdown construct, incl. embedded code, math, a table
- `app.py` — python with first-class fn/class/comment nodes, math, todos
- `lib.rs` — rust: braces regenerate from the tree structure
- `config.json` — pure structure editing, type fidelity preserved
- `config.toml` — sections, keys, comments

## The translation matrix

How each node type serializes per file format. **native** means the format
has real syntax for it (parses back to the same node); **marker** means it
travels as a comment marker — `# lflow <type>: text` (python/toml),
`// lflow <type>: text` (rust), `<!-- lflow <type>: text -->` (markdown) —
and parse restores the node with its text (richer state such as an image's
pixels or a voice clip's audio does not follow it into the file);
**refused** means the save fails with a clear error (JSON has no comments
to hide anything in). The `/type` picker inside a file session only offers
the types its format accepts.

| node | markdown | python | rust | json | toml |
|---|---|---|---|---|---|
| text ¶ | paragraph (prose is NOT forced into a list) | marker | marker | the structural node: `key: value`, `key`, `key []` | the structural node: `[section]`, `key = value` |
| bullets | `- item`, nesting by 2-space indent | marker | marker | refused | marker |
| todo | `- [ ]` / `- [x]` | `# TODO …` / `# DONE …` | `// TODO …` / `// DONE …` | refused | `# TODO …` / `# DONE …` |
| h1 h2 h3 | `#` `##` `###`; the section body nests under its heading | marker | marker | refused | marker |
| quote | `> line` | marker | marker | refused | marker |
| code | fenced block; the language tag survives (` ```python `) | body verbatim at the node's indent (reopens as plain statements) | same as python | refused | marker |
| divider | `---` | marker | marker | refused | marker |
| comment ◦ | `<!-- text -->` | `# text` | `// text` | refused | `# text` |
| fn ƒ | readable `fn sig` paragraph (one-way) | `def sig:` — body is the children; `async` kept in the signature | `fn sig {` — modifiers (pub, async…) kept; braces from structure | refused | marker |
| class ◇ | readable `class head` paragraph (one-way) | `class head:` | `head {` — text keeps rust's own container keyword (`struct Point`, `impl Point`, `enum Op`) | refused | marker |
| python | marker | the statement line itself; blocks nest by indentation | marker | refused | marker |
| rust | marker | marker | the statement line itself; `{` blocks nest as children, `}` is never stored | refused | marker |
| math | `$$ (a + b) $$` block, linear form (reopens as a math atom) | a real expression: `×`→`*`, `^`→`**`, `π`→`math.pi`, `√`→`math.sqrt(…)` (one-way) | a real expression: `^`→`.powf(…)`, `π`→`std::f64::consts::PI` (one-way) | refused | marker |
| nlpcompute | `<!-- nlp: instruction -->` + fenced code block of the generated code | `# nlp: instruction` + the generated code beneath it | `// nlp: instruction` + code | refused | marker |
| table | a GFM grid: `\| a \| b \|` — columns are the node's children, cells their children | marker | marker | refused | marker |
| log, query, json-node, voice, image, wf | marker | marker | marker | refused | marker |

## Normalizations

A first save may reshape a file; after it, parse→render is byte-identical
(enforced by `packages/nodes/filecodec_test.go`, which also round-trips
every file in this directory):

- **python** re-indents to the 4-space grid; blank lines survive as empty
  nodes under the previous line.
- **rust** regenerates braces from structure (`} else {` splits onto two
  lines); a one-liner `fn get() { self.x }` stays a verbatim statement.
- **markdown** emits one blank line between blocks; a table's title becomes
  the paragraph above its grid; children of a paragraph flatten to a list.
- **json** normalizes to 2-space indentation; value types are preserved
  because leaves store raw JSON literals (`1`, `"1"`, `true`, `null`).
- **toml** stays flat like TOML itself (`[a.b]` is a header, not nested
  under `[a]`); one blank line before each section.

## Restriction, and future node types

A codec's `Allowed()` set is the whole restriction story: a CAD or ML node
added tomorrow needs no codec changes to be *safe* — in markdown it travels
as a marker, in code files as a marker comment, and JSON refuses it. To make
it *native* somewhere, teach that one codec its syntax and add a row above.
