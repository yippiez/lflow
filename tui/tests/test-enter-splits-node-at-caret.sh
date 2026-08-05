#!/usr/bin/env bash
set -euo pipefail
DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"; source "$DIR/lib.sh"

# Regression: Enter in the middle of a node's text must split it — text before
# the caret stays in the current node; text after becomes a new sibling below.
#
# Repro: type "helloworld", move Left 5 times (caret lands between "hello" and
# "world"), press Enter. Assert two nodes: "hello" (first) and "world" (second),
# with the caret at the start of "world".
#
# Fix commit: a47abe7

setup; launch

# Type the word that will be split.
type "helloworld"

# Move the caret 5 columns left so it sits between "hello" and "world".
send Left Left Left Left Left

# Press Enter — must split at the caret.
send Enter

# After the split the outline must show two sibling nodes.
wait_for "○ hello"
wait_for "○ world"

# The first node keeps the text before the caret.
assert_contains "○ hello"
# The second node holds the text after the caret.
assert_contains "○ world"
# The combined "helloworld" must no longer exist as a single node.
assert_not_contains "○ helloworld"

# Verify ordering in the pane: "hello" appears above "world".
pane="$(snapshot)"
hello_line="$(printf '%s\n' "$pane" | grep -n '○ hello' | head -1 | cut -d: -f1)"
world_line="$(printf '%s\n' "$pane" | grep -n '○ world' | head -1 | cut -d: -f1)"
if ! (( hello_line < world_line )); then
    fail "expected 'hello' above 'world', got hello on line ${hello_line}, world on line ${world_line}"
fi

# Caret must rest at the START of "world" (the second/new node is focused). Type
# a sentinel and confirm it lands before "world", yielding "Xworld" — not
# "worldX" (caret at end) and not "hello" gaining a char (focus on wrong node).
type "X"
wait_for "○ Xworld"
assert_contains "○ Xworld"
assert_not_contains "○ worldX"
assert_not_contains "○ helloX"
assert_not_contains "○ Xhello"

assert_no_crash
pass
