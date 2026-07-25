package editor

import "testing"

// node builds a molecule item tree for the tests: name plus children.
func molNode(name string, kids ...*item) *item {
	it := &item{name: name}
	it.children = append(it.children, kids...)
	return it
}

func TestMolTreeSMILESChain(t *testing.T) {
	// C -> C -> O  ==  CCO (ethanol)
	tree := molNode("C", molNode("C", molNode("O")))
	if got := molTreeSMILES(tree); got != "CCO" {
		t.Fatalf("molTreeSMILES = %q, want CCO", got)
	}
}

func TestMolTreeSMILESBranch(t *testing.T) {
	// acetic acid: C -> C( =O )( O )  ==  CC(=O)O
	tree := molNode("C", molNode("C", molNode("=O"), molNode("O")))
	if got := molTreeSMILES(tree); got != "CC(=O)O" {
		t.Fatalf("molTreeSMILES = %q, want CC(=O)O", got)
	}
}

func TestMolTreeGraphMatchesTypedSMILES(t *testing.T) {
	// the whole point: the outline form and the typed form must produce the
	// same molecule, because both go through the same parser.
	tree := molNode("C", molNode("C", molNode("=O"), molNode("O")))
	fromTree, err := moleculeGraphOf(tree)
	if err != nil {
		t.Fatal(err)
	}
	typed, err := parseMolecule("CC(=O)O")
	if err != nil {
		t.Fatal(err)
	}
	if len(fromTree.atoms) != len(typed.atoms) || len(fromTree.bonds) != len(typed.bonds) {
		t.Fatalf("tree %d atoms/%d bonds != typed %d atoms/%d bonds",
			len(fromTree.atoms), len(fromTree.bonds), len(typed.atoms), len(typed.bonds))
	}
	if fromTree.formula() != typed.formula() {
		t.Fatalf("formula %q != %q", fromTree.formula(), typed.formula())
	}
	if fromTree.format != "tree" {
		t.Fatalf("format = %q, want tree", fromTree.format)
	}
}

func TestMolTreeRingClosure(t *testing.T) {
	// benzene as a chain of aromatic carbons closed by the shared ring label 1.
	tree := molNode("c1", molNode("c", molNode("c", molNode("c", molNode("c", molNode("c1"))))))
	g, err := moleculeGraphOf(tree)
	if err != nil {
		t.Fatal(err)
	}
	if len(g.atoms) != 6 || len(g.bonds) != 6 {
		t.Fatalf("got %d atoms / %d bonds, want 6/6", len(g.atoms), len(g.bonds))
	}
	if f := g.formula(); f != "C6H6" {
		t.Fatalf("formula = %q, want C6H6", f)
	}
}

func TestMolLeafStillParsesNotation(t *testing.T) {
	// a childless node keeps the original typed-notation behavior.
	g, err := moleculeGraphOf(molNode("CCO"))
	if err != nil {
		t.Fatal(err)
	}
	if g.format != "SMILES" || len(g.atoms) != 3 {
		t.Fatalf("leaf = %+v, want SMILES with 3 atoms", g.format)
	}
	sel, err := moleculeGraphOf(molNode("[C][C][O]"))
	if err != nil {
		t.Fatal(err)
	}
	if sel.format != "SELFIES" {
		t.Fatalf("format = %q, want SELFIES", sel.format)
	}
}

func TestMolTreeBodyTailOnlyWhenComposed(t *testing.T) {
	if tail := molTreeBodyTail(molNode("CCO")); tail != "" {
		t.Fatalf("leaf tail = %q, want empty", tail)
	}
	tail := molTreeBodyTail(molNode("C", molNode("C", molNode("=O"), molNode("O"))))
	if tail == "" {
		t.Fatal("composed node should carry a preview tail")
	}
	if !contains(tail, "CC(=O)O") || !contains(tail, "C2H4O2") {
		t.Fatalf("tail %q should preview the flattened SMILES and formula", tail)
	}
}

func TestMolSpanColorTintsAtomAndBond(t *testing.T) {
	runes := []rune("=O")
	got := molSpanColor(molNode("=O"), runes)
	if got[0] != cYellow {
		t.Fatalf("double-bond prefix not tinted yellow: %q", got[0])
	}
	if got[1] == "" {
		t.Fatal("element symbol should be tinted")
	}
	// a long typed notation leaf stays uncolored (no confetti).
	if c := molSpanColor(molNode("CN1C=NC2=C1"), []rune("CN1C=NC2=C1")); c != nil {
		t.Fatal("long leaf notation should not be per-rune tinted")
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})()
}
