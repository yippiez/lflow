package fileeditor

import (
	"strings"
	"testing"
)

// Regression tests for confirmed content-corruption bugs in the python,
// rust, toml and json codecs. Each targets one bug: a docstring's own
// bytes, a comment's own bytes, a multi-line string's own bytes, or a JSON
// key's own bytes must never be rewritten by the surrounding structure code.

// ── python: triple-quoted strings must not be re-indented ──────────────────

// TestPythonTripleQuoteVerbatim: a docstring's interior lines (2-space and
// 5-space indents, plus a blank line) keep their exact columns across a
// save — only the statement lines around it sit on the 4-space grid.
func TestPythonTripleQuoteVerbatim(t *testing.T) {
	src := strings.Join([]string{
		"def foo():",
		`    """`,
		"  two-space line",
		"     five-space line",
		"",
		`    """`,
		"    return 1",
		"",
	}, "\n")
	roundTrip(t, pythonCodec{}, src, src)
}

// TestPythonTripleQuoteSingleQuoteFlavor: the triple-single-quote flavor
// gets the same verbatim treatment as triple-double-quote.
func TestPythonTripleQuoteSingleQuoteFlavor(t *testing.T) {
	src := strings.Join([]string{
		"x = '''",
		"   kept exactly",
		"",
		"'''",
		"",
	}, "\n")
	roundTrip(t, pythonCodec{}, src, src)
}

// ── rust: brace tracking must classify the line first ──────────────────────

// TestRustCommentedBlockHeader: a comment whose text ends in `{` (commented
// out code) keeps its brace and does not open a phantom block.
func TestRustCommentedBlockHeader(t *testing.T) {
	src := strings.Join([]string{
		"fn main() {",
		"    // for x in y {",
		"    let a = 1;",
		"}",
		"",
	}, "\n")
	doc, err := (rustCodec{}).Parse(src)
	if err != nil {
		t.Fatal(err)
	}
	fn := doc[0]
	if len(fn.Kids) != 2 {
		t.Fatalf("want 2 kids (comment, statement) directly under fn, got %d: %+v", len(fn.Kids), fn.Kids)
	}
	if fn.Kids[0].Text != "for x in y {" {
		t.Fatalf("comment text corrupted: %+v", fn.Kids[0])
	}
	roundTrip(t, rustCodec{}, src, src)
}

// TestRustStringTrailingBrace: a same-line string literal containing `{`
// right before the real block opener doesn't confuse the scanner.
func TestRustStringTrailingBrace(t *testing.T) {
	src := strings.Join([]string{
		`fn f() {`,
		`    if s == "{" {`,
		`        return;`,
		`    }`,
		`}`,
		"",
	}, "\n")
	roundTrip(t, rustCodec{}, src, src)
}

// TestRustMultiLineRawStringRefused: a raw string left open across lines is
// refused instead of silently corrupted.
func TestRustMultiLineRawStringRefused(t *testing.T) {
	src := strings.Join([]string{
		"fn main() {",
		`    let s = r#"abc`,
		`def"#;`,
		"}",
		"",
	}, "\n")
	_, err := (rustCodec{}).Parse(src)
	if err == nil || !strings.Contains(err.Error(), "multi-line raw strings are not yet supported") {
		t.Fatalf("want a multi-line-raw-string refusal, got %v", err)
	}
}

// TestRustSingleLineRawStringUnaffected: a same-line raw string (the common
// case) is not mistaken for an unterminated one.
func TestRustSingleLineRawStringUnaffected(t *testing.T) {
	src := strings.Join([]string{
		`fn re() {`,
		`    let p = r"^\d+$";`,
		`}`,
		"",
	}, "\n")
	roundTrip(t, rustCodec{}, src, src)
}

// ── toml: multi-line strings must not be trimmed/dropped/misread ───────────

// TestTOMLMultiLineStringVerbatim: interior indentation, an interior blank
// line, and an interior line shaped like a section header all survive
// inside a """ string.
func TestTOMLMultiLineStringVerbatim(t *testing.T) {
	src := strings.Join([]string{
		`body = """`,
		"  two-space line",
		"     five-space line",
		"",
		"[not-a-section]",
		`"""`,
		`next = "after"`,
		"",
	}, "\n")
	doc, err := (tomlCodec{}).Parse(src)
	if err != nil {
		t.Fatal(err)
	}
	if len(doc) != 2 {
		t.Fatalf("want 2 top-level nodes (body, next), got %d: %+v", len(doc), doc)
	}
	if strings.Contains(doc[0].Text, "not-a-section") == false {
		t.Fatalf("interior [not-a-section] line lost: %+v", doc[0])
	}
	roundTrip(t, tomlCodec{}, src, src)
}

// ── json: ambiguous keys must round-trip through JSON-quoting ──────────────

// TestJSONAmbiguousColonKey: a key containing ": " no longer truncates.
func TestJSONAmbiguousColonKey(t *testing.T) {
	src := "{\n  \"note: important\": 1\n}\n"
	doc, err := (jsonCodec{}).Parse(src)
	if err != nil {
		t.Fatal(err)
	}
	if doc[0].Text != `"note: important": 1` {
		t.Fatalf("want a json-quoted key in node text, got %q", doc[0].Text)
	}
	roundTrip(t, jsonCodec{}, src, src)
}

// TestJSONAmbiguousBracketSuffixKey: a key ending " []" no longer flips the
// node's container kind.
func TestJSONAmbiguousBracketSuffixKey(t *testing.T) {
	src := "{\n  \"flags []\": [\n    1,\n    2\n  ]\n}\n"
	doc, err := (jsonCodec{}).Parse(src)
	if err != nil {
		t.Fatal(err)
	}
	if doc[0].Text != `"flags []" []` {
		t.Fatalf("want a json-quoted key with the array marker, got %q", doc[0].Text)
	}
	if len(doc[0].Kids) != 2 {
		t.Fatalf("want 2 array items under the key, got %d", len(doc[0].Kids))
	}
	roundTrip(t, jsonCodec{}, src, src)
}

// TestJSONUnambiguousKeyStaysClean: an ordinary key keeps the current
// unquoted node-text form (no needless quoting).
func TestJSONUnambiguousKeyStaysClean(t *testing.T) {
	src := "{\n  \"name\": \"lflow\"\n}\n"
	doc, err := (jsonCodec{}).Parse(src)
	if err != nil {
		t.Fatal(err)
	}
	if doc[0].Text != `name: "lflow"` {
		t.Fatalf("want the clean unquoted form, got %q", doc[0].Text)
	}
	roundTrip(t, jsonCodec{}, src, src)
}
