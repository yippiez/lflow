#!/usr/bin/env bash
set -euo pipefail
DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"; source "$DIR/lib.sh"

# Regression: The Temporary Domain must be an always-visible panel at the bottom
# of the screen, NOT a full-screen window swap reached by alt+t. Pressing Down past
# the last main-outline node must move focus into the temp panel (not swap views).
# Nodes typed in the temp panel must render with the dashed ◌ glyph.
#
# Repro:
#   1. Launch, type "only node" (the sole main-outline node)
#   2. Press Down — cursor must cross the divider/status-bar into the temp panel
#   3. Type "scratch" — must land as a ◌ node in the temp panel
#   4. Assert: ◌ scratch is visible AND ○ only node is still visible (two-region layout)
#
# Fix commit: acaed3f

setup; launch

# Step 1: build the main outline — a single node.
type "only node"

wait_for "○ only node"

# The temp panel must already be visible before we navigate into it.
# The dashed ◌ glyph marks temp nodes — it must be present even while we are
# still focused on the main outline.
assert_contains "◌"

# Both regions must be on screen simultaneously (main on top, temp below).
# The main node is visible.
assert_contains "○ only node"

# Step 2: press Down — this moves focus past the last main node and INTO the
# temp panel (no window swap, no screen replacement).
send Down

# The main node must still be visible (it does NOT disappear in the fixed layout).
wait_for "◌"
assert_contains "○ only node"

# Step 3: type "scratch" — it lands in the temp panel. Temp defaults to a worker
# node, so it renders with the dashed ◌ glyph plus the ✦ worker sign.
type "scratch"

wait_for "scratch"

# Core assertion: both the main node and the temp node are visible at the same time.
assert_contains "○ only node"
assert_contains "scratch"
assert_contains "◌"            # the dashed temp glyph is present

# The temp node must use the dashed glyph, not the plain bullet.
assert_not_contains "○ scratch"

# Verify layout order: main node appears ABOVE the temp node in the pane.
pane="$(snapshot)"
main_line="$(printf '%s\n' "$pane" | grep -n '○ only node' | head -1 | cut -d: -f1)"
temp_line="$(printf '%s\n' "$pane" | grep -n 'scratch' | head -1 | cut -d: -f1)"
if ! (( main_line < temp_line )); then
    fail "expected '○ only node' above '◌ scratch', got main on line ${main_line}, temp on line ${temp_line}"
fi

assert_no_crash
pass
