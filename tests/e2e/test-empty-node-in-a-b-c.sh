#!/usr/bin/env bash
set -euo pipefail
DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"; source "$DIR/lib.sh"

# Repro: pressing Enter on an EXPANDED parent (a, with children b,c) must create
# an empty FIRST child of a, not a sibling of a. Build a>b,c by typing, then put
# the cursor on a, go to End, press Enter, type X, and assert X lands as a's
# first child above b.

setup; launch

# Build:  a
#         ├─ b
#         ╰─ c
type "a"; send Enter
type "b"; send Tab        # b becomes a child of a
send Enter
type "c"                  # c is a sibling of b (second child of a)

wait_for "╰─ ○ c"
assert_contains "├─ ○ b"

# Move cursor up onto the parent a, jump to end of its text, press Enter.
send Up; send Up          # c -> b -> a
send End
send Enter
type "X"

# X must be a's NEW first child, above b.
wait_for "├─ ○ X"
assert_contains "├─ ○ b"
assert_contains "╰─ ○ c"

# Verify ordering: X appears before b which appears before c.
pane="$(snapshot)"
x_line="$(printf '%s\n' "$pane" | grep -n '○ X' | head -1 | cut -d: -f1)"
b_line="$(printf '%s\n' "$pane" | grep -n '○ b' | head -1 | cut -d: -f1)"
c_line="$(printf '%s\n' "$pane" | grep -n '○ c' | head -1 | cut -d: -f1)"
if ! (( x_line < b_line && b_line < c_line )); then
    fail "expected order X < b < c, got lines X=${x_line} b=${b_line} c=${c_line}"
fi

assert_no_crash
pass
