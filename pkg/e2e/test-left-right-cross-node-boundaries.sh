#!/usr/bin/env bash
set -euo pipefail
DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"; source "$DIR/lib.sh"

# Regression: Left at the start of a node must cross into the previous node and
# land at its end; Right at the end of a node must cross into the next node and
# land at its start.
#
# Repro for Left boundary crossing (the originally failing direction):
#   1. type "aaa"; send Enter
#   2. type "bbb"           — two sibling nodes: aaa, bbb
#   3. send Home            — caret jumps to start of bbb (pos 0)
#   4. send Left            — must cross UP into aaa and land at end (pos 3)
#   5. type "Z"             — should insert at end of aaa => "aaaZ"
#
# Expected correct behavior:
#   - "aaa" becomes "aaaZ" (text inserted at end of the previous node)
#   - "bbb" is unchanged
#   - The boundary crossing must NOT leave the caret stuck in bbb
#
# Also exercises Right boundary crossing:
#   After the Left test above, the state is: "aaaZ" (cursor), "bbb" below.
#   Send End to reach the end of aaaZ, then Right — must cross down into bbb
#   (caret at pos 0). Type "W" => "bbb" becomes "Wbbb".
#
# Fix commit: 18f00aa

setup; launch

# --- Build the two-node scenario ---
type "aaa"
send Enter
type "bbb"

wait_for "○ bbb"
assert_contains "○ aaa"

# --- Left boundary crossing ---
# Move caret to start of bbb, then cross Left into aaa.
send Home
send Left

# Type Z — must land in aaa, appending to produce "aaaZ".
type "Z"

wait_for "○ aaaZ"

# The specific regression assertion: "aaa" became "aaaZ" because the caret
# crossed the node boundary. If the old bug were present, Z would insert into
# bbb (producing "Zbbb") and "aaa" would be unchanged.
assert_contains "○ aaaZ"
assert_not_contains "○ Zbbb"
assert_contains "○ bbb"

# --- Right boundary crossing ---
# We are currently on aaaZ with caret somewhere inside it. Send End to reach
# the very end of aaaZ, then Right to cross down into bbb.
send End
send Right

# Type W — must insert at the start of bbb producing "Wbbb".
type "W"

wait_for "○ Wbbb"

assert_contains "○ Wbbb"
assert_not_contains "○ bbb"

assert_no_crash
pass
