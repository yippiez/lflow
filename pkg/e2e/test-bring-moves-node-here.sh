#!/usr/bin/env bash
set -euo pipefail
DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"; source "$DIR/lib.sh"

# /bring picks another node via the fuzzy finder and MOVES it (the real node, its
# subtree too) to the cursor location, leaving its old spot. Here: three root
# nodes alpha/beta/gamma; from alpha we /bring gamma, so the order becomes
# alpha, gamma, beta and gamma no longer sits last.

setup; launch

type "alpha"
send Enter
type "beta"
send Enter
type "gamma"
wait_for "○ gamma"

# save so the finder (DB-backed) can locate the nodes
send C-s

# go up to alpha
send Up
send Up
wait_for "○ alpha"

# open the slash menu and choose /bring (the menu description is unique, so it
# can't be confused with the "/bring" text typed inline while filtering)
send "/"
type "bring"
wait_for "Bring another node"
send Enter

# the finder is open in /bring mode — its hint line is unique to this action
wait_for "Enter bring this node here" 5
type "gamma"
send Enter
wait_for "brought here" 5

# gamma must now render immediately after alpha (its old trailing slot is gone)
out="$(snapshot)"
echo "$out"
python3 - "$out" <<'PY'
import sys, re
s = sys.argv[1]
seen = []
for x in re.findall(r'(alpha|beta|gamma)', s):
    if x not in seen:
        seen.append(x)
assert seen[:3] == ["alpha", "gamma", "beta"], f"unexpected order: {seen}"
print("ORDER OK", seen)
PY
pass
