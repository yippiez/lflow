package nodes

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// ── AST shape ───────────────────────────────────────────────────────────────

func TestLGBuildAST(t *testing.T) {
	ast, vars, err := lgBuild("(A and B) or not C")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Join(vars, ",") != "A,B,C" {
		t.Errorf("vars = %v", vars)
	}
	root, ok := ast.(lgBin)
	if !ok || root.op != "or" {
		t.Fatalf("root should be an OR gate, got %#v", ast)
	}
	if l, ok := root.l.(lgBin); !ok || l.op != "and" {
		t.Errorf("left child should be AND, got %#v", root.l)
	}
	if _, ok := root.r.(lgNot); !ok {
		t.Errorf("right child should be NOT, got %#v", root.r)
	}
}

func TestLGTree(t *testing.T) {
	ast, _, _ := lgBuild("A and not B")
	want := []string{
		"AND",
		"├─ A",
		"└─ NOT",
		"   └─ B",
	}
	got := lgTree(ast)
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("tree =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// ── gate evaluation ─────────────────────────────────────────────────────────

func results(t *testing.T, expr string) []bool {
	t.Helper()
	ast, vars, err := lgBuild(expr)
	if err != nil {
		t.Fatalf("%q: %v", expr, err)
	}
	_, r := lgTruth(ast, vars)
	return r
}

func eq(a, b []bool) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestLGGates(t *testing.T) {
	// rows enumerate A,B as 00,01,10,11 (MSB = A)
	cases := []struct {
		expr string
		want []bool
	}{
		{"A and B", []bool{false, false, false, true}},
		{"A nand B", []bool{true, true, true, false}},
		{"A or B", []bool{false, true, true, true}},
		{"A nor B", []bool{true, false, false, false}},
		{"A xor B", []bool{false, true, true, false}},
		{"A xnor B", []bool{true, false, false, true}},
		{"A -> B", []bool{true, true, false, true}},
		{"A <-> B", []bool{true, false, false, true}},
	}
	for _, c := range cases {
		if got := results(t, c.expr); !eq(got, c.want) {
			t.Errorf("%q = %v, want %v", c.expr, got, c.want)
		}
	}
	// NOT is unary: A alone → 2 rows
	if got := results(t, "not A"); !eq(got, []bool{true, false}) {
		t.Errorf("not A = %v, want [true false]", got)
	}
}

func TestLGSymbolForms(t *testing.T) {
	// symbol and word forms must agree
	pairs := [][2]string{
		{"A & B", "A and B"},
		{"A | B", "A or B"},
		{"A ^ B", "A xor B"},
		{"!A", "not A"},
		{"A => B", "A -> B"},
		{"A <=> B", "A <-> B"},
	}
	for _, p := range pairs {
		if !eq(results(t, p[0]), results(t, p[1])) {
			t.Errorf("%q and %q disagree", p[0], p[1])
		}
	}
}

func TestLGPrecedence(t *testing.T) {
	// "not A or B" must be (not A) or B, not not(A or B)
	if got := results(t, "not A or B"); !eq(got, []bool{true, true, false, true}) {
		t.Errorf("not A or B = %v", got)
	}
	// and binds tighter than or: "A or B and C" == "A or (B and C)"
	if !eq(results(t, "A or B and C"), results(t, "A or (B and C)")) {
		t.Errorf("and/or precedence wrong")
	}
	// implication is right-associative: A -> B -> C == A -> (B -> C)
	if !eq(results(t, "A -> B -> C"), results(t, "A -> (B -> C)")) {
		t.Errorf("implication should be right-associative")
	}
}

func TestLGConstants(t *testing.T) {
	if got := results(t, "1 and 0"); !eq(got, []bool{false}) {
		t.Errorf("1 and 0 = %v", got)
	}
	if got := results(t, "true or false"); !eq(got, []bool{true}) {
		t.Errorf("true or false = %v", got)
	}
}

func TestLGVerdict(t *testing.T) {
	taut := results(t, "A or not A")
	if !strings.HasPrefix(lgVerdict(taut), "tautology") {
		t.Errorf("A or not A: %s", lgVerdict(taut))
	}
	contra := results(t, "A and not A")
	if !strings.HasPrefix(lgVerdict(contra), "contradiction") {
		t.Errorf("A and not A: %s", lgVerdict(contra))
	}
	cont := results(t, "A -> B")
	if !strings.HasPrefix(lgVerdict(cont), "contingent") {
		t.Errorf("A -> B: %s", lgVerdict(cont))
	}
}

func TestLGErrors(t *testing.T) {
	for _, expr := range []string{
		"A and", "(A", "A B", "A @ B", "",
		"A and B and C and D and E and F and G and H and I", // 9 vars
	} {
		if _, _, err := lgBuild(expr); err == nil {
			t.Errorf("%q should error", expr)
		}
	}
}

// ── compute + view ──────────────────────────────────────────────────────────

func TestLGCompute(t *testing.T) {
	out := lgCompute("(A and B) or not C")
	joined := strings.Join(out, "\n")
	for _, want := range []string{"AST", "OR", "AND", "NOT", "truth table", "A B C │ out", "contingent"} {
		if !strings.Contains(joined, want) {
			t.Errorf("compute output missing %q:\n%s", want, joined)
		}
	}
	if got := lgCompute(""); !strings.Contains(got[0], "boolean expression") {
		t.Errorf("empty should hint, got %q", got[0])
	}
	if got := lgCompute("A @ B"); !strings.Contains(got[0], "unexpected character") {
		t.Errorf("bad char should error, got %q", got[0])
	}
}

func TestLGViewLifecycle(t *testing.T) {
	h := newFakeHost(t)
	n := &fakeNode{uuid: "u1", typ: "logic", text: "A and B"}

	if !(logicView{}).Enter(h, n) {
		t.Fatal("Enter should focus a non-empty expression")
	}
	if _, ok := h.NodeStore("u1")["lgOut"]; !ok {
		t.Error("Enter should cache the rendered output")
	}
	if got, want := (logicView{}).Lines(h, n, 80), 1+len(lgCompute(n.text)); got != want {
		t.Errorf("Lines = %d, want %d", got, want)
	}
	if bands := (logicView{}).Bands(h, n, "", 80, 0, 3, true); len(bands) != 3 {
		t.Errorf("Bands winH=3 gave %d lines", len(bands))
	}
	if _, handled := (logicView{}).Key(h, n, tea.KeyMsg{Type: tea.KeyDown}); !handled || h.scroll != 1 {
		t.Errorf("down should scroll to 1, got handled=%v scroll=%d", handled, h.scroll)
	}
	if _, handled := (logicView{}).Key(h, n, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")}); handled {
		t.Error("a plain rune should fall through in a read-only view")
	}
	(logicView{}).Leave(h, n)
	if _, ok := h.NodeStore("u1")["lgOut"]; ok {
		t.Error("Leave should clear the cache")
	}
}

func TestLGViewDeclinesEmpty(t *testing.T) {
	h := newFakeHost(t)
	if (logicView{}).Enter(h, &fakeNode{uuid: "u2", typ: "logic", text: "  "}) {
		t.Error("Enter should decline an empty expression")
	}
}
