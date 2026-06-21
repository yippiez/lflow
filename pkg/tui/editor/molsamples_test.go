package editor

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

// TestMolSamples renders a few molecules in every bond style to /tmp so the
// renderer styles can be eyeballed and one picked. Opt-in: MOL_SAMPLES=1.
func TestMolSamples(t *testing.T) {
	if os.Getenv("MOL_SAMPLES") == "" {
		t.Skip("set MOL_SAMPLES=1 to write /tmp/sample_*.ansi")
	}
	mols := []struct{ name, smiles string }{
		{"benzene", "c1ccccc1"},
		{"caffeine", "CN1C=NC2=C1C(=O)N(C(=O)N2C)C"},
		{"aspirin", "CC(=O)OC1=CC=CC=C1C(=O)O"},
	}
	styles := []struct {
		name string
		s    molStyle
	}{
		{"braille (sub-pixel, smooth)", styleBraille},
		{"arc (45° diagonal + tail)", styleArc},
		{"manhattan (orthogonal PCB)", styleManhattan},
		{"straight (bresenham)", styleStraight},
	}
	const width = 92
	for _, m := range mols {
		g, err := parseMolecule(m.smiles)
		if err != nil {
			t.Fatalf("%s: %v", m.name, err)
		}
		var b strings.Builder
		for _, st := range styles {
			fmt.Fprintf(&b, "%s  ·  %s style  ·  %s  %s  %d atoms  %d bonds\n",
				m.name, st.name, g.format, g.formula(), len(g.atoms), len(g.bonds))
			b.WriteString(strings.Repeat("─", width) + "\n")
			for _, ln := range renderMoleculeStyle(g, width, st.s) {
				b.WriteString(ln + "\n")
			}
			b.WriteString("\n")
		}
		path := "/tmp/sample_" + m.name + ".ansi"
		if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("wrote %s", path)
	}
}
