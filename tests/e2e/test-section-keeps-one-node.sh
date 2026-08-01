#!/usr/bin/env bash
set -euo pipefail
DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"; source "$DIR/lib.sh"

# Behavior: a section always keeps at least one empty node — deleting the last
# node in the Temporary Domain re-creates a fresh empty (worker) node, so the
# section is never empty. No pi needed.
#
# Repro:
#   1. Type a node in the notebook.
#   2. Down into the temp panel (focus the ✦ worker placeholder).
#   3. Delete it (ctrl+d).
#
# Expected: a dashed ◌ node is still present (re-created), not an empty section.

setup; launch

type "keep"
wait_for "○ keep"

send Down                 # focus the temp worker
wait_for "◌"
assert_contains "✦"       # the temp worker placeholder is there

send C-d                  # delete the (childless) temp worker
sleep 0.3

# the section must not be empty — a fresh node is re-created
wait_for "◌"
assert_contains "◌"
assert_contains "○ keep"  # the notebook node is untouched

assert_no_crash
pass
