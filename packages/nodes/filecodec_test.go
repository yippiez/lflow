package nodes

import (
	"strings"
	"testing"

	"github.com/lflow/lflow/packages/database"
)

// codecRoot inserts a plain root to parse under and returns its uuid.
func codecRoot(t *testing.T, db *database.DB) string {
	t.Helper()
	n := database.Node{UUID: "docroot", Name: "doc", Type: database.TypeBullets}
	if err := n.Insert(db); err != nil {
		t.Fatal(err)
	}
	return n.UUID
}

// roundTrip parses src, renders it back, and asserts the output; then parses
// the OUTPUT into a fresh tree and asserts render is idempotent on it.
func roundTrip(t *testing.T, c FileCodec, src, want string) {
	t.Helper()

	db := database.InitTestMemoryDB(t)
	root := codecRoot(t, db)
	if err := c.Parse(db, root, src); err != nil {
		t.Fatalf("parse: %v", err)
	}
	got, err := c.Render(db, root)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if got != want {
		t.Fatalf("render mismatch\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}

	db2 := database.InitTestMemoryDB(t)
	root2 := codecRoot(t, db2)
	if err := c.Parse(db2, root2, got); err != nil {
		t.Fatalf("reparse: %v", err)
	}
	again, err := c.Render(db2, root2)
	if err != nil {
		t.Fatalf("rerender: %v", err)
	}
	if again != got {
		t.Fatalf("not idempotent\n--- first ---\n%s\n--- second ---\n%s", got, again)
	}
}

func TestCodecForPath(t *testing.T) {
	for path, want := range map[string]string{
		"notes.md":  "markdown",
		"NOTES.MD":  "markdown",
		"a/b.py":    "python",
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

// TestMarkdownRoundTrip: a 4-space-grid document with every construct
// round-trips byte-identically.
func TestMarkdownRoundTrip(t *testing.T) {
	src := strings.Join([]string{
		"# Title",
		"",
		"- intro line",
		"- [ ] open task",
		"- [x] done task",
		"",
		"## Section",
		"",
		"> a quote",
		"",
		"```",
		"x = 1",
		"print(x)",
		"```",
		"",
		"- item",
		"  - nested item",
		"",
		"---",
		"",
		"### Deep",
		"",
		"- tail",
		"",
	}, "\n")
	// render normalizes: blank after headings, none between list items
	want := strings.Join([]string{
		"# Title",
		"",
		"- intro line",
		"- [ ] open task",
		"- [x] done task",
		"",
		"## Section",
		"",
		"> a quote",
		"",
		"```",
		"x = 1",
		"print(x)",
		"```",
		"",
		"- item",
		"  - nested item",
		"",
		"---",
		"",
		"### Deep",
		"",
		"- tail",
		"",
	}, "\n")
	roundTrip(t, markdownCodec{}, src, want)
}

// TestMarkdownParagraphNormalizes: prose lines become list items on save.
func TestMarkdownParagraphNormalizes(t *testing.T) {
	roundTrip(t, markdownCodec{},
		"# H\n\nplain paragraph\n",
		"# H\n\n- plain paragraph\n")
}

// TestMarkdownNesting: headings own their section, deeper headings nest.
func TestMarkdownNesting(t *testing.T) {
	db := database.InitTestMemoryDB(t)
	root := codecRoot(t, db)
	src := "# A\n- a1\n## B\n- b1\n# C\n- c1\n"
	if err := (markdownCodec{}).Parse(db, root, src); err != nil {
		t.Fatal(err)
	}
	top, err := database.GetChildren(db, root)
	if err != nil {
		t.Fatal(err)
	}
	if len(top) != 2 || top[0].Name != "A" || top[1].Name != "C" {
		t.Fatalf("top: %+v", top)
	}
	aKids, _ := database.GetChildren(db, top[0].UUID)
	if len(aKids) != 2 || aKids[0].Name != "a1" || aKids[1].Name != "B" {
		t.Fatalf("A children: %+v", aKids)
	}
	if aKids[1].Type != database.TypeH2 {
		t.Fatalf("B type: %s", aKids[1].Type)
	}
}

// TestPythonRoundTrip: a 4-space-indented file round-trips byte-identically,
// blank lines included.
func TestPythonRoundTrip(t *testing.T) {
	src := strings.Join([]string{
		"import os",
		"",
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
		"if __name__ == \"__main__\":",
		"    App().run()",
		"",
	}, "\n")
	roundTrip(t, pythonCodec{}, src, src)
}

// TestPythonReindents: a 2-space file normalizes to the 4-space grid, same
// structure.
func TestPythonReindents(t *testing.T) {
	roundTrip(t, pythonCodec{},
		"def f():\n  if x:\n    return 1\n  return 0\n",
		"def f():\n    if x:\n        return 1\n    return 0\n")
}

// TestPythonNesting: bodies nest under their headers.
func TestPythonNesting(t *testing.T) {
	db := database.InitTestMemoryDB(t)
	root := codecRoot(t, db)
	if err := (pythonCodec{}).Parse(db, root, "def f():\n    a = 1\n    b = 2\nx = 3\n"); err != nil {
		t.Fatal(err)
	}
	top, _ := database.GetChildren(db, root)
	if len(top) != 2 || top[0].Name != "def f():" || top[1].Name != "x = 3" {
		t.Fatalf("top: %+v", top)
	}
	if top[0].Type != database.TypePython {
		t.Fatalf("type: %s", top[0].Type)
	}
	body, _ := database.GetChildren(db, top[0].UUID)
	if len(body) != 2 || body[0].Name != "a = 1" || body[1].Name != "b = 2" {
		t.Fatalf("body: %+v", body)
	}
}

// TestPythonSpanColor: keywords tint, names do not, comments dim to the end.
func TestPythonSpanColor(t *testing.T) {
	runes := []rune("for x in items:  # loop")
	colors := pythonSpanColor(runes)
	if colors[0] == "" || colors[1] == "" || colors[2] == "" {
		t.Fatal("`for` should be colored")
	}
	if colors[4] != "" {
		t.Fatal("`x` should not be colored")
	}
	if colors[6] == "" || colors[7] == "" {
		t.Fatal("`in` should be colored")
	}
	hash := strings.IndexRune(string(runes), '#')
	for i := hash; i < len(runes); i++ {
		if colors[i] == "" {
			t.Fatalf("comment rune %d should be dim", i)
		}
	}
}
