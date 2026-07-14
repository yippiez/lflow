package nodes

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// ── cellular automaton ──────────────────────────────────────────────────────

func TestFSCARule90(t *testing.T) {
	// rule 90 is left XOR right: a single seed opens into a Sierpinski triangle.
	grid := fsCA(90, 3, 7, "single")
	if len(grid) != 3 {
		t.Fatalf("want 3 generations, got %d", len(grid))
	}
	// gen 0: only the centre (index 3) is live
	want0 := []bool{false, false, false, true, false, false, false}
	if !rowsEqual(grid[0], want0) {
		t.Errorf("gen0 = %v, want %v", grid[0], want0)
	}
	// gen 1: the two cells flanking the seed
	want1 := []bool{false, false, true, false, true, false, false}
	if !rowsEqual(grid[1], want1) {
		t.Errorf("gen1 = %v, want %v", grid[1], want1)
	}
	// gen 2: the pair at 2,4 map out to 1,5 (the 101 centre stays dead under rule 90)
	want2 := []bool{false, true, false, false, false, true, false}
	if !rowsEqual(grid[2], want2) {
		t.Errorf("gen2 = %v, want %v", grid[2], want2)
	}
}

func TestFSCAInitString(t *testing.T) {
	grid := fsCA(110, 1, 5, "10100")
	want := []bool{true, false, true, false, false}
	if !rowsEqual(grid[0], want) {
		t.Errorf("init row = %v, want %v", grid[0], want)
	}
}

func TestFSGridRowTrimsTail(t *testing.T) {
	got := fsGridRow([]bool{false, true, false, true, false})
	if want := fsOff + fsOn + fsOff + fsOn; got != want {
		t.Errorf("fsGridRow = %q, want %q", got, want)
	}
}

func TestFSCARenderBadRule(t *testing.T) {
	out := fsCARender(map[string]string{"rule": "999"})
	if len(out) != 1 || !strings.Contains(out[0], "0..255") {
		t.Errorf("want a rule-range error, got %v", out)
	}
}

// ── game of life ────────────────────────────────────────────────────────────

func TestFSLifeBlinkerOscillates(t *testing.T) {
	g := fsLifePattern("blinker", 5, 5) // horizontal at y=2, x∈{1,2,3}
	n := fsLifeStep(g)
	// a blinker flips to vertical: x=2, y∈{1,2,3}
	if !(n[1][2] && n[2][2] && n[3][2]) {
		t.Errorf("blinker did not become vertical: %v", n)
	}
	if n[2][1] || n[2][3] {
		t.Errorf("old horizontal arms still live: %v", n)
	}
	if live(n) != 3 {
		t.Errorf("want 3 live cells, got %d", live(n))
	}
	// a second step returns it to horizontal (period 2)
	if !gridsEqual(fsLifeStep(n), g) {
		t.Errorf("blinker is not period-2")
	}
}

func TestFSLifeBlockIsStill(t *testing.T) {
	g := fsLifePattern("block", 6, 6)
	if !gridsEqual(fsLifeStep(g), g) {
		t.Errorf("block did not stay still under one step")
	}
}

func TestFSLifeUnknownPattern(t *testing.T) {
	if g := fsLifePattern("nope", 6, 6); g != nil {
		t.Errorf("unknown pattern should be nil, got %v", g)
	}
	out := fsLifeRender(map[string]string{"pattern": "nope"})
	if len(out) != 1 || !strings.Contains(out[0], "unknown pattern") {
		t.Errorf("want unknown-pattern error, got %v", out)
	}
}

// ── L-system ────────────────────────────────────────────────────────────────

func TestFSLSystemAlgae(t *testing.T) {
	rules := map[rune]string{'A': "AB", 'B': "A"}
	got := fsLSystem("A", rules, 4)
	want := []string{"A", "AB", "ABA", "ABAAB", "ABAABABA"} // lengths 1,2,3,5,8
	if len(got) != len(want) {
		t.Fatalf("want %d generations, got %d: %v", len(want), len(got), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("gen %d = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestFSParseRules(t *testing.T) {
	rules, err := fsParseRules("A=AB,B=A")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rules['A'] != "AB" || rules['B'] != "A" {
		t.Errorf("parsed rules wrong: %v", rules)
	}
	if _, err := fsParseRules("AB=x"); err == nil {
		t.Errorf("multi-rune LHS should error")
	}
}

func TestFSLSystemRenderDefaultsToAlgae(t *testing.T) {
	out := fsLSystemRender(map[string]string{"gens": "2"})
	if !strings.Contains(out[0], "L-system") {
		t.Errorf("missing header: %v", out[0])
	}
	// header + rules line + 3 generations (0,1,2)
	if len(out) != 5 {
		t.Errorf("want 5 lines, got %d: %v", len(out), out)
	}
}

// ── propositional logic ─────────────────────────────────────────────────────

func TestFSLogicAnd(t *testing.T) {
	vars, _, results, err := fsLogicEval("A and B")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Join(vars, ",") != "A,B" {
		t.Errorf("vars = %v", vars)
	}
	assertResults(t, "A and B", results, []bool{false, false, false, true})
}

func TestFSLogicImplies(t *testing.T) {
	_, _, results, err := fsLogicEval("A -> B")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertResults(t, "A -> B", results, []bool{true, true, false, true})
}

func TestFSLogicNotPrecedence(t *testing.T) {
	// "not A or B" must parse as (not A) or B, not not(A or B)
	_, _, results, err := fsLogicEval("not A or B")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertResults(t, "not A or B", results, []bool{true, true, false, true})
}

func TestFSLogicXorAndIff(t *testing.T) {
	_, _, xr, _ := fsLogicEval("A xor B")
	assertResults(t, "A xor B", xr, []bool{false, true, true, false})
	_, _, ir, _ := fsLogicEval("A <-> B")
	assertResults(t, "A <-> B", ir, []bool{true, false, false, true})
}

func TestFSLogicTautologyAndContradiction(t *testing.T) {
	_, _, taut, _ := fsLogicEval("A or not A")
	if fsLogicVerdict(taut) != "tautology — true under every assignment" {
		t.Errorf("A or not A should be a tautology: %v", taut)
	}
	_, _, contra, _ := fsLogicEval("A and not A")
	if !strings.HasPrefix(fsLogicVerdict(contra), "contradiction") {
		t.Errorf("A and not A should be a contradiction: %v", contra)
	}
}

func TestFSLogicErrors(t *testing.T) {
	for _, expr := range []string{"A and", "(A", "A and B and C and D and E and F and G", "A @ B"} {
		if _, _, _, err := fsLogicEval(expr); err == nil {
			t.Errorf("%q should error", expr)
		}
	}
}

// ── the dispatcher and view ─────────────────────────────────────────────────

func TestFSCompute(t *testing.T) {
	cases := []struct{ spec, want string }{
		{"", "empty"},
		{"bogus", "unknown system"},
		{"ca rule:90 gens:4", "cellular automaton"},
		{"life pattern:glider", "game of life"},
		{"lsystem", "L-system"},
		{"logic A and B", "truth table"},
	}
	for _, c := range cases {
		out := fsCompute(c.spec)
		if len(out) == 0 || !strings.Contains(out[0], c.want) {
			t.Errorf("fsCompute(%q)[0] = %q, want substring %q", c.spec, first(out), c.want)
		}
	}
	// a bounded CA renders exactly one row per generation after its header
	if out := fsCompute("ca rule:90 gens:4 width:9"); len(out) != 5 {
		t.Errorf("ca gens:4 → want 5 lines, got %d", len(out))
	}
}

func TestFSViewLifecycle(t *testing.T) {
	h := newFakeHost(t)
	n := &fakeNode{uuid: "u1", typ: "formalsystem", text: "logic A and B"}

	if !(fsView{}).Enter(h, n) {
		t.Fatal("Enter should focus a non-empty spec")
	}
	if _, ok := h.NodeStore("u1")["fsOut"]; !ok {
		t.Error("Enter should cache the rendered output")
	}
	if got, want := (fsView{}).Lines(h, n, 80), 1+len(fsCompute(n.text)); got != want {
		t.Errorf("Lines = %d, want %d", got, want)
	}
	if bands := (fsView{}).Bands(h, n, "", 80, 0, 3, true); len(bands) != 3 {
		t.Errorf("Bands windowed to winH=3 gave %d lines", len(bands))
	}
	// down scrolls, up clamps at zero
	if _, handled := (fsView{}).Key(h, n, tea.KeyMsg{Type: tea.KeyDown}); !handled || h.scroll != 1 {
		t.Errorf("down should scroll to 1, got handled=%v scroll=%d", handled, h.scroll)
	}
	if _, handled := (fsView{}).Key(h, n, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")}); handled {
		t.Error("a plain rune should fall through (not handled) in a read-only view")
	}
	(fsView{}).Leave(h, n)
	if _, ok := h.NodeStore("u1")["fsOut"]; ok {
		t.Error("Leave should clear the cache")
	}
}

func TestFSViewDeclinesEmpty(t *testing.T) {
	h := newFakeHost(t)
	n := &fakeNode{uuid: "u2", typ: "formalsystem", text: "   "}
	if (fsView{}).Enter(h, n) {
		t.Error("Enter should decline an empty spec")
	}
}

// ── helpers ─────────────────────────────────────────────────────────────────

func rowsEqual(a, b []bool) bool {
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

func gridsEqual(a, b [][]bool) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !rowsEqual(a[i], b[i]) {
			return false
		}
	}
	return true
}

func live(g [][]bool) int {
	n := 0
	for _, row := range g {
		for _, c := range row {
			if c {
				n++
			}
		}
	}
	return n
}

func assertResults(t *testing.T, expr string, got, want []bool) {
	t.Helper()
	if !rowsEqual(got, want) {
		t.Errorf("%q results = %v, want %v", expr, got, want)
	}
}

func first(s []string) string {
	if len(s) == 0 {
		return ""
	}
	return s[0]
}
