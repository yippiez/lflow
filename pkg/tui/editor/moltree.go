package editor

import (
	"fmt"
	"strings"
)

// A Molecule node can be written two ways, and this file is the second one: the
// molecule composed AS an outline, the way the Math node composes an expression
// (see math.go). A node's text is one ATOM — an element symbol, optionally with
// the bond to its PARENT as a prefix (=O, #N) and ring labels as digits (C1) —
// and its children are the atoms bonded to it. The outline structure IS the
// molecular graph.
//
// A LEAF keeps the original behavior: its text is a whole SMILES/SELFIES string.
// So "simple stays inline, complex fans out into a tree" — the same rule math
// uses — and both forms feed the identical alt+e viewer.
//
// The serializer is the trick that keeps the two forms honest: a node's text is
// already a valid SMILES fragment, so the subtree flattens to SMILES by walking
// children and parenthesizing all but the last (exactly SMILES branch notation).
// The graph then comes from the SAME parser the typed form uses, so the inline
// preview and the rendered structure can never disagree.

// molAtomText is a node's contribution to the flattened notation: its text with
// any chip anchors removed.
//
// WARNING (invariant): a name may carry chip anchors, and every surface that
// reads a name must resolve them first. An anchor is a sentinel-delimited uuid,
// never chemistry, so an atom node drops it — otherwise the sentinel and id leak
// into both the row preview and the parsed graph.
func molAtomText(name string) string {
	if hasAnchor(name) {
		runes := []rune(name)
		var b strings.Builder
		i := 0
		for _, sp := range anchorSpans(runes) {
			b.WriteString(string(runes[i:sp.start]))
			i = sp.end
		}
		b.WriteString(string(runes[i:]))
		name = b.String()
	}
	return strings.TrimSpace(name)
}

// molTreeSMILES flattens a molecule subtree into one SMILES string. Pure and
// recursive: the node's own text is a fragment, each child is a branch, and the
// last child continues the main chain unparenthesized.
func molTreeSMILES(it *item) string {
	if it == nil {
		return ""
	}
	var b strings.Builder
	b.WriteString(molAtomText(it.name))
	kids := it.children
	for i, c := range kids {
		sub := molTreeSMILES(c)
		if sub == "" {
			continue
		}
		if i < len(kids)-1 {
			b.WriteString("(" + sub + ")")
		} else {
			b.WriteString(sub)
		}
	}
	return b.String()
}

// moleculeGraphOf resolves whichever form the node uses: a node WITH children is
// an outline-composed molecule (flattened, then parsed); a leaf is a typed
// SMILES/SELFIES string. Every consumer — the viewer, the preview, the formula —
// goes through here, so the two input forms stay interchangeable.
func moleculeGraphOf(it *item) (*molGraph, error) {
	if it == nil {
		return nil, fmt.Errorf("empty")
	}
	if len(it.children) > 0 {
		g, err := parseSMILES(molTreeSMILES(it))
		if err != nil {
			return nil, err
		}
		g.format = "tree"
		return g, nil
	}
	return parseMolecule(it.name)
}

// molTreeBodyTail is the dim linear preview of an outline-composed molecule,
// shown after the atom on its row — the flattened SMILES plus the formula, so
// the whole structure reads at a glance without expanding it. A leaf already IS
// its own notation, so it gets no tail.
func molTreeBodyTail(it *item) string {
	if it == nil || len(it.children) == 0 {
		return ""
	}
	smi := molTreeSMILES(it)
	if smi == "" {
		return ""
	}
	tail := "  " + smi
	if g, err := parseSMILES(smi); err == nil {
		tail += " · " + g.formula()
	}
	return cDim + tail + cReset
}

// molSpanColor tints a molecule body per rune while it stays inline-editable —
// the same channel the Math node paints its operators through (see renderBody).
// Element symbols take their CPK color, bond prefixes the bond-order color, and
// ring labels / brackets go dim, so an atom row is readable as chemistry without
// any markup in the stored text.
func molSpanColor(it *item, runes []rune) map[int]string {
	if len(runes) == 0 {
		return nil
	}
	// A leaf holds a whole notation string; coloring every element inside a long
	// SMILES turns the row into confetti, so only atom-per-node rows are tinted.
	if it != nil && len(it.children) == 0 && len(runes) > 4 {
		return nil
	}
	out := make(map[int]string)
	for i := 0; i < len(runes); {
		switch r := runes[i]; {
		case r == '=':
			out[i] = cYellow // double bond to the parent
			i++
		case r == '#':
			out[i] = cRed // triple bond to the parent
			i++
		case r == '-' || r == ':' || r == '/' || r == '\\':
			out[i] = cDim
			i++
		case r >= '0' && r <= '9', r == '%', r == '[', r == ']', r == '(', r == ')':
			out[i] = cDim // ring labels and brackets are structure, not atoms
			i++
		default:
			sym, _, adv := parseOrganicAtom(runes, i)
			if adv == 0 {
				i++
				continue
			}
			col := rgbFg(atomRGB(sym), 1.0)
			for k := i; k < i+adv; k++ {
				out[k] = col
			}
			i += adv
		}
	}
	return out
}
