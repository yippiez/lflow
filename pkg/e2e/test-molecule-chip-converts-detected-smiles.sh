#!/usr/bin/env bash
set -euo pipefail
DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"; source "$DIR/lib.sh"

# Feature: a molecule can live inline in ordinary prose as a CHIP. /insert
# detects a SMILES/SELFIES token sitting before the caret and offers to convert
# exactly that token — the picker row reads "convert ⌬<notation>" — and the chip
# then renders as the benzene ring plus a truncated notation.

WIN_W=100
WIN_H=24

setup; launch

# --- prose with a notation token at the end ---
type "caffeine CN1C=NC2=C1C(=O)N(C(=O)N2C)C"
wait_for "caffeine CN1C"

# --- /insert DETECTS it and offers the conversion ---
type "/"
type "insert"
send Enter
type "mol"

# the offer names the detected molecule, not a generic "insert a chip"
wait_for "convert ⌬"

send Enter

# the notation is folded into one painted chip: ring glyph + truncated notation.
# the chip's padding is NBSP (nonBreaking, so a soft wrap cannot split the pill),
# so assert the ring and the notation rather than the separator between them.
wait_for "CN1C=NC2=C1C…"
assert_contains "⌬"
# the raw notation no longer sprawls across the row
assert_not_contains "N(C(=O)N2C)C"

assert_no_crash
pass
