#!/usr/bin/env bash
set -euo pipefail
DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"; source "$DIR/lib.sh"

# Regression test for: "Enter at start of a node pushes the node and its children down"
# Fix commit: 4c3f2c4
#
# Before the fix, pressing Enter with the caret at position 0 would split the
# node text (leaving the node empty) and create a new sibling with all the text,
# but crucially the children would be left on the original (now empty) node
# rather than following the text. The subtree was broken apart.
#
# After the fix, Enter at position 0 opens an EMPTY node ABOVE the original and
# moves cursor there. The original node (with its full subtree) shifts down
# intact as a unit.
#
# Repro steps:
#   1. Type "parent"; send Enter
#   2. Type "child"; send Tab (child becomes a child of parent)
#   3. Send Up (cursor back onto parent)
#   4. Send Home (caret to start of "parent")
#   5. Send Enter
#   6. Type "X"
#
# Expected correct behavior:
#   - A new node X appears ABOVE the original parent
#   - "parent" keeps its child "child" beneath it (subtree moved down as a unit)
#   - X is a sibling at the same level, ordered before parent

setup; launch

# Step 1+2: Build the initial tree
#   ○ parent
#   ╰─ ○ child
type "parent"
send Enter
type "child"
send Tab          # child becomes a child of parent

wait_for "╰─ ○ child"
assert_contains "○ parent"

# Step 3: Move cursor up onto parent
send Up

# Step 4: Jump caret to start of "parent"
send Home

# Step 5: Press Enter — must push parent+subtree down, open empty node above
send Enter

# Step 6: Type "X" in the newly opened node above
type "X"

# Wait for X to appear
wait_for "○ X"

# --- Assertions: correct (post-fix) behavior ---

# X must exist as a rendered node
assert_contains "○ X"

# parent must still exist
assert_contains "○ parent"

# child must still be attached under parent (tree prefix visible)
assert_contains "╰─ ○ child"

# Verify ordering: X appears before parent, parent appears before child
pane="$(snapshot)"
x_line="$(printf '%s\n' "$pane"    | grep -n '○ X'       | head -1 | cut -d: -f1)"
par_line="$(printf '%s\n' "$pane"  | grep -n '○ parent'  | head -1 | cut -d: -f1)"
ch_line="$(printf '%s\n' "$pane"   | grep -n '○ child'   | head -1 | cut -d: -f1)"

if [[ -z "$x_line" || -z "$par_line" || -z "$ch_line" ]]; then
    fail "could not locate all three nodes in the pane (X=${x_line} parent=${par_line} child=${ch_line})"
fi

if ! (( x_line < par_line && par_line < ch_line )); then
    fail "expected render order X < parent < child, got lines X=${x_line} parent=${par_line} child=${ch_line}"
fi

assert_no_crash
pass
