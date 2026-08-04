package nodes

import (
	"github.com/lflow/lflow/packages/database"
	"github.com/lflow/lflow/packages/editor"
	"github.com/lflow/lflow/packages/filecodec"
)

// The Rust node: the Rust sibling of the python node. One logical line per
// node; a `{`-opening line holds its block as children and the braces
// REGENERATE from structure on save (a closing `}` is never stored).
// Constructs are the shared first-class types: `fn add(a: i32) -> i32 {` is a
// fn node with text `add(a: i32) -> i32` (modifiers like pub/async kept in
// front), `struct Point {` / `impl Point {` / `enum Op {` are class nodes
// whose text keeps Rust's own container keyword, `// c` a comment node. The
// file codec (packages/filecodec) does the parsing/rendering; this file only
// registers the editor plugin (span coloring, alt+r export).

var rustKeywords = map[string]bool{
	"as": true, "async": true, "await": true, "break": true, "const": true,
	"continue": true, "crate": true, "dyn": true, "else": true, "enum": true,
	"extern": true, "false": true, "fn": true, "for": true, "if": true,
	"impl": true, "in": true, "let": true, "loop": true, "match": true,
	"mod": true, "move": true, "mut": true, "pub": true, "ref": true,
	"return": true, "self": true, "Self": true, "static": true, "struct": true,
	"super": true, "trait": true, "true": true, "type": true, "union": true,
	"unsafe": true, "use": true, "where": true, "while": true,
}

func init() {
	editor.RegisterNodePlugin(editor.NodePlugin{
		Key:            database.TypeRust,
		Label:          "Rust",
		InlineEditable: true,
		SpanColor:      keywordSpanColor(rustKeywords, "//"),
		BodyTail:       codeBodyTail,
		Run:            runCodeExport(filecodec.RustSpec),
	})
}
