package filecodec

import (
	"regexp"
	"strings"

	"github.com/lflow/lflow/packages/database"
	"github.com/pkg/errors"
)

// The .rs file codec: the Rust sibling of the python codec. One logical
// line per node; a `{`-opening line holds its block as children and the
// braces REGENERATE from structure on save (a closing `}` is never stored).
// Constructs are the shared first-class types: `fn add(a: i32) -> i32 {` is a
// fn node with text `add(a: i32) -> i32` (modifiers like pub/async kept in
// front), `struct Point {` / `impl Point {` / `enum Op {` are class nodes
// whose text keeps Rust's own container keyword, `// c` a comment node.
// Normalizations on save: 4-space indent, `} else {` splits onto two lines.
// The plugin side (span coloring, alt+r export) lives in
// packages/nodes/rust.go and reuses RustSpec through RenderCode.

// RustSpec is exported so the nodes package's plugin side can render a
// subtree through the same rules the codec saves with (alt+r export).
var RustSpec = LangSpec{
	Name:        "rust",
	stmtType:    database.TypeRust,
	commentLead: "// ",
	indentUnit:  "    ",
	braces:      true,
	renderFn:    renderRustFn,
	renderClass: func(head string) string { return head },
}

// renderRustFn reinserts `fn ` after any leading modifiers.
func renderRustFn(sig string) string {
	words := strings.Fields(sig)
	i := 0
	for i < len(words) && rustFnModifier(words[i]) {
		i++
	}
	return strings.Join(append(append([]string{}, words[:i]...), append([]string{"fn"}, words[i:]...)...), " ")
}

func rustFnModifier(w string) bool {
	return w == "pub" || strings.HasPrefix(w, "pub(") || w == "async" ||
		w == "const" || w == "unsafe" || w == "extern"
}

// rustClassLead reports a type-container header (after modifiers).
func rustClassLead(trimmed string) bool {
	words := strings.Fields(trimmed)
	i := 0
	for i < len(words) && rustFnModifier(words[i]) {
		i++
	}
	if i >= len(words) {
		return false
	}
	switch words[i] {
	case "struct", "enum", "trait", "impl", "mod", "union":
		return true
	}
	return false
}

// rustFnLead reports a fn header (after modifiers) and returns the signature
// with the `fn` keyword removed.
func rustFnLead(trimmed string) (string, bool) {
	words := strings.Fields(trimmed)
	i := 0
	for i < len(words) && rustFnModifier(words[i]) {
		i++
	}
	if i >= len(words) || words[i] != "fn" {
		return "", false
	}
	return strings.Join(append(append([]string{}, words[:i]...), words[i+1:]...), " "), true
}

// ── the .rs file codec ──────────────────────────────────────────────────────

type rustCodec struct{}

func init() { fileCodecs = append(fileCodecs, rustCodec{}) }

func (rustCodec) Name() string             { return "rust" }
func (rustCodec) Exts() []string           { return []string{".rs"} }
func (rustCodec) Allowed() map[string]bool { return codeAllowed(database.TypeRust) }

// classifyRust maps one trimmed line (brace already stripped when block) onto
// its node. block marks a line that opened `{` so an empty block round-trips.
func classifyRust(trimmed string, block bool) *SrcNode {
	switch {
	case strings.HasPrefix(trimmed, "// "):
		return classifyComment(trimmed[3:])
	case trimmed == "//":
		return &SrcNode{Type: database.TypeComment}
	default:
		if block {
			if sig, ok := rustFnLead(trimmed); ok {
				return &SrcNode{Type: database.TypeFn, Text: sig}
			}
			if rustClassLead(trimmed) {
				return &SrcNode{Type: database.TypeClass, Text: trimmed}
			}
			return &SrcNode{Type: database.TypeRust, Text: trimmed, Note: "block"}
		}
		return &SrcNode{Type: database.TypeRust, Text: trimmed}
	}
}

func (rustCodec) Parse(src string) ([]*SrcNode, error) {
	root := &SrcNode{}
	stack := []*SrcNode{root} // brace nesting; last element is the open block

	for _, raw := range strings.Split(strings.TrimRight(src, "\n"), "\n") {
		line := strings.TrimRight(raw, " \t")
		_, trimmed := indentWidth(line)
		if trimmed == "" {
			// blank: attaches inside the open block, in order — never under a
			// plain statement, which would read as a block on render
			stack[len(stack)-1].Kid(&SrcNode{Type: database.TypeRust})
			continue
		}
		if rustUnterminatedRawString(trimmed) {
			return nil, errors.New("multi-line raw strings are not yet supported")
		}
		// a leading `}` closes the current block; `} else {` etc. continues
		for strings.HasPrefix(trimmed, "}") {
			if len(stack) > 1 {
				stack = stack[:len(stack)-1]
			}
			trimmed = strings.TrimSpace(strings.TrimPrefix(trimmed, "}"))
		}
		if trimmed == "" {
			continue
		}
		// classify (comment? string?) BEFORE deciding the line opens a block
		// — a trailing `{` inside a comment ("// for x in y {") or a string
		// literal is never a real block opener; trusting it blind deletes the
		// `{` from the comment/string text and re-parents everything after.
		isComment := trimmed == "//" || strings.HasPrefix(trimmed, "// ")
		opens := !isComment && rustLastBraceIsCode(trimmed)
		if opens {
			trimmed = strings.TrimSpace(strings.TrimSuffix(trimmed, "{"))
		}
		n := stack[len(stack)-1].Kid(classifyRust(trimmed, opens))
		if opens {
			stack = append(stack, n)
		}
	}
	return root.Kids, nil
}

// rustLastBraceIsCode reports whether trimmed's trailing `{` sits in code —
// not inside a "..." string literal left open by the rest of the line (a
// plain string can legally continue onto the next line in Rust). A simple
// scanner with backslash-escape handling is enough to tell the two apart.
func rustLastBraceIsCode(trimmed string) bool {
	if !strings.HasSuffix(trimmed, "{") {
		return false
	}
	inStr := false
	for i := 0; i < len(trimmed)-1; i++ {
		switch {
		case inStr && trimmed[i] == '\\':
			i++
		case trimmed[i] == '"':
			inStr = !inStr
		}
	}
	return !inStr
}

// rustRawStringOpen matches a raw-string opener: r"…, r#"…, r##"…, and so on.
var rustRawStringOpen = regexp.MustCompile(`r(#*)"`)

// rustUnterminatedRawString reports whether trimmed opens a raw string whose
// matching closer (`"` followed by the same run of `#`) never appears later
// on the line — a multi-line raw string. This line-based codec can't
// losslessly represent one, so Parse refuses instead of corrupting it.
func rustUnterminatedRawString(trimmed string) bool {
	for _, m := range rustRawStringOpen.FindAllStringSubmatchIndex(trimmed, -1) {
		hashes := trimmed[m[2]:m[3]]
		if !strings.Contains(trimmed[m[1]:], `"`+hashes) {
			return true
		}
	}
	return false
}

func (rustCodec) Render(doc []*SrcNode) (string, error) {
	return RenderCode(doc, RustSpec)
}

// DefaultType: a fresh line in a .rs session is a rust statement.
func (rustCodec) DefaultType() string { return database.TypeRust }
