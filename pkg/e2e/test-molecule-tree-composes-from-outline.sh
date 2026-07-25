#!/usr/bin/env bash
set -euo pipefail
DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"; source "$DIR/lib.sh"

# Feature: a Molecule can be composed AS an outline (like the Math node) instead
# of typed as one notation string. A node's text is one atom — the bond to its
# parent as a prefix (=O) — and its children are the atoms bonded to it. The
# outline structure IS the molecular graph.
#
# Verifies: Enter keeps the Molecule type going while building (continueOnEnter),
# each composed row shows a dim preview of its flattened subtree, and alt+e
# renders the molecule built from the tree (format reads "tree", not SMILES).

WIN_W=100
WIN_H=30

setup; launch

# --- make the first node a Molecule via the /type picker ---
type "/"
type "type"
send Enter
wait_for "type:"
type "molecule"
send Enter

# --- build acetic acid as an outline: C > C > (=O, O) ---
# Enter keeps the Molecule type, so the whole tree is atoms without retyping.
type "C"
send Enter
send Tab
type "C"
send Enter
send Tab
type "=O"
send Enter
type "O"

# the root row carries a dim preview of the flattened subtree + formula
wait_for "CC(=O)O"
assert_contains "C2H4O2"

# --- alt+e renders the molecule built from the TREE ---
send Up
send Up
send Up
send M-e

# the info bar reports the tree source (not a typed SMILES string) and the
# chemistry derived from the outline: acetic acid, MW 60.05, 4 heavy atoms
wait_for "molecule · tree"
assert_contains "C2H4O2"
assert_contains "4 atoms"
assert_contains "MW 60.05"
assert_contains "tab switch"

# esc closes the viewer, leaving the outline intact
send Escape
wait_for "CC(=O)O"

assert_no_crash
pass
