#!/usr/bin/env bash
set -euo pipefail
DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"; source "$DIR/lib.sh"

# Regression: Backspace on an empty node below an empty todo must only delete
# the lower node — same as alt+d — and must NOT flip the surviving todo into a
# plain bullet (○). The bug: merge-into-blank carried the absorbed node's type
# even when both sides were empty leaves.
#
# Repro steps (all against a :memory: DB in tmux):
#   1. setup; launch
#   2. make the blank root node a Todo via /type
#   3. Enter → second empty todo (continueOnEnter)
#   4. BSpace once → demotes the second empty todo to a bullet (○)
#   5. BSpace again → should delete that empty bullet only
#
# Expected: a single empty todo (□) remains; the survivor must not become ○.

setup; launch

# Step 2: convert the starting blank node to Todo.
send /
send t
send y
send p
send e
wait_for "/type"
send Enter
wait_for "type to search"
send t
send o
send d
send o
wait_for "Todo"
send Enter
wait_for "□"

# Step 3: sibling below continues as todo.
send Enter
# two empty todos visible
wait_for "□"

# Step 4: first BSpace on the empty second todo demotes it to a plain bullet.
send BSpace
wait_for "○"
assert_contains "□"
assert_contains "○"

# Step 5: second BSpace deletes the empty bullet. Survivor must stay a todo.
send BSpace

wait_for "□"
pane="$(snapshot)"
if ! printf '%s\n' "$pane" | grep -q '□'; then
    fail "expected surviving empty todo □, but □ is gone (type was rewritten to bullet)"
fi

# No empty bullet row left as the sole survivor of the pair.
empty_bullet="$(printf '%s\n' "$pane" | grep -cE '^[[:space:]]*○[[:space:]]*$' || true)"
if (( empty_bullet > 0 )); then
    fail "expected empty bullet gone after second BSpace, found empty ○ row"
fi

assert_no_crash
pass
