#!/usr/bin/env bash
set -euo pipefail
DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"; source "$DIR/lib.sh"

# The Agent Domain is a bottom panel reached by pressing Down past the last main
# node (not a window swap). Its always-empty compose line (◌ ✦) is HIDDEN while
# focus is in the notes, and appears once you enter the panel. Both regions stay
# on screen together (main on top, panel below).

setup; launch

# Step 1: a single main node.
type "only node"
wait_for "○ only node"

# The empty compose line is hidden while focused in the notes.
assert_not_contains "◌"

# Step 2: Down enters the Agent Domain panel (no window swap) — compose appears.
send Down
wait_for "◌"
assert_contains "○ only node" # main stays visible above

# Step 3: type into the compose line (a temp worker — dashed ◌ glyph).
type "scratch"
wait_for "scratch"
assert_contains "○ only node"
assert_contains "◌"
assert_contains "scratch"
assert_not_contains "○ scratch" # temp uses the dashed glyph, not a plain bullet

# Layout order: the main node is ABOVE the temp node.
pane="$(snapshot)"
main_line="$(printf '%s\n' "$pane" | grep -n '○ only node' | head -1 | cut -d: -f1)"
temp_line="$(printf '%s\n' "$pane" | grep -n 'scratch' | head -1 | cut -d: -f1)"
if ! (( main_line < temp_line )); then
    fail "expected '○ only node' above the temp node, got main=${main_line} temp=${temp_line}"
fi

assert_no_crash
pass
