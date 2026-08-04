package editor

import (
	"strings"
	"testing"
)

func TestMolLooksLikeNotationAccepts(t *testing.T) {
	for _, s := range []string{
		"CCO",                          // ethanol, a clean run of element symbols
		"CC(=O)O",                      // acetic acid: branch + double bond
		"c1ccccc1",                     // benzene: balanced ring labels
		"CN1C=NC2=C1C(=O)N(C(=O)N2C)C", // caffeine
		"[C][C][O]",                    // SELFIES
	} {
		if !molLooksLikeNotation(s) {
			t.Errorf("molLooksLikeNotation(%q) = false, want true", s)
		}
	}
}

// The SMILES grammar is permissive enough that prose can look like chemistry;
// these are the words the strict detector must refuse.
func TestMolLooksLikeNotationRejectsProse(t *testing.T) {
	for _, s := range []string{
		"Once",   // 'e' is not an element
		"cat",    // 'a','t' are not elements
		"SIN",    // element letters, but no structure and it is a word
		"IS",     // too short
		"the",    // plain prose
		"C",      // a single atom is not worth a chip
		"CC(=O",  // unbalanced branch
		"c1cccc", // unbalanced ring label
		"acetic", // prose that starts with an element letter
		"",       // nothing
	} {
		if molLooksLikeNotation(s) {
			t.Errorf("molLooksLikeNotation(%q) = true, want false", s)
		}
	}
}

func TestMolChipDisplayTruncates(t *testing.T) {
	// the chip is a painted block: the ring, the notation, and the padding that
	// gives the background its pill shape.
	if got := molChipDisplay("CCO"); got != " ⌬ CCO " {
		t.Fatalf("display = %q, want \" ⌬ CCO \"", got)
	}
	long := molChipDisplay("CN1C=NC2=C1C(=O)N(C(=O)N2C)C")
	if !strings.HasPrefix(long, " ⌬ ") || !strings.HasSuffix(long, "… ") {
		t.Fatalf("truncated display = %q, want ring + clipped notation + ellipsis", long)
	}
	if got := len([]rune(long)); got != 3+molChipMaxDisplay+2 {
		t.Fatalf("truncated display %q is %d runes, want %d", long, got, 3+molChipMaxDisplay+2)
	}
	if got := molChipDisplay(""); got != " ⌬ " {
		t.Fatalf("empty display = %q, want the bare ring", got)
	}
}

func TestMolTokenBeforeCaretDetects(t *testing.T) {
	it := &item{name: "made from CC(=O)O today"}
	// caret just after the notation token
	tok, start, end, ok := molTokenBeforeCaret(it, len([]rune("made from CC(=O)O")))
	if !ok {
		t.Fatal("expected the notation before the caret to be detected")
	}
	if tok != "CC(=O)O" {
		t.Fatalf("tok = %q, want CC(=O)O", tok)
	}
	if got := string([]rune(it.name)[start:end]); got != "CC(=O)O" {
		t.Fatalf("span %d:%d = %q, want CC(=O)O", start, end, got)
	}
	// caret sitting in ordinary prose finds nothing
	if _, _, _, ok := molTokenBeforeCaret(it, len([]rune("made from"))); ok {
		t.Fatal("prose before the caret must not be detected as a molecule")
	}
}

func TestMolConvertBeforeCaretFoldsTokenInPlace(t *testing.T) {
	m := newTestModel(80)
	cur := &item{name: "solvent CCO here"}
	m.caret = len([]rune("solvent CCO"))
	if !m.molConvertBeforeCaret(cur) {
		t.Fatal("expected the detected notation to convert")
	}
	if !hasAnchor(cur.name) {
		t.Fatalf("name %q should carry a chip anchor", cur.name)
	}
	// the surrounding prose survives and the notation is gone from the text
	if got := expandAnchors(cur.name, m.chips); got != "solvent CCO here" {
		t.Fatalf("expanded = %q, want the original text back", got)
	}
	// a second conversion at prose does nothing
	if m.molConvertBeforeCaret(&item{name: "just words"}) {
		t.Fatal("prose must not convert")
	}
}
