#!/usr/bin/env bash
set -euo pipefail
DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"; source "$DIR/lib.sh"

# Behavior: emptying the Temporary Domain leaves a SECTION, not a void — the
# panel keeps its dotted identity and says it is empty, and the notebook above
# is untouched. The panel no longer seeds a placeholder node of its own, so the
# node to delete is one you made.
#
# Repro:
#   1. Type a node in the notebook.
#   2. Down into the temp panel and type a scratch node.
#   3. Delete it (ctrl+d).
#
# Expected: the dashed ◌ panel is still there, now reading as empty.

setup; launch

type "keep"
wait_for "○ keep"

send Down                 # into the temp panel, which starts empty
wait_for "◌"

type "scratch"            # the one temp node this test will delete
wait_for "◌ scratch"

send C-d                  # delete the (childless) temp node
sleep 0.3

# the section must not become a void — the panel still says what it is
wait_for "◌"
assert_not_contains "◌ scratch"
assert_contains "○ keep"  # the notebook node is untouched

assert_no_crash
pass
