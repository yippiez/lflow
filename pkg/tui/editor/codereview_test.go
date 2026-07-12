package editor

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/lflow/lflow/pkg/tui/database"
)

func TestCRRange(t *testing.T) {
	cases := map[string][]string{
		"":              nil,
		"a1b2..HEAD":    {"a1b2", "HEAD"},
		"a1b2 c3d4":     {"a1b2", "c3d4"},
		"main":          {"main"},
		"a1b2..":        {"a1b2"},
		" a1b2 .. c3d4": {"a1b2", "c3d4"},
	}
	for in, want := range cases {
		got := crRange(in)
		if len(got) != len(want) {
			t.Fatalf("crRange(%q) = %v, want %v", in, got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("crRange(%q) = %v, want %v", in, got, want)
			}
		}
	}
}

func TestSigIdent(t *testing.T) {
	cases := map[string]string{
		"func EncodeValue(v any) (any, error)":  "EncodeValue",
		"func (m *Model) canvasRender(it *item)": "canvasRender",
		"type Req struct":                        "Req",
		"class Foo(Base):":                       "Foo",
		"def train(inputs, epochs=10):":          "train",
		"pub fn resolve(&self) -> u32":           "resolve",
		"const OpHello = …":                      "OpHello",
	}
	for in, want := range cases {
		if got := sigIdent(in); got != want {
			t.Fatalf("sigIdent(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestCodeSigExplore drives the real signatures CLI over a fixture — skipped
// where the binary is absent.
func TestCodeSigExplore(t *testing.T) {
	if _, err := exec.LookPath("signatures"); err != nil {
		t.Skip("signatures not installed")
	}
	m, _, n1 := newAgentTestModel(t)
	n1.typ = database.TypeCodeSig
	src := filepath.Join(t.TempDir(), "fixture.go")
	if err := os.WriteFile(src, []byte("package x\n\nfunc Hello() int { return 1 }\n\ntype Box struct{ N int }\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	n1.name = src

	v := codeSigView{}
	if !v.Enter(m, n1) {
		t.Fatal("Enter must show")
	}
	st := csStateOf(m, n1)
	if st.err != "" {
		t.Fatalf("unexpected error: %s", st.err)
	}
	var sawFunc, sawType bool
	for _, e := range st.entries {
		if e.Kind == "function" && sigIdent(e.Text) == "Hello" {
			sawFunc = true
		}
		if e.Kind == "class" && sigIdent(e.Text) == "Box" {
			sawType = true
		}
	}
	if !sawFunc || !sawType {
		t.Fatalf("signatures must list Hello and Box: %+v", st.entries)
	}
}
