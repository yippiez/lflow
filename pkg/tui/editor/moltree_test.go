package editor

import (
	"strings"
	"testing"
)

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

// A molecule node has exactly ONE form: the outline. Even a childless node is
// read as a one-node tree, never as a typed-notation leaf — an inline notation
// reference is a molecule CHIP instead.
func TestMoleculeNodeIsAlwaysTreeFormat(t *testing.T) {
	for _, name := range []string{"CCO", "c1ccccc1"} {
		g, err := moleculeGraphOf(molNode(name))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if g.format != "tree" {
			t.Fatalf("%s: format = %q, want tree", name, g.format)
		}
	}
	// the notation string itself still parses — that is the chip's job
	if !molLooksLikeNotation("[C][C][O]") {
		t.Fatal("SELFIES should still be detectable as chip notation")
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

// A molecule node's name may carry chip anchors (a ⌬ chip pasted into an atom
// row). An anchor is a sentinel-delimited uuid, never chemistry — it must never
// reach the preview or the parsed graph.
func TestMolTreeSMILESDropsChipAnchors(t *testing.T) {
	tree := molNode("C"+chipAnchor("1c38af6c-83ff-4a75-a0c4-ce442e73e777"), molNode("O"))
	got := molTreeSMILES(tree)
	if got != "CO" {
		t.Fatalf("molTreeSMILES = %q, want CO (anchor dropped)", got)
	}
	if strings.ContainsRune(got, chipSentinel) {
		t.Fatalf("flattened notation %q still carries a chip sentinel", got)
	}
	if _, err := moleculeGraphOf(tree); err != nil {
		t.Fatalf("graph from anchored tree: %v", err)
	}
}
