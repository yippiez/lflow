package editor

import (
	"encoding/json"
	"os"
	"testing"
)

func TestDumpGraphs(t *testing.T) {
	if os.Getenv("MOL_DUMP") == "" {
		t.Skip("set MOL_DUMP=1")
	}
	mols := map[string]string{
		"ethanol":     "CCO",
		"acetic_acid": "CC(=O)O",
		"benzene":     "c1ccccc1",
		"pyridine":    "c1ccncc1",
		"caffeine":    "CN1C=NC2=C1C(=O)N(C(=O)N2C)C",
		"aspirin":     "CC(=O)OC1=CC=CC=C1C(=O)O",
		"naphthalene": "c1ccc2ccccc2c1",
		"glucose":     "OCC1OC(O)C(O)C(O)C1O",
	}
	type atomJ struct {
		Sym  string  `json:"sym"`
		Arom bool    `json:"arom"`
		X    float64 `json:"x"`
		Y    float64 `json:"y"`
	}
	type bondJ struct {
		A     int  `json:"a"`
		B     int  `json:"b"`
		Order int  `json:"order"`
		Arom  bool `json:"arom"`
	}
	type molJ struct {
		SMILES  string  `json:"smiles"`
		Formula string  `json:"formula"`
		Atoms   []atomJ `json:"atoms"`
		Bonds   []bondJ `json:"bonds"`
	}
	out := map[string]molJ{}
	for name, smi := range mols {
		g, err := parseMolecule(smi)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		m := molJ{SMILES: smi, Formula: g.formula()}
		pos := layoutMolecule(g)
		for i, a := range g.atoms {
			m.Atoms = append(m.Atoms, atomJ{a.sym, a.arom, pos[i].x, pos[i].y})
		}
		for _, b := range g.bonds {
			m.Bonds = append(m.Bonds, bondJ{b.a, b.b, b.order, b.arom})
		}
		out[name] = m
	}
	b, _ := json.MarshalIndent(out, "", "  ")
	os.WriteFile("/tmp/molgraphs.json", b, 0o644)
	t.Logf("wrote /tmp/molgraphs.json (%d molecules)", len(out))
}
