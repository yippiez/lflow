#!/usr/bin/env bash
set -euo pipefail
DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"; source "$DIR/lib.sh"

# Regression: Backspace with the caret at the start of a node must merge the
# node's text up into the previous node rather than doing nothing or deleting
# wrongly.
#
# Repro steps:
#   1. setup; launch
#   2. type "hello"; send Enter
#   3. type "world"
#   4. send Home (caret at start of "world")
#   5. send BSpace
#
# Expected correct behavior: the two nodes merge into a single node reading
# "helloworld"; the caret sits between "hello" and "world" (i.e. there is
# exactly one node and it contains both words).
#
# Fix commit: 18f00aa

setup; launch

# Step 2: create the first node.
type "hello"
send Enter

# Step 3: create the second node on the line below.
type "world"

# Confirm both nodes are visible before the merge.
wait_for "○ hello"
wait_for "○ world"

# Step 4: jump the caret to the very start of "world".
send Home

# Step 5: Backspace at caret position 0 — must merge "world" up into "hello".
send BSpace

# Wait for the merged node to appear.
wait_for "○ helloworld"

# The merged node must be present as a single node.
assert_contains "○ helloworld"

# The two original separate nodes must no longer exist.
assert_not_contains "○ hello"$'\n'
assert_not_contains "○ world"

# There must be only one node — no second bullet that reads just "hello" or
# just "world" (the merged text subsumes both).
pane="$(snapshot)"
hello_count="$(printf '%s\n' "$pane" | grep -c '○ hello' || true)"
if (( hello_count > 1 )); then
    fail "expected exactly one node containing 'hello' after merge, found ${hello_count}"
fi

# "world" must not appear as a standalone node.
world_standalone="$(printf '%s\n' "$pane" | grep -c '^[[:space:]]*○ world$' || true)"
if (( world_standalone > 0 )); then
    fail "expected 'world' to be merged, not a standalone node"
fi

# Caret position: after the merge the block cursor (reverse-video, SGR 7) must
# sit on the 'w' that immediately follows "hello" — i.e. exactly between the two
# original words. Capture WITH escapes (-e) so the cursor cell is visible; the
# plain-text snapshot strips it. This is the half of the spec the plain checks
# above cannot see: a merge that lands the caret at the wrong offset (e.g. at the
# end of "world", or back at the document start) would still read "helloworld"
# in plain text but fail here.
LAST_PANE="$(tmux capture-pane -t "${SESSION}" -pe)"
if ! printf '%s' "${LAST_PANE}" | grep -q $'hello\e\\[7mw'; then
    fail "expected block cursor on the 'w' between 'hello' and 'world' after merge"
fi

assert_no_crash
pass
