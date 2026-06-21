package editor

import "testing"

func degree(g *molGraph, atom int) int {
	d := 0
	for _, b := range g.bonds {
		if b.a == atom || b.b == atom {
			d++
		}
	}
	return d
}

func TestParseSMILESChain(t *testing.T) {
	g, err := parseMolecule("CCO") // ethanol skeleton
	if err != nil {
		t.Fatal(err)
	}
	if g.format != "SMILES" {
		t.Fatalf("format = %q, want SMILES", g.format)
	}
	if len(g.atoms) != 3 || len(g.bonds) != 2 {
		t.Fatalf("got %d atoms / %d bonds, want 3/2", len(g.atoms), len(g.bonds))
	}
	if g.atoms[2].sym != "O" {
		t.Fatalf("atom[2] = %q, want O", g.atoms[2].sym)
	}
	if f := g.formula(); f != "C2H6O" {
		t.Fatalf("formula = %q, want C2H6O", f)
	}
}

func TestParseSMILESDoubleBond(t *testing.T) {
	g, err := parseMolecule("C=O")
	if err != nil {
		t.Fatal(err)
	}
	if len(g.bonds) != 1 || g.bonds[0].order != 2 {
		t.Fatalf("bonds = %+v, want one order-2 bond", g.bonds)
	}
}

func TestParseSMILESBranch(t *testing.T) {
	g, err := parseMolecule("CC(C)C") // isobutane: central C degree 3
	if err != nil {
		t.Fatal(err)
	}
	if len(g.atoms) != 4 || len(g.bonds) != 3 {
		t.Fatalf("got %d atoms / %d bonds, want 4/3", len(g.atoms), len(g.bonds))
	}
	if d := degree(g, 1); d != 3 {
		t.Fatalf("central atom degree = %d, want 3", d)
	}
}

func TestParseSMILESRingBenzene(t *testing.T) {
	g, err := parseMolecule("c1ccccc1")
	if err != nil {
		t.Fatal(err)
	}
	if len(g.atoms) != 6 || len(g.bonds) != 6 {
		t.Fatalf("got %d atoms / %d bonds, want 6/6", len(g.atoms), len(g.bonds))
	}
	for i := range g.atoms {
		if d := degree(g, i); d != 2 {
			t.Fatalf("ring atom %d degree = %d, want 2", i, d)
		}
	}
	if f := g.formula(); f != "C6H6" {
		t.Fatalf("formula = %q, want C6H6", f)
	}
}

func TestParseSMILESTwoLetterAtom(t *testing.T) {
	g, err := parseMolecule("CCl")
	if err != nil {
		t.Fatal(err)
	}
	if len(g.atoms) != 2 || g.atoms[1].sym != "Cl" {
		t.Fatalf("atoms = %+v, want C + Cl", g.atoms)
	}
}

func TestSelfiesDetection(t *testing.T) {
	if !looksLikeSELFIES("[C][C][O]") {
		t.Fatal("[C][C][O] should look like SELFIES")
	}
	if looksLikeSELFIES("CCO") {
		t.Fatal("CCO should not look like SELFIES")
	}
	if looksLikeSELFIES("C[C]O") {
		t.Fatal("mixed C[C]O should not look like SELFIES")
	}
}

func TestParseSELFIESChain(t *testing.T) {
	g, err := parseMolecule("[C][C][O]")
	if err != nil {
		t.Fatal(err)
	}
	if g.format != "SELFIES" {
		t.Fatalf("format = %q, want SELFIES", g.format)
	}
	if len(g.atoms) != 3 || len(g.bonds) != 2 {
		t.Fatalf("got %d atoms / %d bonds, want 3/2", len(g.atoms), len(g.bonds))
	}
}

func TestParseSELFIESDoubleBond(t *testing.T) {
	g, err := parseMolecule("[C][=C]")
	if err != nil {
		t.Fatal(err)
	}
	if len(g.bonds) != 1 || g.bonds[0].order != 2 {
		t.Fatalf("bonds = %+v, want one order-2 bond", g.bonds)
	}
}

func TestParseSELFIESBranch(t *testing.T) {
	// [C] [Branch1] [C] [O] [C]:
	//  - atom0 C
	//  - Branch1 reads 1 index symbol [C] (index 0 → Q=0) → branch body is the
	//    next Q+1=1 symbol [O], bonded to atom0
	//  - main chain continues from atom0 with [C] → atom2 bonded to atom0
	g, err := parseMolecule("[C][Branch1][C][O][C]")
	if err != nil {
		t.Fatal(err)
	}
	if len(g.atoms) != 3 || len(g.bonds) != 2 {
		t.Fatalf("got %d atoms / %d bonds, want 3/2 (%+v)", len(g.atoms), len(g.bonds), g.bonds)
	}
	if d := degree(g, 0); d != 2 {
		t.Fatalf("atom0 degree = %d, want 2", d)
	}
	if g.atoms[1].sym != "O" {
		t.Fatalf("branch atom = %q, want O", g.atoms[1].sym)
	}
}

func TestParseSELFIESBenzeneRing(t *testing.T) {
	// canonical SELFIES for benzene: a 6-carbon chain closed by Ring1 back 6.
	g, err := parseMolecule("[C][=C][C][=C][C][=C][Ring1][=Branch1]")
	if err != nil {
		t.Fatal(err)
	}
	if len(g.atoms) != 6 {
		t.Fatalf("got %d atoms, want 6", len(g.atoms))
	}
	if len(g.bonds) != 6 {
		t.Fatalf("got %d bonds, want 6 (ring closure) (%+v)", len(g.bonds), g.bonds)
	}
	// the ring closure must connect the last atom (5) back to atom 0.
	closed := false
	for _, b := range g.bonds {
		if (b.a == 0 && b.b == 5) || (b.a == 5 && b.b == 0) {
			closed = true
		}
	}
	if !closed {
		t.Fatalf("expected a 5–0 ring-closure bond, got %+v", g.bonds)
	}
}

func TestRenderProducesCanvas(t *testing.T) {
	g, err := parseMolecule("c1ccccc1")
	if err != nil {
		t.Fatal(err)
	}
	lines := renderMolecule(g, 70)
	if len(lines) < 3 {
		t.Fatalf("expected a multi-line canvas, got %d lines", len(lines))
	}
}

func TestParseEmpty(t *testing.T) {
	if _, err := parseMolecule("   "); err == nil {
		t.Fatal("expected error on empty input")
	}
}
