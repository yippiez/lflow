package nodes

import (
	"strings"
	"testing"

	"github.com/lflow/lflow/packages/database"
)

// roundTrip parses src, renders it back, and asserts the output; then parses
// the OUTPUT again and asserts render is idempotent on it.
func roundTrip(t *testing.T, c FileCodec, src, want string) {
	t.Helper()
	doc, err := c.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	got, err := c.Render(doc)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if got != want {
		t.Fatalf("render mismatch\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
	doc2, err := c.Parse(got)
	if err != nil {
		t.Fatalf("reparse: %v", err)
	}
	again, err := c.Render(doc2)
	if err != nil {
		t.Fatalf("rerender: %v", err)
	}
	if again != got {
		t.Fatalf("not idempotent\n--- first ---\n%s\n--- second ---\n%s", got, again)
	}
}

func TestCodecForPath(t *testing.T) {
	for path, want := range map[string]string{
		"notes.md": "markdown", "NOTES.MD": "markdown",
		"a/b.py": "python", "lib.rs": "rust",
		"cfg.json": "json", "Cargo.toml": "toml",
		"plain.txt": "",
	} {
		c, ok := CodecForPath(path)
		if want == "" {
			if ok {
				t.Fatalf("%s: unexpected codec %s", path, c.Name())
			}
			continue
		}
		if !ok || c.Name() != want {
			t.Fatalf("%s: want %s, got ok=%v", path, want, ok)
		}
	}
}

// ── markdown ────────────────────────────────────────────────────────────────

// TestMarkdownRoundTrip: every construct round-trips; prose stays prose.
func TestMarkdownRoundTrip(t *testing.T) {
	src := strings.Join([]string{
		"# Title",
		"",
		"An opening paragraph, kept as prose — not a list item.",
		"",
		"- [ ] open task",
		"- [x] done task",
		"",
		"## Section",
		"",
		"> a quote",
		"",
		"```python",
		"x = 1",
		"print(x)",
		"```",
		"",
		"- item",
		"  - nested item",
		"",
		"<!-- an honest comment -->",
		"",
		"$$",
		"(a + b)",
		"$$",
		"",
		"| name | qty |",
		"| --- | --- |",
		"| bolts | 40 |",
		"| nuts | 12 |",
		"",
		"---",
		"",
		"### Deep",
		"",
		"closing prose",
		"",
	}, "\n")
	roundTrip(t, markdownCodec{}, src, src)
}

// TestMarkdownParagraphStaysProse: the regression the v2 rework exists for.
func TestMarkdownParagraphStaysProse(t *testing.T) {
	doc, err := (markdownCodec{}).Parse("# H\n\nplain prose\n")
	if err != nil {
		t.Fatal(err)
	}
	if len(doc) != 1 || len(doc[0].Kids) != 1 {
		t.Fatalf("shape: %+v", doc)
	}
	p := doc[0].Kids[0]
	if p.Type != database.TypeText || p.Text != "plain prose" {
		t.Fatalf("paragraph: %+v", p)
	}
	roundTrip(t, markdownCodec{}, "# H\n\nplain prose\n", "# H\n\nplain prose\n")
}

// TestMarkdownFenceLanguageSurvives: the fence's language tag lives in Note.
func TestMarkdownFenceLanguageSurvives(t *testing.T) {
	doc, err := (markdownCodec{}).Parse("```bash\necho hi\n```\n")
	if err != nil {
		t.Fatal(err)
	}
	if doc[0].Type != database.TypeCode || doc[0].Note != "bash" || doc[0].Text != "echo hi" {
		t.Fatalf("fence: %+v", doc[0])
	}
}

// TestMarkdownTable: a GFM grid becomes a table node (columns → cells) and back.
func TestMarkdownTable(t *testing.T) {
	doc, err := (markdownCodec{}).Parse("| a | b |\n| --- | --- |\n| 1 | 2 |\n| 3 | 4 |\n")
	if err != nil {
		t.Fatal(err)
	}
	tab := doc[0]
	if tab.Type != database.TypeTable || len(tab.Kids) != 2 {
		t.Fatalf("table: %+v", tab)
	}
	if tab.Kids[0].Text != "a" || len(tab.Kids[0].Kids) != 2 || tab.Kids[0].Kids[1].Text != "3" {
		t.Fatalf("column a: %+v", tab.Kids[0])
	}
}

// TestMarkdownForeignTypeMarker: an lflow-only type survives as a marker.
func TestMarkdownForeignTypeMarker(t *testing.T) {
	out, err := (markdownCodec{}).Render([]*SrcNode{{Type: database.TypeLog, Text: "shipped it"}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "<!-- lflow log: shipped it -->") {
		t.Fatalf("marker missing: %q", out)
	}
	doc, err := (markdownCodec{}).Parse(out)
	if err != nil {
		t.Fatal(err)
	}
	if doc[0].Type != database.TypeLog || doc[0].Text != "shipped it" {
		t.Fatalf("restored: %+v", doc[0])
	}
}

// TestMarkdownNLPCompute: instruction comment + generated code beneath.
func TestMarkdownNLPCompute(t *testing.T) {
	out, err := (markdownCodec{}).Render([]*SrcNode{{
		Type: database.TypeNLPCompute, Text: "sum the counts",
		Note: "python", Output: "total = sum(counts)",
	}})
	if err != nil {
		t.Fatal(err)
	}
	want := "<!-- nlp: sum the counts -->\n```python\ntotal = sum(counts)\n```\n"
	if out != want {
		t.Fatalf("nlp render:\n%q\nwant\n%q", out, want)
	}
	doc, _ := (markdownCodec{}).Parse(out)
	if doc[0].Type != database.TypeNLPCompute || doc[0].Text != "sum the counts" {
		t.Fatalf("nlp reparse: %+v", doc[0])
	}
}

// ── python ──────────────────────────────────────────────────────────────────

// TestPythonRoundTrip: constructs + statements + blanks round-trip.
func TestPythonRoundTrip(t *testing.T) {
	src := strings.Join([]string{
		"import os",
		"",
		"# a plain comment",
		"# TODO tighten the retry loop",
		"",
		"def greet(name):",
		"    if name:",
		"        print(f\"hi {name}\")",
		"    else:",
		"        print(\"hi\")",
		"",
		"",
		"class App:",
		"    def run(self):",
		"        for i in range(3):",
		"            greet(str(i))",
		"        return 0",
		"",
		"",
		"async def main():",
		"    await App().run()",
		"",
	}, "\n")
	roundTrip(t, pythonCodec{}, src, src)
}

// TestPythonConstructNodes: def/class/# parse into first-class nodes.
func TestPythonConstructNodes(t *testing.T) {
	doc, err := (pythonCodec{}).Parse("# top\ndef f(a):\n    pass\nclass C(Base):\n    pass\n")
	if err != nil {
		t.Fatal(err)
	}
	if doc[0].Type != database.TypeComment || doc[0].Text != "top" {
		t.Fatalf("comment: %+v", doc[0])
	}
	if doc[1].Type != database.TypeFn || doc[1].Text != "f(a)" {
		t.Fatalf("fn: %+v", doc[1])
	}
	if doc[2].Type != database.TypeClass || doc[2].Text != "C(Base)" {
		t.Fatalf("class: %+v", doc[2])
	}
	if len(doc[1].Kids) != 1 || doc[1].Kids[0].Text != "pass" {
		t.Fatalf("fn body: %+v", doc[1].Kids)
	}
}

// TestPythonReindents: a 2-space file normalizes to the 4-space grid.
func TestPythonReindents(t *testing.T) {
	roundTrip(t, pythonCodec{},
		"def f():\n  if x:\n    return 1\n  return 0\n",
		"def f():\n    if x:\n        return 1\n    return 0\n")
}

// TestPythonMathNode: a math subtree renders as a real expression.
func TestPythonMathNode(t *testing.T) {
	math := &SrcNode{Type: database.TypeMath, Text: "=", Kids: []*SrcNode{
		{Text: "area"},
		{Type: database.TypeMath, Text: "×", Kids: []*SrcNode{
			{Text: "π"},
			{Type: database.TypeMath, Text: "^", Kids: []*SrcNode{{Text: "r"}, {Text: "2"}}},
		}},
	}}
	out, err := (pythonCodec{}).Render([]*SrcNode{math})
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(out) != "(area == (math.pi * (r ** 2)))" {
		t.Fatalf("math expr: %q", out)
	}
}

// TestPythonTodoAndFallback: todo ⇄ # TODO, a foreign type ⇄ # lflow marker.
func TestPythonTodoAndFallback(t *testing.T) {
	out, err := (pythonCodec{}).Render([]*SrcNode{
		{Type: database.TypeTodo, Text: "profile the parser"},
		{Type: database.TypeQuery, Text: "type:todo"},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := "# TODO profile the parser\n# lflow query: type:todo\n"
	if out != want {
		t.Fatalf("got %q want %q", out, want)
	}
	doc, _ := (pythonCodec{}).Parse(out)
	if doc[0].Type != database.TypeTodo || doc[1].Type != database.TypeQuery {
		t.Fatalf("restored: %+v %+v", doc[0], doc[1])
	}
}

// ── rust ────────────────────────────────────────────────────────────────────

// TestRustRoundTrip: braces regenerate from structure.
func TestRustRoundTrip(t *testing.T) {
	src := strings.Join([]string{
		"use std::fmt;",
		"",
		"// a comment",
		"",
		"pub struct Point {",
		"    x: f64,",
		"    y: f64,",
		"}",
		"",
		"impl Point {",
		"    pub fn norm(&self) -> f64 {",
		"        if self.x > 0.0 {",
		"            return self.x;",
		"        }",
		"        self.y",
		"    }",
		"}",
		"",
		"fn main() {",
		"    let p = Point { x: 1.0, y: 2.0 };",
		"    println!(\"{}\", p.norm());",
		"}",
		"",
	}, "\n")
	roundTrip(t, rustCodec{}, src, src)
}

// TestRustConstructNodes: fn strips its keyword (modifiers kept), containers
// keep theirs.
func TestRustConstructNodes(t *testing.T) {
	doc, err := (rustCodec{}).Parse("pub fn add(a: i32, b: i32) -> i32 {\n    a + b\n}\nstruct S {\n    v: u8,\n}\n")
	if err != nil {
		t.Fatal(err)
	}
	if doc[0].Type != database.TypeFn || doc[0].Text != "pub add(a: i32, b: i32) -> i32" {
		t.Fatalf("fn: %+v", doc[0])
	}
	if doc[1].Type != database.TypeClass || doc[1].Text != "struct S" {
		t.Fatalf("class: %+v", doc[1])
	}
}

// TestRustEmptyBlock: a childless block keeps its braces via the Note marker.
func TestRustEmptyBlock(t *testing.T) {
	roundTrip(t, rustCodec{}, "fn noop() {\n}\n", "fn noop() {\n}\n")
}

// TestRustMathPow: ^ becomes .powf, π its constant.
func TestRustMathPow(t *testing.T) {
	math := &SrcNode{Type: database.TypeMath, Text: "^", Kids: []*SrcNode{{Text: "π"}, {Text: "2.0"}}}
	out, err := (rustCodec{}).Render([]*SrcNode{math})
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(out) != "(std::f64::consts::PI).powf(2.0)" {
		t.Fatalf("math expr: %q", out)
	}
}

// ── json ────────────────────────────────────────────────────────────────────

// TestJSONRoundTrip: types, order, empty containers and arrays all survive.
func TestJSONRoundTrip(t *testing.T) {
	src := strings.Join([]string{
		`{`,
		`  "name": "lflow",`,
		`  "version": 2,`,
		`  "pinned": true,`,
		`  "ratio": 1.50,`,
		`  "nothing": null,`,
		`  "tags": [`,
		`    "editor",`,
		`    "outline"`,
		`  ],`,
		`  "empty_obj": {},`,
		`  "empty_arr": [],`,
		`  "nested": {`,
		`    "deep": [`,
		`      {`,
		`        "k": "v"`,
		`      }`,
		`    ]`,
		`  }`,
		`}`,
		``,
	}, "\n")
	roundTrip(t, jsonCodec{}, src, src)
}

// TestJSONTopLevelArray: an array document keeps its kind.
func TestJSONTopLevelArray(t *testing.T) {
	src := "[\n  1,\n  \"two\",\n  {\n    \"three\": 3\n  }\n]\n"
	roundTrip(t, jsonCodec{}, src, src)
}

// TestJSONRejectsForeignTypes: the restriction is enforced with a clear error.
func TestJSONRejectsForeignTypes(t *testing.T) {
	_, err := (jsonCodec{}).Render([]*SrcNode{{Type: database.TypeMath, Text: "+"}})
	if err == nil || !strings.Contains(err.Error(), "text nodes only") {
		t.Fatalf("want restriction error, got %v", err)
	}
}

// ── toml ────────────────────────────────────────────────────────────────────

// TestTOMLRoundTrip: sections own their keys, comments and todos survive.
func TestTOMLRoundTrip(t *testing.T) {
	src := strings.Join([]string{
		`title = "demo"`,
		"# a comment",
		"# TODO bump the edition",
		"",
		"[package]",
		`name = "lflow"`,
		`version = "0.1.0"`,
		"",
		"[dependencies]",
		`serde = { version = "1", features = ["derive"] }`,
		"",
	}, "\n")
	roundTrip(t, tomlCodec{}, src, src)
}

// ── DB glue ─────────────────────────────────────────────────────────────────

// TestParseRenderThroughDB: the full ParseIntoDB → RenderFromDB path.
func TestParseRenderThroughDB(t *testing.T) {
	db := database.InitTestMemoryDB(t)
	root := database.Node{UUID: "docroot", Name: "doc", Type: database.TypeBullets}
	if err := root.Insert(db); err != nil {
		t.Fatal(err)
	}
	src := "def f():\n    return 1\n"
	if err := ParseIntoDB(db, "docroot", pythonCodec{}, src); err != nil {
		t.Fatal(err)
	}
	top, err := database.GetChildren(db, "docroot")
	if err != nil {
		t.Fatal(err)
	}
	if len(top) != 1 || top[0].Type != database.TypeFn || top[0].Name != "f()" {
		t.Fatalf("db shape: %+v", top)
	}
	out, err := RenderFromDB(db, "docroot", pythonCodec{})
	if err != nil {
		t.Fatal(err)
	}
	if out != src {
		t.Fatalf("db round-trip: %q", out)
	}
}

// ── span coloring ───────────────────────────────────────────────────────────

// TestKeywordSpanColor: keywords tint, names do not, comments dim to the end.
func TestKeywordSpanColor(t *testing.T) {
	sc := keywordSpanColor(pythonKeywords, "#")
	runes := []rune("for x in items:  # loop")
	colors := sc(runes)
	if colors[0] == "" || colors[2] == "" {
		t.Fatal("`for` should be colored")
	}
	if colors[4] != "" {
		t.Fatal("`x` should not be colored")
	}
	hash := strings.IndexRune(string(runes), '#')
	for i := hash; i < len(runes); i++ {
		if colors[i] == "" {
			t.Fatalf("comment rune %d should be dim", i)
		}
	}
}
