package nodes

import "github.com/lflow/lflow/packages/database"

// a molecule is an ordinary subtree READ as a structure: one atom per node,
// the bond to the parent as a prefix (=O), children the atoms bonded to it —
// so the outline IS the molecular graph, the way the Math node is the AST.
// alt+e draws it. A notation string mentioned inline is a ⌬ chip instead.
//
// spanColor, bodyTail and view stay in the editor's attachment (see
// molecule.go/moltree.go's init): spanColor needs to know whether the node is
// a leaf, which Plugin.SpanColor's signature (runes only, no Ref) cannot
// express; bodyTail's SMILES flattening runs through parseSMILES/molGraph and
// the chip-anchor stripping in packages/editor/chip.go, editor-internal
// machinery a Ref can't reach; view is the Model-bound 2D viewer.
func init() {
	Register(Plugin{
		Key:            database.TypeMol,
		Label:          "Molecule",
		InlineEditable: true,
		// building a molecule as an outline is atom-after-atom, so Enter keeps the
		// type going (the todo-list continuation) — otherwise every second atom
		// would land as a bullet and need retyping.
		ContinueOnEnter: true,
		Glyph:           moleculeGlyph,
	})
}

// moleculeGlyph is the benzene ring, muted — the row's chemistry is carried by
// the atom text and its preview, so the glyph stays quiet like a bullet.
func moleculeGlyph() (string, string) { return "⌬", Theme().Dim }
