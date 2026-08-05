#!/usr/bin/env bash
set -euo pipefail
DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"; source "$DIR/lib.sh"

# Feature: the "molecule" node type holds a SMILES (or SELFIES) string as its
# text, and alt+e opens an inline 2D node-link viewer — atoms are circles,
# bonds are lines — rendered as bands beneath the node (never a separate
# screen). Depth is shaded into the background, so the drawing reads like a
# circuit. This verifies the round trip: type SMILES → /type Molecule → alt+e.

WIN_W=90
WIN_H=30

setup; launch

# --- type a SMILES string as the node text, then convert it to a molecule ---
type "c1ccccc1" # benzene
send Enter
send Up # back onto the benzene node (Enter dropped a fresh node below)

# open the slash menu → /type picker → filter to Molecule → select
type "/"
type "type"
send Enter
wait_for "type:"
type "molecule"
send Enter

# the node now carries the molecule glyph in front of its SMILES text
wait_for "⌬ c1ccccc1"

# --- alt+e opens the inline 2D viewer ---
send M-e

# the properties line reports format, formula, weight and counts — the panel is
# that line plus the drawing, with no rules around it
wait_for "molecule · tree"
assert_contains "C6H6"
assert_contains "6 atoms"
assert_contains "MW"
assert_contains "6 bonds"

# the panel carries no horizontal rules — just properties and the picture
assert_not_contains "────────"

# esc collapses the viewer back to the plain node (json/agent close pattern)
send Escape
wait_for "⌬ c1ccccc1"
assert_not_contains "atoms"

assert_no_crash
pass
